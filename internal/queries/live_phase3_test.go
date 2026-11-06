package queries_test

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/loomtest"
	"github.com/kurofune/beads-loom/internal/loomtest/livedolt"
	"github.com/kurofune/beads-loom/internal/queries"
)

// TestDoltStatusAgainstSeededDB exercises the sync query against the real dolt
// system tables. This repo has no dolt remote configured, so it is the
// "remote:null, ahead/behind omitted" branch that gets covered here.
func TestDoltStatusAgainstSeededDB(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()

	st, err := queries.DoltStatusFor(ctx, db)
	if err != nil {
		t.Fatalf("DoltStatusFor: %v", err)
	}

	if st.Branch == "" {
		t.Error("branch was not resolved from active_branch()")
	}
	if st.LastCommit == nil {
		t.Fatal("a seeded database should have at least one dolt commit")
	}
	if st.LastCommit.Hash == "" || st.LastCommit.Message == "" {
		t.Errorf("lastCommit = %+v, want hash and message populated", st.LastCommit)
	}
	if st.LastCommit.When.IsZero() {
		t.Error("lastCommit.when is zero")
	}
	if st.LastCommit.When.Location() != time.UTC {
		t.Errorf("lastCommit.when is not UTC: %v", st.LastCommit.When.Location())
	}

	if st.Remote == nil {
		// No remote: ahead/behind must be absent rather than a misleading zero.
		if st.Ahead != nil || st.Behind != nil {
			t.Errorf("no remote configured but ahead=%v behind=%v", st.Ahead, st.Behind)
		}
	} else {
		// With a remote and a fetched tracking branch, both must be present.
		if st.Ahead != nil && *st.Ahead < 0 {
			t.Errorf("ahead = %d", *st.Ahead)
		}
		if st.Behind != nil && *st.Behind < 0 {
			t.Errorf("behind = %d", *st.Behind)
		}
	}
	t.Logf("dolt status: dirty=%v branch=%s remote=%v lastCommit=%q",
		st.Dirty, st.Branch, st.Remote, st.LastCommit.Message)
}

// TestNowAgainstSeededDB checks the cross-project Now query against real data.
func TestNowAgainstSeededDB(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	entries, err := queries.NowFor(ctx, db, "loom", now)
	if err != nil {
		t.Fatalf("NowFor: %v", err)
	}

	// Cross-check against the list query: Now must return exactly the visible
	// in_progress issues.
	list, err := queries.ListIssues(ctx, db, queries.Filter{
		Statuses: []string{queries.StatusInProgress},
		Limit:    queries.MaxLimit,
	})
	if err != nil {
		t.Fatal(err)
	}

	inList := map[string]bool{}
	for _, iss := range list.Issues {
		if loomtest.IsScratch(iss.Title) {
			continue
		}
		inList[iss.ID] = true
	}
	inNow := map[string]bool{}
	for _, e := range entries {
		if loomtest.IsScratch(e.Issue.Title) {
			continue
		}
		inNow[e.Issue.ID] = true
	}
	for id := range inList {
		if !inNow[id] {
			t.Errorf("%s is in_progress in the list but missing from /now", id)
		}
	}
	for id := range inNow {
		if !inList[id] {
			t.Errorf("%s is in /now but not in the in_progress list", id)
		}
	}

	for _, e := range entries {
		if e.Project != "loom" {
			t.Errorf("%s: project = %q", e.Issue.ID, e.Project)
		}
		if e.Issue.Status != queries.StatusInProgress {
			t.Errorf("%s: status = %q, want in_progress", e.Issue.ID, e.Issue.Status)
		}
		// in_progress is not an open status, so nothing here can be ready.
		if e.Issue.Ready {
			t.Errorf("%s: in_progress issues must not be ready", e.Issue.ID)
		}
		// inStatusSince falls back through started_at → last status_changed →
		// updated_at, so it is never null for a real row.
		if e.InStatusSince == nil {
			t.Errorf("%s: inStatusSince is null; the fallback chain should always resolve", e.Issue.ID)
		} else if e.InStatusSince.Location() != time.UTC {
			t.Errorf("%s: inStatusSince is not UTC", e.Issue.ID)
		}
		if e.InStatusSince != nil && e.InStatusSince.After(now.Add(time.Minute)) {
			t.Errorf("%s: inStatusSince %v is in the future", e.Issue.ID, e.InStatusSince)
		}
	}

	// Entries are ordered oldest-first so the most neglected work leads.
	for i := 1; i < len(entries); i++ {
		a, b := entries[i-1].InStatusSince, entries[i].InStatusSince
		if a != nil && b != nil && a.After(*b) {
			t.Errorf("entries out of order at %d: %v then %v", i, a, b)
		}
	}
}

