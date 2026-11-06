package sweep

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/gitread"
)

const sweepDriverName = "loom-sweep-test"

func init() { sql.Register(sweepDriverName, sweepDriver{}) }

type sweepDriver struct{}

func (sweepDriver) Open(scenario string) (driver.Conn, error) {
	return sweepConn{scenario: scenario}, nil
}

type sweepConn struct{ scenario string }

func (sweepConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare not supported")
}
func (sweepConn) Close() error              { return nil }
func (sweepConn) Begin() (driver.Tx, error) { return nil, errors.New("transactions not supported") }

func (c sweepConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "AS in_status_since") {
		if c.scenario == "receipts" || c.scenario == "no-beads" {
			return &sweepRows{columns: []string{"id", "title", "status", "priority", "assignee", "started_at", "in_status_since", "updated_at", "ready"}}, nil
		}
		now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
		if c.scenario == "cap" || c.scenario == "volume" {
			count := 1002
			if c.scenario == "volume" {
				count = 200
			}
			values := make([][]driver.Value, count)
			for i := range values {
				values[i] = []driver.Value{
					fmt.Sprintf("loom-%04d", i), "Forgotten work", "in_progress", int64(2), "Robot",
					now.Add(-72 * time.Hour), now.Add(-72 * time.Hour), now.Add(-48 * time.Hour), false,
				}
			}
			return &sweepRows{columns: []string{"id", "title", "status", "priority", "assignee", "started_at", "in_status_since", "updated_at", "ready"}, values: values}, nil
		}
		inStatusSince := now.Add(-72 * time.Hour)
		if c.scenario == "dismiss-changed" {
			inStatusSince = now.Add(-96 * time.Hour)
		}
		return &sweepRows{
			columns: []string{"id", "title", "status", "priority", "assignee", "started_at", "in_status_since", "updated_at", "ready"},
			values: [][]driver.Value{
				{"loom-stale", "Forgotten work", "in_progress", int64(2), "Nate", inStatusSince, inStatusSince, now.Add(-48 * time.Hour), false},
				{"loom-fresh", "Active work", "in_progress", int64(1), "Robot", now.Add(-2 * time.Hour), now.Add(-2 * time.Hour), now.Add(-time.Hour), false},
			},
		}, nil
	}
	if strings.Contains(query, "SELECT COUNT(*)") {
		count := int64(0)
		if c.scenario == "receipts" {
			count = 2
		}
		return &sweepRows{columns: []string{"count"}, values: [][]driver.Value{{count}}}, nil
	}
	if strings.Contains(query, "SELECT\n  i.id") {
		if c.scenario != "receipts" {
			return &sweepRows{columns: issueListColumns()}, nil
		}
		now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
		return &sweepRows{columns: issueListColumns(), values: [][]driver.Value{
			{"loom-no-ac", "Closed without AC", "task", "closed", int64(2), "", nil, now, now, now, nil, nil, int64(0), false, nil, int64(0), int64(0), nil, nil, nil, nil, nil, true, false},
			{"loom-no-receipts", "Closed without receipts", "task", "closed", int64(3), "", nil, now, now, now, nil, nil, int64(0), false, nil, int64(0), int64(0), nil, nil, nil, nil, nil, true, true},
		}}, nil
	}
	if strings.HasPrefix(query, "SELECT issue_id, label") {
		return &sweepRows{columns: []string{"issue_id", "label"}}, nil
	}
	if strings.Contains(query, "SUBSTRING_INDEX(id,'-',1) AS prefix") {
		if c.scenario == "no-beads" {
			return &sweepRows{columns: []string{"prefix", "count"}}, nil
		}
		return &sweepRows{columns: []string{"prefix", "count"}, values: [][]driver.Value{{"loom", int64(20)}, {"djinn", int64(5)}}}, nil
	}
	return nil, errors.New("unexpected query: " + query)
}

var _ driver.QueryerContext = sweepConn{}

type sweepRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *sweepRows) Columns() []string { return r.columns }
func (*sweepRows) Close() error        { return nil }
func (r *sweepRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

func issueListColumns() []string {
	return []string{
		"id", "title", "issue_type", "status", "priority", "assignee", "estimated_minutes",
		"created_at", "updated_at", "closed_at", "due_at", "defer_until", "pinned", "ready",
		"not_ready", "blocked_by", "blocks", "parent_id", "parent_title",
		"child_total", "child_closed", "child_started", "missing_acceptance", "missing_close_reason",
	}
}

func TestRunUsesNowStalenessForDanglingInProgress(t *testing.T) {
	db, err := sql.Open(sweepDriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	report, err := Run(t.Context(), Input{
		DB: db, Project: "loom", Workdir: "/project",
		Now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %#v, want one stale issue", report.Findings)
	}
	got := report.Findings[0]
	if got.Kind != "dangling-in-progress" || got.Severity != "medium" ||
		!reflect.DeepEqual(got.IssueIDs, []string{"loom-stale"}) {
		t.Fatalf("finding = %#v", got)
	}
	if len(got.Fingerprint) != 16 || got.MaterialDigest == "" {
		t.Fatalf("identity fields = fingerprint %q digest %q", got.Fingerprint, got.MaterialDigest)
	}
	wantSteps := [][]string{{"bd", "update", "loom-stale", "--status", "open"}}
	if !got.Suggested.Applicable || !reflect.DeepEqual(got.Suggested.Steps, wantSteps) {
		t.Fatalf("suggestion = %#v, want %#v", got.Suggested, wantSteps)
	}
	if report.Counts["dangling-in-progress"] != 1 {
		t.Fatalf("counts = %#v", report.Counts)
	}
}

func TestRunClassifiesMissingReceiptsFromListIssues(t *testing.T) {
	db, err := sql.Open(sweepDriverName, "receipts")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	report, err := Run(t.Context(), Input{
		DB: db, Project: "loom", Workdir: "/project",
		Now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Counts["empty-ac-close"] != 2 || report.Counts["empty-reason-close"] != 1 {
		t.Fatalf("counts = %#v", report.Counts)
	}
	for _, finding := range report.Findings {
		if finding.Kind != "empty-ac-close" && finding.Kind != "empty-reason-close" {
			continue
		}
		if finding.Suggested.Applicable {
			t.Errorf("receipt finding unexpectedly applicable: %#v", finding)
		}
		if finding.MaterialDigest == "" || len(finding.IssueIDs) != 1 {
			t.Errorf("receipt finding missing material identity: %#v", finding)
		}
		var want []string
		switch finding.Kind {
		case "empty-ac-close":
			want = []string{"bd", "update", finding.IssueIDs[0], "--acceptance", "<what done meant>"}
		case "empty-reason-close":
			want = []string{"bd", "close", finding.IssueIDs[0], "--reason", "<why it closed>"}
		}
		if len(finding.Suggested.Steps) != 1 || !reflect.DeepEqual(finding.Suggested.Steps[0], want) {
			t.Errorf("%s suggestion = %#v, want %v", finding.Kind, finding.Suggested.Steps, want)
		}
	}
}

type fakeGit struct {
	branch string
	method string
	log    []gitread.Commit
}

type fakeKV map[string]string

func (kv fakeKV) KVList(context.Context, string) (map[string]string, error) {
	return kv, nil
}

func (g *fakeGit) DefaultBranch(context.Context, string) (string, string, error) {
	return g.branch, g.method, nil
}

func (g *fakeGit) Log(context.Context, string, string, int) ([]gitread.Commit, error) {
	return append([]gitread.Commit(nil), g.log...), nil
}

func TestRunMatchesOnlyProjectPrefixesForUntrackedCommits(t *testing.T) {
	db, err := sql.Open(sweepDriverName, "receipts")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	git := &fakeGit{branch: "main", method: "origin/HEAD", log: []gitread.Commit{
		{SHA: "aaa111", AuthoredAt: now.Add(-time.Hour), Subject: "docs: post-mortem notes"},
		{SHA: "bbb222", AuthoredAt: now.Add(-2 * time.Hour), Subject: "feat: tracked work (loom-abc)"},
		{SHA: "ccc333", AuthoredAt: now.Add(-3 * time.Hour), Subject: "refactor: read-only sweep"},
	}}

	report, err := Run(t.Context(), Input{
		DB: db, Project: "loom", Workdir: "/project", Git: git, Window: 500, Now: now,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Branch != "main" || report.Window != 500 || report.CommitsScanned != 3 {
		t.Fatalf("report metadata = branch %q window %d scanned %d", report.Branch, report.Window, report.CommitsScanned)
	}
	swept := map[string]gitread.Commit{}
	for _, commit := range git.log {
		swept[commit.SHA] = commit
	}
	var shas []string
	for _, finding := range report.Findings {
		if finding.Kind != "untracked-commit" {
			continue
		}
		shas = append(shas, finding.Commits...)
		evidence := strings.Join(finding.Evidence, "\n")
		for _, want := range []string{"main", "500", "origin/HEAD", "loom, djinn", "merges excluded"} {
			if !strings.Contains(evidence, want) {
				t.Errorf("evidence %q missing %q", evidence, want)
			}
		}
		if !finding.Suggested.Applicable || len(finding.Suggested.Steps) != 2 ||
			!reflect.DeepEqual(finding.Suggested.Steps[1], []string{"bd", "close", "{{created}}", "--reason", "shipped in " + finding.Commits[0]}) {
			t.Errorf("suggestion = %#v", finding.Suggested)
		}
		// The displayed argv and the apply endpoint's executed call have one source.
		commit := swept[finding.Commits[0]]
		wantIntent := ApplyIntent{
			Title: commit.Subject, Type: "chore", Priority: 3,
			Description: fmt.Sprintf("Retroactive bead for %s on main (%s); the subject cited no bead id.",
				commit.SHA, commit.AuthoredAt.UTC().Format("2006-01-02")),
			SHA: commit.SHA,
		}
		if finding.Apply != wantIntent {
			t.Errorf("apply intent = %#v, want %#v", finding.Apply, wantIntent)
		}
		if !reflect.DeepEqual(finding.Suggested.Steps, finding.Apply.RetroactiveBeadSteps()) {
			t.Errorf("displayed steps %v do not render from the typed intent %#v", finding.Suggested.Steps, finding.Apply)
		}
	}
	if !reflect.DeepEqual(shas, []string{"aaa111", "ccc333"}) {
		t.Fatalf("untracked shas = %v, want ordinary hyphenated English left untracked and loom id tracked", shas)
	}
}

// prefixEvidenceLines returns every "project bead prefixes used: …" evidence
// line in a report, so a test can assert on what the line actually names.
func prefixEvidenceLines(report Report) []string {
	const label = "project bead prefixes used: "
	var lines []string
	for _, finding := range report.Findings {
		for _, evidence := range finding.Evidence {
			if strings.HasPrefix(evidence, label) {
				lines = append(lines, strings.TrimPrefix(evidence, label))
			}
		}
	}
	return lines
}

// TestZeroIssueProjectPartialsInsteadOfFlaggingEveryCommit is corner 2's revert
// sentinel. Both subtests go red when beadIDPattern's nil pattern is allowed to
// wave every commit through again.
func TestZeroIssueProjectPartialsInsteadOfFlaggingEveryCommit(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	commits := []gitread.Commit{
		{SHA: "aaa111", AuthoredAt: now.Add(-time.Hour), Subject: "docs: post-mortem notes"},
		{SHA: "bbb222", AuthoredAt: now.Add(-2 * time.Hour), Subject: "feat: tracked work (loom-abc)"},
		{SHA: "ccc333", AuthoredAt: now.Add(-3 * time.Hour), Subject: "refactor: read-only sweep"},
	}
	run := func(t *testing.T, scenario string) Report {
		t.Helper()
		db, err := sql.Open(sweepDriverName, scenario)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		report, err := Run(t.Context(), Input{
			DB: db, Project: "loom", Workdir: "/project", Window: 500, Now: now,
			Git: &fakeGit{branch: "main", method: "origin/HEAD", log: commits},
		})
		if err != nil {
			t.Fatalf("Run(%s): %v", scenario, err)
		}
		return report
	}
	empty := run(t, "no-beads")

	t.Run("partial names the missing bead prefixes", func(t *testing.T) {
		for _, finding := range empty.Findings {
			if finding.Kind == "untracked-commit" {
				t.Fatalf("zero-issue project flagged commits as untracked: %#v", empty.Findings)
			}
		}
		if len(empty.Partial) != 1 || empty.Partial[0].Detector != "untracked-commit" {
			t.Fatalf("partial = %#v, want one untracked-commit partial", empty.Partial)
		}
		for _, want := range []string{"bead id prefixes", "skipped"} {
			if !strings.Contains(empty.Partial[0].Reason, want) {
				t.Errorf("partial reason %q does not name %q", empty.Partial[0].Reason, want)
			}
		}
	})

	t.Run("no finding renders an empty prefix evidence line", func(t *testing.T) {
		for _, named := range prefixEvidenceLines(empty) {
			if strings.TrimSpace(named) == "" {
				t.Errorf("zero-issue report rendered the prefix evidence line with nothing after it")
			}
		}
		// The same scan against a project that does have prefixes proves the scan
		// above is looking at a line that really exists rather than at nothing.
		populated := prefixEvidenceLines(run(t, "receipts"))
		if len(populated) == 0 {
			t.Fatal("no finding carried a prefix evidence line; the invariant scan guards nothing")
		}
		for _, named := range populated {
			if strings.TrimSpace(named) == "" {
				t.Errorf("prefix evidence line named no prefix: %q", named)
			}
		}
	})
}

func TestBeadIDPatternRefusesAnEmptyPrefixSet(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("beadIDPattern built a pattern from no prefixes; every commit would match or none would")
		}
	}()
	beadIDPattern(nil)
}

func TestDismissalSurvivesDerivedTimeAndExpiresOnMaterialChange(t *testing.T) {
	baseDB, err := sql.Open(sweepDriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { baseDB.Close() })
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	initial, err := Run(t.Context(), Input{DB: baseDB, Project: "loom", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	finding := initial.Findings[0]
	dismissedAt := now.Add(time.Minute)
	value, err := json.Marshal(Dismissal{
		MaterialDigest: finding.MaterialDigest, Kind: finding.Kind, DismissedAt: dismissedAt, Note: "expected exception",
	})
	if err != nil {
		t.Fatal(err)
	}
	kv := fakeKV{
		"memory.unrelated":                "ignore me",
		DismissalKey(finding.Fingerprint): string(value),
	}

	later, err := Run(t.Context(), Input{DB: baseDB, Project: "loom", Now: now.Add(24 * time.Hour), KV: kv})
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range later.Findings {
		if got.Fingerprint == finding.Fingerprint {
			t.Fatalf("derived time resurrected dismissal: %#v", got)
		}
	}
	including, err := Run(t.Context(), Input{DB: baseDB, Project: "loom", Now: now.Add(24 * time.Hour), KV: kv, IncludeDismissed: true})
	if err != nil {
		t.Fatal(err)
	}
	var included *Finding
	for i := range including.Findings {
		if including.Findings[i].Fingerprint == finding.Fingerprint {
			included = &including.Findings[i]
		}
	}
	if included == nil || !included.Dismissed || included.DismissedAt == nil {
		t.Fatalf("includeDismissed report = %#v", including.Findings)
	}

	changedDB, err := sql.Open(sweepDriverName, "dismiss-changed")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { changedDB.Close() })
	changed, err := Run(t.Context(), Input{DB: changedDB, Project: "loom", Now: now, KV: kv})
	if err != nil {
		t.Fatal(err)
	}
	var resurrected *Finding
	for i := range changed.Findings {
		if changed.Findings[i].Fingerprint == finding.Fingerprint {
			resurrected = &changed.Findings[i]
		}
	}
	if resurrected == nil || resurrected.Dismissed || resurrected.MaterialDigest == finding.MaterialDigest {
		t.Fatalf("material change did not resurrect finding: %#v", changed.Findings)
	}
}

func TestRunCapsEachFindingKindAndReportsPartialProgress(t *testing.T) {
	db, err := sql.Open(sweepDriverName, "cap")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	report, err := Run(t.Context(), Input{
		DB: db, Project: "loom", Now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != MaxFindings || report.Counts["dangling-in-progress"] != MaxFindings {
		t.Fatalf("findings = %d count = %d, want cap %d", len(report.Findings), report.Counts["dangling-in-progress"], MaxFindings)
	}
	if len(report.Partial) != 1 || report.Partial[0].Detector != "dangling-in-progress" || !strings.Contains(report.Partial[0].Reason, "1000") {
		t.Fatalf("partial = %#v", report.Partial)
	}
}

// TestDismissalsFilterBeforeTheFindingCap is corner 1's revert sentinel: it goes
// red the moment finishReport counts dismissed findings against the per-kind cap.
func TestDismissalsFilterBeforeTheFindingCap(t *testing.T) {
	db, err := sql.Open(sweepDriverName, "cap")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	base, err := Run(t.Context(), Input{DB: db, Project: "loom", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(base.Findings) != MaxFindings {
		t.Fatalf("undismissed findings = %d, want the cap %d", len(base.Findings), MaxFindings)
	}
	if len(base.Partial) != 1 || base.Partial[0].Detector != "dangling-in-progress" ||
		base.Partial[0].Reason != fmt.Sprintf("finding cap %d reached; additional findings omitted", MaxFindings) {
		t.Fatalf("partial = %#v, want the cap partial while the live count genuinely exceeds the cap", base.Partial)
	}

	// Dismiss enough of the visible findings that the live count falls under the
	// cap. The two beads the cap hid (loom-1000, loom-1001) must now be visible.
	const dismissed = 500
	kv := fakeKV{}
	for _, finding := range base.Findings[:dismissed] {
		value, err := json.Marshal(Dismissal{
			MaterialDigest: finding.MaterialDigest, Kind: finding.Kind,
			DismissedAt: now, Note: "known exception",
		})
		if err != nil {
			t.Fatal(err)
		}
		kv[DismissalKey(finding.Fingerprint)] = string(value)
	}

	after, err := Run(t.Context(), Input{DB: db, Project: "loom", Now: now, KV: kv})
	if err != nil {
		t.Fatal(err)
	}
	const totalStale = 1002
	if len(after.Findings) != totalStale-dismissed || after.Counts["dangling-in-progress"] != totalStale-dismissed {
		t.Fatalf("after dismissing %d of %d: findings = %d counts = %#v, want %d live findings visible",
			dismissed, totalStale, len(after.Findings), after.Counts, totalStale-dismissed)
	}
	visible := map[string]bool{}
	for _, finding := range after.Findings {
		if finding.Dismissed {
			t.Fatalf("dismissed finding survived the default report: %#v", finding)
		}
		visible[finding.IssueIDs[0]] = true
	}
	for _, want := range []string{"loom-1000", "loom-1001"} {
		if !visible[want] {
			t.Errorf("%s still hidden behind the cap after dismissals freed room for it", want)
		}
	}
	if len(after.Partial) != 0 {
		t.Fatalf("partial = %#v, want no omission claim once dismissals bring the live count under the cap", after.Partial)
	}
}

func TestRunHandles200BeadsAnd500CommitsWithinBound(t *testing.T) {
	db, err := sql.Open(sweepDriverName, "volume")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	commits := make([]gitread.Commit, 500)
	for i := range commits {
		commits[i] = gitread.Commit{
			SHA: fmt.Sprintf("sha%03d", i), AuthoredAt: now.Add(-time.Duration(i) * time.Minute),
			Subject: fmt.Sprintf("chore: shipped work %03d", i),
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	report, err := Run(ctx, Input{
		DB: db, Project: "loom", Workdir: "/project", Window: 500, Now: now,
		Git: &fakeGit{branch: "main", method: "origin/HEAD", log: commits},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts["dangling-in-progress"] != 200 || report.Counts["untracked-commit"] != 500 {
		t.Fatalf("counts = %#v", report.Counts)
	}
	if len(report.Findings) != 700 || report.CommitsScanned != 500 || len(report.Partial) != 0 {
		t.Fatalf("volume report = findings %d scanned %d partial %#v", len(report.Findings), report.CommitsScanned, report.Partial)
	}
}

type recordingGitRunner struct {
	calls [][]string
}

func (r *recordingGitRunner) Run(_ context.Context, _ string, args ...string) (string, string, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	return "", "", nil
}

func TestRunIsReadOnlyAcrossBdAndGitRunners(t *testing.T) {
	db, err := sql.Open(sweepDriverName, "receipts")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	bdRunner := bdexec.NewMockRunner()
	bdRunner.Stdout = `{}`
	gitRunner := &recordingGitRunner{}

	_, err = Run(t.Context(), Input{
		DB: db, Project: "loom", Workdir: "/project", Branch: "main", Window: 500,
		Now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
		Git: gitread.New(gitRunner), KV: bdexec.New(bdRunner),
	})
	if err != nil {
		t.Fatal(err)
	}
	bdCalls := bdRunner.Calls()
	if len(bdCalls) != 1 || !reflect.DeepEqual(bdCalls[0].Args, []string{"kv", "list", "--json"}) {
		t.Fatalf("sweep reached non-read bd argv: %#v", bdCalls)
	}
	allowed := map[string]bool{"log": true, "rev-parse": true, "symbolic-ref": true}
	if len(gitRunner.calls) == 0 {
		t.Fatal("sweep did not read git")
	}
	for _, call := range gitRunner.calls {
		if len(call) == 0 || !allowed[call[0]] {
			t.Fatalf("sweep reached non-read git argv: %#v", gitRunner.calls)
		}
	}
}

type failingGitRunner struct{ stderr string }

func (r failingGitRunner) Run(context.Context, string, ...string) (string, string, error) {
	return "", r.stderr, errors.New("exit status 128")
}

func TestRunReportsGitFailureAsPartialWithVerbatimStderr(t *testing.T) {
	db, err := sql.Open(sweepDriverName, "receipts")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	const stderr = "fatal: bad revision 'missing'\n"

	report, err := Run(t.Context(), Input{
		DB: db, Project: "loom", Workdir: "/project", Branch: "missing",
		Git: gitread.New(failingGitRunner{stderr: stderr}),
		Now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Partial) != 1 || report.Partial[0].Detector != "untracked-commit" || report.Partial[0].Reason != stderr {
		t.Fatalf("partial = %#v, want verbatim git stderr", report.Partial)
	}
}
