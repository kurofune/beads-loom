package queries_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/loomtest"
	"github.com/kurofune/beads-loom/internal/queries"
)

// scratchTitle marks every bead the live read-path tests create. It carries the
// prefix internal/bdexec's write-path tests agree on, so neither suite mistakes
// the other's in-flight fixture for seeded data.
const scratchTitle = loomtest.ScratchTitlePrefix + " queries live rollup test"

// scratchFixture owns the beads a live test builds for itself.
//
// It exists because these tests used to mutate whichever real open child of a
// real epic happened to sort first: that wrote status_changed events into the
// production tracker on every `go test` run, and a SIGKILL between the flip and
// its restore left a real work item falsely in_progress. A test must never
// mutate a bead it did not create.
type scratchFixture struct {
	t       *testing.T
	db      *sql.DB
	client  *bdexec.Client
	workdir string
	// ids in creation order; torn down in reverse so a parent is never deleted
	// out from under its child.
	ids []string
}

// newScratchFixture registers teardown BEFORE the first bead exists, so a
// t.Fatal anywhere in the test body — including one raised while building the
// fixture — still deletes whatever was created.
func newScratchFixture(t *testing.T, db *sql.DB) *scratchFixture {
	t.Helper()
	f := &scratchFixture{
		t:       t,
		db:      db,
		client:  bdexec.New(bdexec.NewExecRunner()),
		workdir: loomtest.RepoRoot(t),
	}
	t.Cleanup(f.teardown)
	return f
}

// create makes one scratch bead and records it for teardown.
//
// It t.Fatals rather than logging or skipping: a live test that cannot build
// the fixture it asserts against has verified nothing, and must say so.
func (f *scratchFixture) create(ctx context.Context, suffix, issueType, parent string) string {
	f.t.Helper()
	id, err := f.client.Create(ctx, f.workdir, bdexec.CreateSpec{
		Title:       scratchTitle + " " + suffix,
		Type:        issueType,
		Parent:      parent,
		Description: "Created by internal/queries live rollup tests. Deleted before the test ends.",
		Labels:      []string{"loom-scratch-test"},
	})
	// Recorded before the error check so a half-created bead is still deleted.
	if id != "" {
		f.ids = append(f.ids, id)
	}
	if err != nil {
		f.t.Fatalf("create scratch bead %s: %v", suffix, err)
	}
	if id == "" {
		f.t.Fatalf("create scratch bead %s returned an empty id", suffix)
	}
	return id
}

// setStatus flips a scratch bead through the real bd binary.
func (f *scratchFixture) setStatus(ctx context.Context, id, status string) {
	f.t.Helper()
	if err := f.client.Update(ctx, f.workdir, []string{id},
		bdexec.UpdateFields{Status: &status}); err != nil {
		f.t.Fatalf("set %s to %s: %v", id, status, err)
	}
}

// depAdd records that blocked is blocked by blocker, through the real bd binary.
func (f *scratchFixture) depAdd(ctx context.Context, blocked, blocker string) {
	f.t.Helper()
	if err := f.client.DepAdd(ctx, f.workdir, blocked, blocker, "blocks"); err != nil {
		f.t.Fatalf("make %s blocked by %s: %v", blocked, blocker, err)
	}
}

func (f *scratchFixture) setDue(ctx context.Context, id, due string) {
	f.t.Helper()
	if err := f.client.Update(ctx, f.workdir, []string{id}, bdexec.UpdateFields{Due: &due}); err != nil {
		f.t.Fatalf("set %s due date to %s: %v", id, due, err)
	}
}