// TestNowStaleFlag checks the 24h rule against the seeded rows by evaluating
// the same query at two different "now" values.
func TestNowStaleFlag(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()

	fresh, err := queries.NowFor(ctx, db, "loom", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) == 0 {
		t.Skip("no in-progress issues in the fixture")
	}

	// Far in the future, everything is stale.
	future, err := queries.NowFor(ctx, db, "loom", time.Now().UTC().Add(365*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range future {
		if !e.Stale {
			t.Errorf("%s: should be stale when evaluated a year later", e.Issue.ID)
		}
	}

	// Far in the past, nothing is.
	past, err := queries.NowFor(ctx, db, "loom", time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range past {
		if e.Stale {
			t.Errorf("%s: should not be stale when evaluated before it was updated", e.Issue.ID)
		}
	}
}

// TestDoltStatusWithRemote covers the ahead/behind branch, which this repo
// cannot exercise because it has no dolt remote. It borrows a sibling beads
// workspace that does, and skips when none is available.
func TestDoltStatusWithRemote(t *testing.T) {
	db, name := openSiblingWithRemote(t)
	ctx := context.Background()

	st, err := queries.DoltStatusFor(ctx, db)
	if err != nil {
		t.Fatalf("DoltStatusFor(%s): %v", name, err)
	}
	if st.Remote == nil {
		t.Fatalf("%s was selected for having a remote but reported none", name)
	}
	if *st.Remote == "" {
		t.Error("remote name is empty")
	}
	// A remote whose tracking branch has been fetched must yield real numbers.
	if st.Ahead == nil || st.Behind == nil {
		t.Skipf("%s has remote %q but no fetched tracking branch; ahead/behind correctly omitted",
			name, *st.Remote)
	}
	if *st.Ahead < 0 || *st.Behind < 0 {
		t.Errorf("ahead=%d behind=%d", *st.Ahead, *st.Behind)
	}
	t.Logf("%s: remote=%s branch=%s ahead=%d behind=%d dirty=%v",
		name, *st.Remote, st.Branch, *st.Ahead, *st.Behind, st.Dirty)
}

// openSiblingWithRemote finds a beads workspace other than this repo that has a
// running dolt server and at least one configured remote.
func openSiblingWithRemote(t *testing.T) (*sql.DB, string) {
	t.Helper()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	// The real projects on this machine, per ARCHITECTURE.md.
	candidates := []string{
		filepath.Join(home, "Projects", "djinn"),
		filepath.Join(home, "Projects", "GameIdeas", "Project-Pheonix"),
	}

	for _, workdir := range candidates {
		// Same environment rule as every other live test, in its scanning
		// form: a candidate without a reachable server is simply not this
		// machine's sibling workspace.
		env, reason := livedolt.Probe(workdir)
		if reason != "" {
			t.Log(reason)
			continue
		}
		db, err := sql.Open("mysql", env.DSN())
		if err != nil {
			continue
		}

		var remotes int
		if err := db.QueryRow("SELECT COUNT(*) FROM dolt_remotes").Scan(&remotes); err != nil || remotes == 0 {
			db.Close()
			continue
		}
		t.Cleanup(func() { db.Close() })
		return db, filepath.Base(workdir)
	}

	t.Skip("no sibling beads workspace with a dolt remote and a running server")
	return nil, ""
}

// TestI2DeferredIsNotDepBlocked is the regression test for review finding I2.
//
// It builds the real scenario live: an epic deferred into the future, and a
// child that stays `open`. The child drops out of ready_issues with zero
// blocking dependencies, and used to be counted as dependency-blocked — telling
// the manager a number that was wrong.
//
// Note the reviewer's stated mechanism does not reproduce: verified against
// bd 1.1.2, `bd update <id> --defer <date>` moves the issue's OWN status to
// `deferred`, so a directly-deferred issue never appears among open ones. It is
// the parent-deferred child that leaks through, which this test pins.
func TestI2DeferredIsNotDepBlocked(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd is not on PATH")
	}
	db := openSeeded(t)
	workdir := loomtest.RepoRoot(t)
	ctx := context.Background()

	c := bdexec.New(bdexec.NewExecRunner())

	var ids []string
	t.Cleanup(func() {
		if len(ids) > 0 {
			if err := c.Delete(context.Background(), workdir, ids); err != nil {
				t.Errorf("CLEANUP FAILED — scratch beads %v remain: %v", ids, err)
			}
		}
	})

	before, err := queries.StatsFor(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	// A deferred epic with an open child.
	parent, err := c.Create(ctx, workdir, bdexec.CreateSpec{
		Title: loomtest.ScratchTitlePrefix + " I2 deferred parent", Type: "epic",
	})
	if parent != "" {
		ids = append(ids, parent)
	}
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := c.Create(ctx, workdir, bdexec.CreateSpec{
		Title: loomtest.ScratchTitlePrefix + " I2 child", Parent: parent,
	})
	if child != "" {
		ids = append([]string{child}, ids...) // delete the child first
	}
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	deferTo := "2030-01-01"
	if err := c.Update(ctx, workdir, []string{parent}, bdexec.UpdateFields{Defer: &deferTo}); err != nil {
		t.Fatalf("defer parent: %v", err)
	}

	// The child is the I2 shape: open, not ready, nothing blocking it.
	detail, err := queries.GetIssue(ctx, db, child)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != queries.StatusOpen {
		t.Fatalf("child status = %q, want open (the premise of this test)", detail.Status)
	}
	if detail.Ready {
		t.Fatal("child should have dropped out of ready_issues")
	}
	if detail.DepCounts.BlockedBy != 0 {
		t.Fatalf("child blockedBy = %d, want 0 — nothing blocks it", detail.DepCounts.BlockedBy)
	}

	// The fix: it reports "deferred", not "deps".
	if detail.NotReady == nil {
		t.Fatal("notReady is null for an open, unready issue")
	}
	if *detail.NotReady != queries.NotReadyDeferred {
		t.Errorf("notReady = %q, want %q", *detail.NotReady, queries.NotReadyDeferred)
	}

	// And the counts moved to the right bucket.
	after, err := queries.StatsFor(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if after.DepBlocked != before.DepBlocked {
		t.Errorf("depBlocked moved from %d to %d; a deferred child must not count as blocked",
			before.DepBlocked, after.DepBlocked)
	}
	if after.DeferScheduled != before.DeferScheduled+1 {
		t.Errorf("deferScheduled = %d, want %d", after.DeferScheduled, before.DeferScheduled+1)
	}
	if after.Health().Blocked != before.Health().Blocked {
		t.Errorf("health.blocked moved from %d to %d", before.Health().Blocked, after.Health().Blocked)
	}

	// The list row agrees with the detail.
	list, err := queries.ListIssues(ctx, db, queries.Filter{Statuses: []string{queries.StatusOpen}, Limit: queries.MaxLimit})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, iss := range list.Issues {
		if iss.ID != child {
			continue
		}
		found = true
		if iss.NotReady == nil || *iss.NotReady != queries.NotReadyDeferred {
			t.Errorf("list notReady = %v, want deferred", iss.NotReady)
		}
	}
	if !found {
		t.Error("child not found in the open list")
	}
}

// TestI2DepBlockedStillReportsDeps is the other half: a genuinely blocked issue
// must still say "deps".
func TestI2DepBlockedStillReportsDeps(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd is not on PATH")
	}
	db := openSeeded(t)
	workdir := loomtest.RepoRoot(t)
	ctx := context.Background()

	c := bdexec.New(bdexec.NewExecRunner())
	var ids []string
	t.Cleanup(func() {
		if len(ids) > 0 {
			if err := c.Delete(context.Background(), workdir, ids); err != nil {
				t.Errorf("CLEANUP FAILED — scratch beads %v remain: %v", ids, err)
			}
		}
	})

	blocker, err := c.Create(ctx, workdir, bdexec.CreateSpec{Title: loomtest.ScratchTitlePrefix + " I2 blocker"})
	if blocker != "" {
		ids = append(ids, blocker)
	}
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := c.Create(ctx, workdir, bdexec.CreateSpec{Title: loomtest.ScratchTitlePrefix + " I2 blocked"})
	if blocked != "" {
		ids = append(ids, blocked)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DepAdd(ctx, workdir, blocked, blocker, "blocks"); err != nil {
		t.Fatal(err)
	}

	detail, err := queries.GetIssue(ctx, db, blocked)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Ready {
		t.Fatal("a blocked issue should not be ready")
	}
	if detail.NotReady == nil || *detail.NotReady != queries.NotReadyDeps {
		t.Errorf("notReady = %v, want %q", detail.NotReady, queries.NotReadyDeps)
	}
}

// TestNotReadyIsNullWhenReady covers the null cases.
func TestNotReadyIsNullWhenReady(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()

	list, err := queries.ListIssues(ctx, db, queries.Filter{IncludeClosed: true, Limit: queries.MaxLimit})
	if err != nil {
		t.Fatal(err)
	}
	for _, iss := range list.Issues {
		switch {
		case iss.Ready:
			if iss.NotReady != nil {
				t.Errorf("%s: ready but notReady = %q", iss.ID, *iss.NotReady)
			}
		case iss.Status != queries.StatusOpen:
			if iss.NotReady != nil {
				t.Errorf("%s: status %q but notReady = %q", iss.ID, iss.Status, *iss.NotReady)
			}
		default:
			if iss.NotReady != nil &&
				*iss.NotReady != queries.NotReadyDeps && *iss.NotReady != queries.NotReadyDeferred {
				t.Errorf("%s: notReady = %q, not a known reason", iss.ID, *iss.NotReady)
			}
		}
	}
}
