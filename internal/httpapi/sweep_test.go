package httpapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/gitread"
	"github.com/kurofune/beads-loom/internal/registry"
	internalsweep "github.com/kurofune/beads-loom/internal/sweep"
)

const sweepHTTPDriverName = "loom-http-sweep-test"

func init() { sql.Register(sweepHTTPDriverName, sweepHTTPDriver{}) }

type sweepHTTPDriver struct{}

func (sweepHTTPDriver) Open(string) (driver.Conn, error) { return sweepHTTPConn{}, nil }

type sweepHTTPConn struct{}

func (sweepHTTPConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare not supported")
}
func (sweepHTTPConn) Close() error              { return nil }
func (sweepHTTPConn) Begin() (driver.Tx, error) { return nil, errors.New("transactions not supported") }

func (sweepHTTPConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "AS in_status_since"):
		return &sweepHTTPRows{columns: []string{"id", "title", "status", "priority", "assignee", "started_at", "in_status_since", "updated_at", "ready"}}, nil
	case strings.Contains(query, "SELECT COUNT(*)"):
		return &sweepHTTPRows{columns: []string{"count"}, values: [][]driver.Value{{int64(0)}}}, nil
	case strings.Contains(query, "SELECT\n  i.id"):
		return &sweepHTTPRows{columns: []string{
			"id", "title", "issue_type", "status", "priority", "assignee", "estimated_minutes",
			"created_at", "updated_at", "closed_at", "due_at", "defer_until", "pinned", "ready",
			"not_ready", "blocked_by", "blocks", "parent_id", "parent_title",
			"child_total", "child_closed", "child_started", "missing_acceptance", "missing_close_reason",
		}}, nil
	case strings.HasPrefix(query, "SELECT issue_id, label"):
		return &sweepHTTPRows{columns: []string{"issue_id", "label"}}, nil
	case strings.Contains(query, "SUBSTRING_INDEX(id,'-',1) AS prefix"):
		return &sweepHTTPRows{columns: []string{"prefix", "count"}, values: [][]driver.Value{{"loom", int64(1)}}}, nil
	default:
		return nil, errors.New("unexpected sweep query: " + query)
	}
}

type sweepHTTPRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *sweepHTTPRows) Columns() []string { return r.columns }
func (*sweepHTTPRows) Close() error        { return nil }
func (r *sweepHTTPRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

type sweepHTTPGit struct{ commits []gitread.Commit }

func (g sweepHTTPGit) DefaultBranch(context.Context, string) (string, string, error) {
	return "main", "test", nil
}
func (g sweepHTTPGit) Log(context.Context, string, string, int) ([]gitread.Commit, error) {
	return append([]gitread.Commit(nil), g.commits...), nil
}

func newProductionSweepServer(t *testing.T, git internalsweep.GitReader, bdRunner *bdexec.MockRunner) (*Server, string) {
	t.Helper()
	workdir := t.TempDir()
	reg := registry.New(&config.Config{Projects: []config.Project{{Name: "loom", Workdir: workdir}}})
	t.Cleanup(func() { reg.Close() })
	db, err := sql.Open(sweepHTTPDriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := New(Options{Registry: reg, BD: bdexec.New(bdRunner), Git: git})
	s.queryDB = func(ctx context.Context, project string, fn func(context.Context, *sql.DB) error) error {
		if project != "loom" {
			t.Fatalf("sweep project = %q, want loom", project)
		}
		return fn(ctx, db)
	}
	return s, workdir
}

// reopenIntent is the typed apply intent a dangling-in-progress finding carries;
// apply reads it instead of the displayed argv.
var reopenIntent = internalsweep.ApplyIntent{IssueID: "loom-stale", Status: "open"}

type capturedSweepRequest struct {
	project, workdir, branch string
	window                   int
	includeDismissed         bool
}

func newSweepServer(t *testing.T, run func(context.Context, string, string, string, int, bool) (internalsweep.Report, error)) (*Server, *bdexec.MockRunner, string) {
	t.Helper()
	workdir := t.TempDir()
	reg := registry.New(&config.Config{Projects: []config.Project{{Name: "loom", Workdir: workdir}}})
	t.Cleanup(func() { reg.Close() })
	bdRunner := bdexec.NewMockRunner()
	return New(Options{Registry: reg, BD: bdexec.New(bdRunner), Sweep: run}), bdRunner, workdir
}

func TestSweepEndpointRoundTripsParametersAndRejectsUnknown(t *testing.T) {
	var calls []capturedSweepRequest
	s, _, workdir := newSweepServer(t, func(_ context.Context, project, gotWorkdir, branch string, window int, includeDismissed bool) (internalsweep.Report, error) {
		calls = append(calls, capturedSweepRequest{project, gotWorkdir, branch, window, includeDismissed})
		return internalsweep.Report{
			SweptAt: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC), Branch: branch, Window: window,
			Findings: []internalsweep.Finding{}, Partial: []internalsweep.Partial{}, Counts: map[string]int{},
		}, nil
	})

	rec := do(t, s, http.MethodGet, "/api/projects/loom/sweep?window=750&branch=release%2Fv1&includeDismissed=true", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if len(calls) != 1 || calls[0] != (capturedSweepRequest{"loom", workdir, "release/v1", 750, true}) {
		t.Fatalf("calls = %#v", calls)
	}
	var report internalsweep.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Branch != "release/v1" || report.Window != 750 {
		t.Fatalf("response = %#v", report)
	}

	unknown := do(t, s, http.MethodGet, "/api/projects/loom/sweep?widnow=3", "")
	if unknown.Code != http.StatusBadRequest || !strings.Contains(unknown.Body.String(), "unknown query parameter") {
		t.Fatalf("unknown param response = %d %s", unknown.Code, unknown.Body.String())
	}
	if len(calls) != 1 {
		t.Fatalf("unknown parameter reached sweep: %#v", calls)
	}
}

func TestFindingWritesRerunOriginalSweepContext(t *testing.T) {
	t.Run("apply", func(t *testing.T) {
		const fingerprint = "a1b2c3d4e5f60708"
		const digest = "material-v1"
		var calls []capturedSweepRequest
		s, _, workdir := newSweepServer(t, func(_ context.Context, project, gotWorkdir, branch string, window int, includeDismissed bool) (internalsweep.Report, error) {
			calls = append(calls, capturedSweepRequest{project, gotWorkdir, branch, window, includeDismissed})
			findings := []internalsweep.Finding{}
			if window == 2000 && branch == "release" {
				findings = append(findings, internalsweep.Finding{
					Fingerprint: fingerprint, MaterialDigest: digest, Kind: "dangling-in-progress",
					IssueIDs: []string{"loom-stale"}, Apply: reopenIntent,
					Suggested: internalsweep.Suggestion{Applicable: true},
				})
			}
			return internalsweep.Report{Window: window, Branch: branch, Findings: findings}, nil
		})

		read := do(t, s, http.MethodGet, "/api/projects/loom/sweep?window=2000&branch=release", "")
		if read.Code != http.StatusOK {
			t.Fatalf("sweep status = %d, want 200 (body: %s)", read.Code, read.Body.String())
		}
		var report internalsweep.Report
		if err := json.Unmarshal(read.Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if len(report.Findings) != 1 {
			t.Fatalf("findings = %#v, want one finding from release at window 2000", report.Findings)
		}

		applied := do(t, s, http.MethodPost, "/api/projects/loom/sweep/findings/"+report.Findings[0].Fingerprint+"/apply",
			`{"materialDigest":"`+report.Findings[0].MaterialDigest+`","window":2000,"branch":"release"}`)
		if applied.Code != http.StatusOK {
			t.Fatalf("apply status = %d, want 200 (body: %s)", applied.Code, applied.Body.String())
		}
		wantCalls := []capturedSweepRequest{
			{"loom", workdir, "release", 2000, false},
			{"loom", workdir, "release", 2000, true},
		}
		if !reflect.DeepEqual(calls, wantCalls) {
			t.Fatalf("sweep calls = %#v, want original context for read and apply %#v", calls, wantCalls)
		}
	})

	t.Run("dismiss", func(t *testing.T) {
		const fingerprint = "1122334455667788"
		const digest = "material-v1"
		var calls []capturedSweepRequest
		s, bdRunner, workdir := newSweepServer(t, func(_ context.Context, project, gotWorkdir, branch string, window int, includeDismissed bool) (internalsweep.Report, error) {
			calls = append(calls, capturedSweepRequest{project, gotWorkdir, branch, window, includeDismissed})
			findings := []internalsweep.Finding{}
			if window == 2000 && branch == "release" {
				findings = append(findings, internalsweep.Finding{
					Fingerprint: fingerprint, MaterialDigest: digest, Kind: "empty-ac-close",
				})
			}
			return internalsweep.Report{Window: window, Branch: branch, Findings: findings}, nil
		})

		read := do(t, s, http.MethodGet, "/api/projects/loom/sweep?window=2000&branch=release", "")
		if read.Code != http.StatusOK {
			t.Fatalf("sweep status = %d, want 200 (body: %s)", read.Code, read.Body.String())
		}
		var report internalsweep.Report
		if err := json.Unmarshal(read.Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if len(report.Findings) != 1 {
			t.Fatalf("findings = %#v, want one finding from release at window 2000", report.Findings)
		}

		dismissed := do(t, s, http.MethodPost, "/api/projects/loom/sweep/findings/"+report.Findings[0].Fingerprint+"/dismiss",
			`{"materialDigest":"`+report.Findings[0].MaterialDigest+`","note":"known exception","window":2000,"branch":"release"}`)
		if dismissed.Code != http.StatusOK {
			t.Fatalf("dismiss status = %d, want 200 (body: %s)", dismissed.Code, dismissed.Body.String())
		}
		wantCalls := []capturedSweepRequest{
			{"loom", workdir, "release", 2000, false},
			{"loom", workdir, "release", 2000, true},
		}
		if !reflect.DeepEqual(calls, wantCalls) {
			t.Fatalf("sweep calls = %#v, want original context for read and dismiss %#v", calls, wantCalls)
		}
		if got := bdRunner.Calls(); len(got) != 1 || !reflect.DeepEqual(got[0].Args[:3], []string{"kv", "set", internalsweep.DismissalKey(fingerprint)}) {
			t.Fatalf("dismiss calls = %#v", got)
		}
	})
}

func TestApplyFindingRerunsSweepAndExecutesServerSuggestion(t *testing.T) {
	const fingerprint = "a1b2c3d4e5f60708"
	const digest = "material-v1"
	runs := 0
	s, bdRunner, _ := newSweepServer(t, func(context.Context, string, string, string, int, bool) (internalsweep.Report, error) {
		runs++
		return internalsweep.Report{Findings: []internalsweep.Finding{{
			Fingerprint: fingerprint, MaterialDigest: digest, Kind: "dangling-in-progress",
			IssueIDs: []string{"loom-stale"}, Apply: reopenIntent,
			Suggested: internalsweep.Suggestion{Applicable: true, Steps: reopenIntent.ReopenSteps()},
		}}}, nil
	})

	rec := do(t, s, http.MethodPost, "/api/projects/loom/sweep/findings/"+fingerprint+"/apply", `{"materialDigest":"`+digest+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if runs != 1 {
		t.Fatalf("sweep runs = %d, want exactly one server-side refresh", runs)
	}
	calls := bdRunner.Calls()
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, []string{"update", "loom-stale", "--status", "open"}) {
		t.Fatalf("bd calls = %#v", calls)
	}
	project, err := s.reg.Get("loom")
	if err != nil {
		t.Fatal(err)
	}
	if project.Generation != 1 {
		t.Fatalf("generation = %d, want MarkWritten after apply", project.Generation)
	}
}

func TestApplyUntrackedCommitSubstitutesCreatedIssueID(t *testing.T) {
	const fingerprint = "feedfacecafebeef"
	const digest = "sha-only-digest"
	description := "Retroactive bead for abc123 on main (2026-08-04); the subject cited no bead id."
	intent := internalsweep.ApplyIntent{
		Title: "feat: shipped without a bead", Type: "chore", Priority: 3,
		Description: description, SHA: "abc123",
	}
	s, bdRunner, _ := newSweepServer(t, func(context.Context, string, string, string, int, bool) (internalsweep.Report, error) {
		return internalsweep.Report{Findings: []internalsweep.Finding{{
			Fingerprint: fingerprint, MaterialDigest: digest, Kind: "untracked-commit",
			Commits: []string{"abc123"}, Apply: intent,
			Suggested: internalsweep.Suggestion{Applicable: true, Steps: intent.RetroactiveBeadSteps()},
		}}}, nil
	})
	bdRunner.Stdout = "loom-new\n"

	rec := do(t, s, http.MethodPost, "/api/projects/loom/sweep/findings/"+fingerprint+"/apply", `{"materialDigest":"`+digest+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	want := [][]string{
		{"create", "--title", "feat: shipped without a bead", "--type", "chore", "--priority", "3", "--description", description, "--silent"},
		{"close", "loom-new", "--reason", "shipped in abc123"},
	}
	calls := bdRunner.Calls()
	if len(calls) != len(want)+1 {
		t.Fatalf("calls = %#v, want create, close, and applied-resolution dismissal", calls)
	}
	for i := range want {
		if !reflect.DeepEqual(calls[i].Args, want[i]) {
			t.Errorf("call %d = %v, want %v", i, calls[i].Args, want[i])
		}
	}
	if len(calls[2].Args) != 4 || !reflect.DeepEqual(calls[2].Args[:3], []string{"kv", "set", internalsweep.DismissalKey(fingerprint)}) {
		t.Errorf("resolution call = %#v", calls[2])
	}
}

// flagValue reads the argument after flag in a recorded bd argv.
func flagValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	t.Fatalf("recorded argv %v carries no %s", args, flag)
	return ""
}

// TestApplyDerivesBdCallFromTypedFieldsNotArgvIndices is corner 3's revert
// sentinel. The displayed suggestion's argv shape is deliberately drifted away
// from what apply used to index into; the recorded bd calls must still carry the
// finding's typed apply intent — the same values the honest suggestion renders.
func TestApplyDerivesBdCallFromTypedFieldsNotArgvIndices(t *testing.T) {
	const fingerprint = "feedfacecafebeef"
	const digest = "sha-only-digest"
	intent := internalsweep.ApplyIntent{
		Title:       "feat: shipped without a bead",
		Type:        "chore",
		Priority:    3,
		Description: "Retroactive bead for abc123 on main (2026-08-04); the subject cited no bead id.",
		SHA:         "abc123",
	}
	displayed := intent.RetroactiveBeadSteps()
	if flagValue(t, displayed[0], "--title") != intent.Title ||
		flagValue(t, displayed[0], "--description") != intent.Description ||
		flagValue(t, displayed[1], "--reason") != "shipped in "+intent.SHA {
		t.Fatalf("displayed suggestion %v does not render the typed intent %#v", displayed, intent)
	}

	shapes := map[string][][]string{
		"displayed shape": displayed,
		// What a change in internal/sweep looks like from apply's side: reordered
		// flags, an extra one, a different length. Apply must not read any of it.
		"drifted shape": {
			{"bd", "create", "--type", intent.Type, "--label", "retroactive",
				"--title", intent.Title, "--description", intent.Description, "--priority", "3"},
			{"bd", "close", "{{created}}", "--reason", "shipped in " + intent.SHA, "--force"},
		},
	}
	for name, steps := range shapes {
		t.Run(name, func(t *testing.T) {
			s, bdRunner, _ := newSweepServer(t, func(context.Context, string, string, string, int, bool) (internalsweep.Report, error) {
				return internalsweep.Report{Findings: []internalsweep.Finding{{
					Fingerprint: fingerprint, MaterialDigest: digest, Kind: "untracked-commit",
					Commits: []string{intent.SHA}, Apply: intent,
					Suggested: internalsweep.Suggestion{Applicable: true, Steps: steps},
				}}}, nil
			})
			bdRunner.Stdout = "loom-new\n"

			rec := do(t, s, http.MethodPost, "/api/projects/loom/sweep/findings/"+fingerprint+"/apply", `{"materialDigest":"`+digest+`"}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
			calls := bdRunner.Calls()
			if len(calls) != 3 || calls[0].Args[0] != "create" || calls[1].Args[0] != "close" {
				t.Fatalf("recorded bd calls = %#v, want create, close, and the applied-resolution dismissal", calls)
			}
			create, closed := calls[0].Args, calls[1].Args
			if got := flagValue(t, create, "--title"); got != intent.Title {
				t.Errorf("recorded create title = %q, want the typed field %q", got, intent.Title)
			}
			if got := flagValue(t, create, "--description"); got != intent.Description {
				t.Errorf("recorded create description = %q, want the typed field %q", got, intent.Description)
			}
			if got := flagValue(t, create, "--type"); got != intent.Type {
				t.Errorf("recorded create type = %q, want the typed field %q", got, intent.Type)
			}
			if got := flagValue(t, create, "--priority"); got != strconv.Itoa(intent.Priority) {
				t.Errorf("recorded create priority = %q, want the typed field %d", got, intent.Priority)
			}
			wantClose := []string{"close", "loom-new", "--reason", "shipped in " + intent.SHA}
			if !reflect.DeepEqual(closed, wantClose) {
				t.Errorf("recorded close = %v, want %v", closed, wantClose)
			}
			for _, arg := range append(append([]string{}, create...), closed...) {
				if arg == "--label" || arg == "--force" || arg == "retroactive" {
					t.Errorf("apply replayed a displayed argv token %q instead of the typed intent: %v", arg, calls)
				}
			}
		})
	}
}

func TestApplyUntrackedCommitResolvesFindingAndCannotDuplicateBead(t *testing.T) {
	values := map[string]string{}
	createCalls := 0
	bdRunner := bdexec.NewMockRunner()
	bdRunner.Handler = func(call bdexec.Call) (string, string, error) {
		switch {
		case reflect.DeepEqual(call.Args, []string{"kv", "list", "--json"}):
			encoded, err := json.Marshal(values)
			return string(encoded), "", err
		case len(call.Args) == 4 && reflect.DeepEqual(call.Args[:2], []string{"kv", "set"}):
			values[call.Args[2]] = call.Args[3]
			return "", "", nil
		case len(call.Args) > 0 && call.Args[0] == "create":
			createCalls++
			return "loom-retro\n", "", nil
		default:
			return "", "", nil
		}
	}
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	s, _ := newProductionSweepServer(t, sweepHTTPGit{commits: []gitread.Commit{{
		SHA: "abc123", Subject: "feat: shipped without a bead", AuthoredAt: now,
	}}}, bdRunner)

	read := do(t, s, http.MethodGet, "/api/projects/loom/sweep?branch=main", "")
	if read.Code != http.StatusOK {
		t.Fatalf("initial sweep status = %d, want 200 (body: %s)", read.Code, read.Body.String())
	}
	var report internalsweep.Report
	if err := json.Unmarshal(read.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 1 || report.Findings[0].Kind != "untracked-commit" {
		t.Fatalf("initial findings = %#v, want one untracked commit", report.Findings)
	}
	finding := report.Findings[0]
	body := `{"materialDigest":"` + finding.MaterialDigest + `","window":500,"branch":"main"}`
	path := "/api/projects/loom/sweep/findings/" + finding.Fingerprint + "/apply"

	applied := do(t, s, http.MethodPost, path, body)
	if applied.Code != http.StatusOK {
		t.Fatalf("apply status = %d, want 200 (body: %s)", applied.Code, applied.Body.String())
	}
	storedValue, ok := values[internalsweep.DismissalKey(finding.Fingerprint)]
	if !ok {
		t.Fatal("apply did not persist resolution in the finding dismissal record")
	}
	var stored internalsweep.Dismissal
	if err := json.Unmarshal([]byte(storedValue), &stored); err != nil {
		t.Fatalf("stored dismissal JSON: %v", err)
	}
	if stored.MaterialDigest != finding.MaterialDigest || stored.Kind != finding.Kind ||
		stored.DismissedAt.IsZero() || !strings.Contains(stored.Note, "Applied") ||
		!strings.Contains(stored.Note, stored.DismissedAt.Format(time.RFC3339)) {
		t.Fatalf("applied resolution = %#v", stored)
	}

	after := do(t, s, http.MethodGet, "/api/projects/loom/sweep?branch=main", "")
	if after.Code != http.StatusOK {
		t.Fatalf("next sweep status = %d, want 200 (body: %s)", after.Code, after.Body.String())
	}
	if err := json.Unmarshal(after.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("findings after apply = %#v, want applied finding resolved", report.Findings)
	}

	retry := do(t, s, http.MethodPost, path, body)
	if retry.Code != http.StatusNotFound {
		t.Fatalf("second apply status = %d, want 404 (body: %s)", retry.Code, retry.Body.String())
	}
	if createCalls != 1 {
		t.Fatalf("bd create calls = %d, want exactly one", createCalls)
	}
}

func TestDismissAndUndismissFindingPersistKVAndMarkWrites(t *testing.T) {
	const fingerprint = "1122334455667788"
	const digest = "material-digest"
	s, bdRunner, _ := newSweepServer(t, func(context.Context, string, string, string, int, bool) (internalsweep.Report, error) {
		return internalsweep.Report{Findings: []internalsweep.Finding{{
			Fingerprint: fingerprint, MaterialDigest: digest, Kind: "empty-ac-close",
		}}}, nil
	})

	dismiss := do(t, s, http.MethodPost, "/api/projects/loom/sweep/findings/"+fingerprint+"/dismiss",
		`{"materialDigest":"`+digest+`","note":"known historical exception"}`)
	if dismiss.Code != http.StatusOK {
		t.Fatalf("dismiss status = %d (body: %s)", dismiss.Code, dismiss.Body.String())
	}
	calls := bdRunner.Calls()
	if len(calls) != 1 || len(calls[0].Args) != 4 || !reflect.DeepEqual(calls[0].Args[:3], []string{"kv", "set", internalsweep.DismissalKey(fingerprint)}) {
		t.Fatalf("dismiss calls = %#v", calls)
	}
	var stored internalsweep.Dismissal
	if err := json.Unmarshal([]byte(calls[0].Args[3]), &stored); err != nil {
		t.Fatalf("stored dismissal JSON: %v", err)
	}
	if stored.MaterialDigest != digest || stored.Kind != "empty-ac-close" || stored.Note != "known historical exception" || stored.DismissedAt.IsZero() {
		t.Fatalf("stored dismissal = %#v", stored)
	}

	undismiss := do(t, s, http.MethodDelete, "/api/projects/loom/sweep/findings/"+fingerprint+"/dismiss", "")
	if undismiss.Code != http.StatusOK {
		t.Fatalf("undismiss status = %d (body: %s)", undismiss.Code, undismiss.Body.String())
	}
	calls = bdRunner.Calls()
	if len(calls) != 2 || !reflect.DeepEqual(calls[1].Args, []string{"kv", "clear", internalsweep.DismissalKey(fingerprint)}) {
		t.Fatalf("undismiss calls = %#v", calls)
	}
	project, err := s.reg.Get("loom")
	if err != nil {
		t.Fatal(err)
	}
	if project.Generation != 2 {
		t.Fatalf("generation = %d, want two marked writes", project.Generation)
	}
}

func TestApplyFindingPassesBdRefusalStderrVerbatim(t *testing.T) {
	const fingerprint = "deadbeefdeadbeef"
	const stderr = "refusing to reopen guarded issue; use --force only after operator review\n"
	s, bdRunner, _ := newSweepServer(t, func(context.Context, string, string, string, int, bool) (internalsweep.Report, error) {
		return internalsweep.Report{Findings: []internalsweep.Finding{{
			Fingerprint: fingerprint, MaterialDigest: "v1", Kind: "dangling-in-progress",
			IssueIDs:  []string{"loom-guarded"},
			Apply:     internalsweep.ApplyIntent{IssueID: "loom-guarded", Status: "open"},
			Suggested: internalsweep.Suggestion{Applicable: true},
		}}}, nil
	})
	bdRunner.Stderr = stderr
	bdRunner.Err = errors.New("exit status 1")

	rec := do(t, s, http.MethodPost, "/api/projects/loom/sweep/findings/"+fingerprint+"/apply", `{"materialDigest":"v1"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body: %s)", rec.Code, rec.Body.String())
	}
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Stderr != strings.TrimSpace(stderr) {
		t.Fatalf("stderr = %q, want verbatim diagnostic %q", body.Stderr, strings.TrimSpace(stderr))
	}
	project, err := s.reg.Get("loom")
	if err != nil {
		t.Fatal(err)
	}
	if project.Generation != 0 {
		t.Fatalf("failed apply marked project written: generation %d", project.Generation)
	}
}

func TestSweepPathReachesWriteVerbOnlyAfterApply(t *testing.T) {
	workdir := t.TempDir()
	reg := registry.New(&config.Config{Projects: []config.Project{{Name: "loom", Workdir: workdir}}})
	t.Cleanup(func() { reg.Close() })
	bdRunner := bdexec.NewMockRunner()
	bdRunner.Stdout = `{}`
	client := bdexec.New(bdRunner)
	const fingerprint = "abcdef0123456789"
	s := New(Options{
		Registry: reg, BD: client,
		Sweep: func(ctx context.Context, _, gotWorkdir, _ string, _ int, _ bool) (internalsweep.Report, error) {
			if _, err := client.KVList(ctx, gotWorkdir); err != nil {
				return internalsweep.Report{}, err
			}
			return internalsweep.Report{Findings: []internalsweep.Finding{{
				Fingerprint: fingerprint, MaterialDigest: "v1", Kind: "dangling-in-progress",
				IssueIDs: []string{"loom-stale"}, Apply: reopenIntent,
				Suggested: internalsweep.Suggestion{Applicable: true},
			}}}, nil
		},
	})

	read := do(t, s, http.MethodGet, "/api/projects/loom/sweep", "")
	if read.Code != http.StatusOK {
		t.Fatalf("sweep status = %d (body: %s)", read.Code, read.Body.String())
	}
	calls := bdRunner.Calls()
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, []string{"kv", "list", "--json"}) {
		t.Fatalf("read sweep reached write argv: %#v", calls)
	}

	applied := do(t, s, http.MethodPost, "/api/projects/loom/sweep/findings/"+fingerprint+"/apply", `{"materialDigest":"v1"}`)
	if applied.Code != http.StatusOK {
		t.Fatalf("apply status = %d (body: %s)", applied.Code, applied.Body.String())
	}
	calls = bdRunner.Calls()
	if len(calls) != 3 || !reflect.DeepEqual(calls[1].Args, []string{"kv", "list", "--json"}) ||
		!reflect.DeepEqual(calls[2].Args, []string{"update", "loom-stale", "--status", "open"}) {
		t.Fatalf("calls after apply = %#v", calls)
	}
}

func TestSweepProductionPathInvokesOnlyBdKVRead(t *testing.T) {
	bdRunner := bdexec.NewMockRunner()
	bdRunner.Stdout = `{}`
	s, _ := newProductionSweepServer(t, sweepHTTPGit{}, bdRunner)

	read := do(t, s, http.MethodGet, "/api/projects/loom/sweep?branch=main", "")
	if read.Code != http.StatusOK {
		t.Fatalf("sweep status = %d, want 200 (body: %s)", read.Code, read.Body.String())
	}
	calls := bdRunner.Calls()
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, []string{"kv", "list", "--json"}) {
		t.Fatalf("production sweep reached non-read bd argv: %#v", calls)
	}
}

func TestSweepEndpointPassesBdReadFailureStderrVerbatim(t *testing.T) {
	failing := bdexec.NewMockRunner()
	failing.Stderr = "dolt server unavailable on configured port\n"
	failing.Err = errors.New("exit status 1")
	s, _, _ := newSweepServer(t, func(ctx context.Context, _, workdir, _ string, _ int, _ bool) (internalsweep.Report, error) {
		_, err := bdexec.New(failing).KVList(ctx, workdir)
		return internalsweep.Report{}, err
	})

	rec := do(t, s, http.MethodGet, "/api/projects/loom/sweep", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body: %s)", rec.Code, rec.Body.String())
	}
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Stderr != "dolt server unavailable on configured port" {
		t.Fatalf("stderr = %q", body.Stderr)
	}
}

func TestApplyFindingRejectsChangedUnknownAndNonApplicable(t *testing.T) {
	const fingerprint = "1234567890abcdef"
	tests := []struct {
		name        string
		pathFP      string
		body        string
		applicable  bool
		wantStatus  int
		wantMessage string
	}{
		{"changed digest", fingerprint, `{"materialDigest":"old"}`, true, http.StatusConflict, "finding changed"},
		{"unknown fingerprint", "ffffffffffffffff", `{"materialDigest":"v1"}`, true, http.StatusNotFound, "no current sweep finding"},
		{"human template", fingerprint, `{"materialDigest":"v1"}`, false, http.StatusUnprocessableEntity, "requires human context"},
		{"unknown body field", fingerprint, `{"materialDigest":"v1","steps":[]}`, true, http.StatusBadRequest, "unknown field"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, runner, _ := newSweepServer(t, func(context.Context, string, string, string, int, bool) (internalsweep.Report, error) {
				return internalsweep.Report{Findings: []internalsweep.Finding{{
					Fingerprint: fingerprint, MaterialDigest: "v1", Kind: "empty-ac-close",
					Suggested: internalsweep.Suggestion{Applicable: tc.applicable},
				}}}, nil
			})
			rec := do(t, s, http.MethodPost, "/api/projects/loom/sweep/findings/"+tc.pathFP+"/apply", tc.body)
			if rec.Code != tc.wantStatus || !strings.Contains(rec.Body.String(), tc.wantMessage) {
				t.Fatalf("response = %d %s, want %d containing %q", rec.Code, rec.Body.String(), tc.wantStatus, tc.wantMessage)
			}
			if calls := runner.Calls(); len(calls) != 0 {
				t.Fatalf("rejected apply reached bd: %#v", calls)
			}
		})
	}
}