// backdateClose rewrites one scratch bead's closed_at directly in SQL.
//
// This is the ONE write in these tests that does not shell out to bd, and it is
// a FIXTURE operation rather than a new write path: bd cannot close a bead in
// the past, and the trailing-window cutoff in the closed_in_window SUM cannot be
// exercised without a descendant closed before it. Product code still writes
// exclusively through bd. The id check keeps the exemption from widening — this
// refuses to touch anything the fixture did not create itself.
func (f *scratchFixture) backdateClose(ctx context.Context, id string, closedAt time.Time) {
	f.t.Helper()
	if !slices.Contains(f.ids, id) {
		f.t.Fatalf("refusing to backdate %s: not a bead this fixture created", id)
	}
	result, err := f.db.ExecContext(ctx,
		"UPDATE issues SET closed_at = ? WHERE id = ? AND status = ?", closedAt, id, queries.StatusClosed)
	if err != nil {
		f.t.Fatalf("backdate closed_at for %s: %v", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		f.t.Fatalf("backdate closed_at for %s: %v", id, err)
	}
	if affected != 1 {
		f.t.Fatalf("backdate closed_at for %s changed %d rows, want exactly the one closed scratch bead", id, affected)
	}
}

func (f *scratchFixture) close(ctx context.Context, id string) {
	f.t.Helper()
	if err := f.client.Close(ctx, f.workdir, []string{id}, bdexec.CloseOptions{Reason: "scratch health close-rate probe"}); err != nil {
		f.t.Fatalf("close scratch bead %s: %v", id, err)
	}
}

// teardown deletes every bead this fixture created and proves each one is gone.
//
// It deliberately does not compare absolute row counts: the fixture is a live
// database other agents also write to, so "the count is unchanged" is not a
// property this test can own. "Nothing I created is still here" is.
func (f *scratchFixture) teardown() {
	if len(f.ids) == 0 {
		return
	}
	ids := make([]string, 0, len(f.ids))
	for i := len(f.ids) - 1; i >= 0; i-- {
		ids = append(ids, f.ids[i])
	}
	if err := f.client.Delete(context.Background(), f.workdir, ids); err != nil {
		f.t.Errorf("CLEANUP FAILED — scratch beads %v remain: %v", ids, err)
		return
	}
	for _, id := range ids {
		if _, err := queries.GetIssue(context.Background(), f.db, id); err == nil {
			f.t.Errorf("CLEANUP FAILED — scratch bead %s is still in the fixture", id)
		} else if !errors.Is(err, queries.ErrNotFound) {
			f.t.Errorf("could not confirm scratch bead %s was deleted: %v", id, err)
		}
	}
}

// rollupSnapshot is one epic read through every surface this bead touches: the
// list statement's ChildStats, the detail statement's twin, and the recursive
// subtree rollup.
type rollupSnapshot struct {
	childTotal     int
	childClosed    int
	childStarted   int
	detailTotal    int
	detailStarted  int
	detailClosed   int
	subtreeTotal   int
	subtreeClosed  int
	subtreeStarted int
}

func epicRollups(ctx context.Context, db *sql.DB, id string) (rollupSnapshot, error) {
	var snap rollupSnapshot

	list, err := queries.ListIssues(ctx, db, queries.Filter{
		IncludeClosed: true,
		Limit:         queries.MaxLimit,
	})
	if err != nil {
		return snap, fmt.Errorf("ListIssues: %w", err)
	}
	found := false
	for _, issue := range list.Issues {
		if issue.ID != id {
			continue
		}
		found = true
		if issue.ChildStats == nil {
			return snap, fmt.Errorf("%s has no child stats in the list response", id)
		}
		snap.childTotal = issue.ChildStats.Total
		snap.childClosed = issue.ChildStats.Closed
		snap.childStarted = issue.ChildStats.Started
	}
	if !found {
		return snap, fmt.Errorf("%s is not in the list response", id)
	}

	detail, err := queries.GetIssue(ctx, db, id)
	if err != nil {
		return snap, fmt.Errorf("GetIssue: %w", err)
	}
	if detail.ChildStats == nil {
		return snap, fmt.Errorf("%s has no child stats in the detail response", id)
	}
	snap.detailTotal = detail.ChildStats.Total
	snap.detailStarted = detail.ChildStats.Started
	snap.detailClosed = detail.ChildStats.Closed

	initiatives, err := queries.InitiativesFor(ctx, db, true)
	if err != nil {
		return snap, fmt.Errorf("InitiativesFor: %w", err)
	}
	found = false
	for _, initiative := range initiatives {
		if initiative.ID != id {
			continue
		}
		found = true
		snap.subtreeTotal = initiative.Subtree.Total
		snap.subtreeClosed = initiative.Subtree.Closed
		snap.subtreeStarted = initiative.Subtree.Started
	}
	if !found {
		return snap, fmt.Errorf("%s is not in the initiatives response", id)
	}
	return snap, nil
}

// TestStartedRoundTripAgainstScratchEpic is the live round trip for AC-7: flip a
// child to in_progress with the real bd binary, refetch through both rollups,
// and assert each one moved by exactly one.
//
// The epic and the child are this test's own scratch beads. Nothing it did not
// create is ever written to.
//
// It deliberately never logs-and-passes. If the scratch fixture cannot be
// built, the test fails rather than pretending it verified something.
func TestStartedRoundTripAgainstScratchEpic(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd is not on PATH")
	}
	db := openSeeded(t)
	ctx := context.Background()

	fixture := newScratchFixture(t, db)
	epicID := fixture.create(ctx, "(round-trip epic)", "epic", "")
	childID := fixture.create(ctx, "(round-trip child)", "task", epicID)

	before, err := epicRollups(ctx, db, epicID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("epic %s, child %s: before %+v", epicID, childID, before)
	if before.childTotal != 1 || before.detailTotal != 1 || before.subtreeTotal != 1 {
		t.Fatalf("scratch epic did not adopt exactly one child: %+v", before)
	}
	if before.childStarted != 0 || before.detailStarted != 0 || before.subtreeStarted != 0 {
		t.Fatalf("a freshly created open child must not be counted started: %+v", before)
	}

	fixture.setStatus(ctx, childID, queries.StatusInProgress)

	after, err := epicRollups(ctx, db, epicID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("epic %s, child %s: after %+v", epicID, childID, after)

	if after.childStarted != before.childStarted+1 {
		t.Errorf("ChildStats.started = %d, want %d (one more in-flight direct child)",
			after.childStarted, before.childStarted+1)
	}
	if after.detailStarted != after.childStarted {
		t.Errorf("GetIssue started = %d, ListIssues started = %d; the two subselects disagree",
			after.detailStarted, after.childStarted)
	}
	if after.subtreeStarted != before.subtreeStarted+1 {
		t.Errorf("InitiativeRollup.started = %d, want %d",
			after.subtreeStarted, before.subtreeStarted+1)
	}
	// Membership and the closed bucket are untouched: started fills a hole, it
	// does not move work between existing buckets.
	if after.childTotal != before.childTotal || after.subtreeTotal != before.subtreeTotal {
		t.Errorf("totals moved: child %d→%d, subtree %d→%d",
			before.childTotal, after.childTotal, before.subtreeTotal, after.subtreeTotal)
	}
	if after.childClosed != before.childClosed || after.detailClosed != before.detailClosed ||
		after.subtreeClosed != before.subtreeClosed {
		t.Errorf("closed moved: child %d→%d, detail %d→%d, subtree %d→%d; started must never be folded into closed",
			before.childClosed, after.childClosed,
			before.detailClosed, after.detailClosed,
			before.subtreeClosed, after.subtreeClosed)
	}
}

// TestSubtreeRollupIsTransitiveAgainstLiveDB executes the recursive subtree CTE
// over a database that really contains a grandchild.
//
// This is the direct-vs-transitive distinction the whole bead rests on, and it
// is the one thing SQL-text matching cannot check: a join-direction or
// stop-recursing bug that left every magic string intact would sail through
// every fixture-driver test, because those drivers return scripted rows without
// ever executing the statement.
//
// The chain is epic → task → subtask, all scratch beads. It fails rather than
// logging when the chain cannot be built.
func TestSubtreeRollupIsTransitiveAgainstLiveDB(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd is not on PATH")
	}
	db := openSeeded(t)
	ctx := context.Background()

	fixture := newScratchFixture(t, db)
	epicID := fixture.create(ctx, "(transitive epic)", "epic", "")
	taskID := fixture.create(ctx, "(transitive child)", "task", epicID)
	grandchildID := fixture.create(ctx, "(transitive grandchild)", "task", taskID)

	// The grandchild is the only started bead in the chain, so every started
	// count below is attributable to it alone.
	fixture.setStatus(ctx, grandchildID, queries.StatusInProgress)

	epic, err := epicRollups(ctx, db, epicID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("epic %s → task %s → grandchild %s: %+v", epicID, taskID, grandchildID, epic)

	// Transitive: the recursive CTE must reach depth 2.
	if epic.subtreeTotal != 2 {
		t.Errorf("InitiativeRollup.total = %d, want 2 (child + grandchild); the subtree CTE did not recurse past the direct child",
			epic.subtreeTotal)
	}
	if epic.subtreeStarted != 1 {
		t.Errorf("InitiativeRollup.started = %d, want 1 (the in_progress grandchild); started is not rolled up transitively",
			epic.subtreeStarted)
	}

	// Direct: ChildStats must stop at depth 1 on both read surfaces.
	if epic.childTotal != 1 || epic.detailTotal != 1 {
		t.Errorf("ChildStats.total = %d (list) / %d (detail), want 1; the direct-children rollup swallowed the grandchild",
			epic.childTotal, epic.detailTotal)
	}
	if epic.childStarted != 0 || epic.detailStarted != 0 {
		t.Errorf("ChildStats.started = %d (list) / %d (detail), want 0; the in_progress grandchild is not a direct child of the epic",
			epic.childStarted, epic.detailStarted)
	}

	// The grandchild is counted exactly once, in its own parent's direct rollup.
	task, err := queries.GetIssue(ctx, db, taskID)
	if err != nil {
		t.Fatalf("GetIssue %s: %v", taskID, err)
	}
	if task.ChildStats == nil {
		t.Fatalf("%s has no child stats; the grandchild is not attached to it", taskID)
	}
	if task.ChildStats.Total != 1 {
		t.Errorf("%s ChildStats.total = %d, want the one grandchild", taskID, task.ChildStats.Total)
	}
	if task.ChildStats.Started != 1 {
		t.Errorf("%s ChildStats.started = %d, want the in_progress grandchild", taskID, task.ChildStats.Started)
	}
}

func TestHealthChangesWhenTransitiveDescendantClosesInWindow(t *testing.T) {
	db := openSeeded(t)
	if _, err := exec.LookPath("bd"); err != nil {
		t.Fatalf("bd is required for the live health fixture: %v", err)
	}
	ctx := context.Background()

	fixture := newScratchFixture(t, db)
	epicID := fixture.create(ctx, "(health epic)", "epic", "")
	childID := fixture.create(ctx, "(health child)", "task", epicID)
	grandchildID := fixture.create(ctx, "(health grandchild)", "task", childID)
	// bd stores a date-only due value. Give the fixture one extra UTC day so a
	// driver/session timezone conversion cannot shave the exact 14-day boundary.
	fixture.setDue(ctx, epicID, time.Now().UTC().AddDate(0, 0, queries.HealthWindowDays+1).Format("2006-01-02"))

	before, err := queries.GetIssue(ctx, db, epicID)
	if err != nil {
		t.Fatalf("GetIssue before close: %v", err)
	}
	if before.Health == nil {
		t.Fatal("epic detail has nil health before close")
	}
	if before.Health.State != "off-track" || !strings.Contains(before.Health.Explanation, "closed 0 in 14d, 2 remain") {
		t.Fatalf("health before close = %#v, want zero-rate off-track over both descendants", before.Health)
	}

	fixture.close(ctx, grandchildID)

	after, err := queries.GetIssue(ctx, db, epicID)
	if err != nil {
		t.Fatalf("GetIssue after close: %v", err)
	}
	if after.Health == nil {
		t.Fatal("epic detail has nil health after close")
	}
	if after.Health.State != "on-track" || !strings.Contains(after.Health.Explanation, "closed 1 in 14d, 1 remain") {
		t.Fatalf("health after close = %#v, want recent transitive close to make it on-track", after.Health)
	}
	if after.ChildStats == nil || after.ChildStats.Total != 1 {
		t.Fatalf("direct ChildStats = %#v, want one direct child while health counts two descendants", after.ChildStats)
	}
}

// TestHealthWindowExcludesDescendantClosedBeforeCutoff is the only guard on the
// trailing half of "trailing close rate".
//
// The cutoff itself lives in SQL — the closed_in_window SUM in initiativesFor —
// and every other test closes its fixture NOW, which is inside any window, so
// for them "closed in the window" and "closed at all" are the same set. A cutoff
// that matched every closed descendant passes all of them untouched.
//
// Shape: one epic with three descendants. Two are closed through bd; one of
// those two is then backdated past the window. The closed rollup must still
// count both while health counts only the recent one.
func TestHealthWindowExcludesDescendantClosedBeforeCutoff(t *testing.T) {
	db := openSeeded(t)
	if _, err := exec.LookPath("bd"); err != nil {
		t.Fatalf("bd is required for the live health-window fixture: %v", err)
	}
	ctx := context.Background()

	fixture := newScratchFixture(t, db)
	epicID := fixture.create(ctx, "(window epic)", "epic", "")
	staleID := fixture.create(ctx, "(window stale child)", "task", epicID)
	recentID := fixture.create(ctx, "(window recent child)", "task", epicID)
	fixture.create(ctx, "(window open child)", "task", epicID)

	// Runway of window-4 days is the discriminator. One remaining descendant and
	// one close inside the window give ratio (W-4)/W ≈ 0.71 — at risk. Counting
	// the stale close as well doubles the trailing rate to ≈ 1.43 — on track. bd
	// stores a date-only due value, so a timezone shift of a day either way
	// leaves both verdicts where they are.
	fixture.setDue(ctx, epicID,
		time.Now().UTC().AddDate(0, 0, queries.HealthWindowDays-4).Format("2006-01-02"))
	fixture.close(ctx, staleID)
	fixture.close(ctx, recentID)

	epicRow := func(stage string) queries.Initiative {
		t.Helper()
		initiatives, err := queries.InitiativesFor(ctx, db, true)
		if err != nil {
			t.Fatalf("InitiativesFor %s the backdate: %v", stage, err)
		}
		for _, initiative := range initiatives {
			if initiative.ID == epicID {
				return initiative
			}
		}
		t.Fatalf("scratch epic %s is not in the initiatives response %s the backdate", epicID, stage)
		return queries.Initiative{}
	}

	// Both closes are recent here, so this is the state the SQL cutoff must
	// change — it proves the backdate below is what moved the answer.
	before := epicRow("before")
	t.Logf("epic %s before backdate: subtree %+v health %#v", epicID, before.Subtree, before.Health)
	if before.Subtree.Total != 3 || before.Subtree.Closed != 2 {
		t.Fatalf("subtree = %+v, want 3 descendants with 2 closed", before.Subtree)
	}
	wantBefore := fmt.Sprintf("closed 2 in %dd, 1 remain", queries.HealthWindowDays)
	if before.Health.State != "on-track" || !strings.Contains(before.Health.Explanation, wantBefore) {
		t.Fatalf("health before backdate = %#v, want on-track counting both recent closes (%q)",
			before.Health, wantBefore)
	}

	// Eleven days older than the window, so no rounding of the cutoff timestamp
	// can pull it back inside.
	fixture.backdateClose(ctx, staleID,
		time.Now().UTC().AddDate(0, 0, -(queries.HealthWindowDays+11)))

	after := epicRow("after")
	t.Logf("epic %s after backdate: subtree %+v health %#v", epicID, after.Subtree, after.Health)
	// The stale descendant is still closed: the cutoff narrows the trailing rate,
	// never the closed count the progress bar reads.
	if after.Subtree.Total != 3 || after.Subtree.Closed != 2 {
		t.Errorf("subtree = %+v, want 3 descendants with 2 closed; the window cutoff leaked into the closed count",
			after.Subtree)
	}
	wantAfter := fmt.Sprintf("closed 1 in %dd, 1 remain", queries.HealthWindowDays)
	if !strings.Contains(after.Health.Explanation, wantAfter) {
		t.Errorf("health explanation = %q, want %q — the descendant closed %d days ago is still inside the trailing window",
			after.Health.Explanation, wantAfter, queries.HealthWindowDays+11)
	}
	if after.Health.State != "at-risk" {
		t.Errorf("health state = %q (%q), want at-risk once only the recent close counts",
			after.Health.State, after.Health.Explanation)
	}
}
