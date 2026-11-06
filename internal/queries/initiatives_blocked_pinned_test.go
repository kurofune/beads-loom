package queries_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/kurofune/beads-loom/internal/queries"
)

// TestInitiativesBlockedDescendantsLandInDepBlocked pins the first half of
// loom-8qt: a status=blocked descendant used to be counted in total and in no
// bucket at all, because notReadyExpr short-circuits to NULL for any status
// other than open and neither the closed nor the started arm claims it.
//
// It is now unioned into the existing dep_blocked bucket, exactly the way
// status='deferred' is unioned into defer_scheduled — one OR arm on the one
// recursive-CTE statement. The fixture driver enforces that shape; this test
// enforces the count that shape produces.
func TestInitiativesBlockedDescendantsLandInDepBlocked(t *testing.T) {
	db := openInitiativesFixture(t, "blocked")

	got, err := queries.InitiativesFor(context.Background(), db, true)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("initiatives = %#v, want one epic", got)
	}
	rollup := got[0].Subtree
	if rollup.DepBlocked != 1 {
		t.Errorf("depBlocked = %d, want the one status=blocked descendant", rollup.DepBlocked)
	}
	// Counted exactly once: every other bucket stays empty, so the blocked
	// descendant cannot be double-counted across buckets.
	if rollup.Closed != 0 || rollup.Started != 0 || rollup.Ready != 0 || rollup.DeferScheduled != 0 {
		t.Errorf("blocked descendant leaked into another bucket: %+v", rollup)
	}
	if rollup.Total != 1 {
		t.Errorf("total = %d, want the single descendant", rollup.Total)
	}
	if rollup.Closed+rollup.Started+rollup.Ready+rollup.DepBlocked+rollup.DeferScheduled != rollup.Total {
		t.Errorf("buckets do not partition the subtree: %+v", rollup)
	}
}

// TestInitiativesPinnedDescendantsLeaveTheSubtree pins the second half: pinned
// is a standing reference note, not work (web/src/lib/board.test.ts's ruling,
// which is why the board hides pinned rows). It is dropped from the descendant
// join so it inflates neither total nor any bucket, and it deliberately does
// NOT get a rollup column of its own.
func TestInitiativesPinnedDescendantsLeaveTheSubtree(t *testing.T) {
	db := openInitiativesFixture(t, "pinned-excluded")

	got, err := queries.InitiativesFor(context.Background(), db, true)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("initiatives = %#v, want one epic", got)
	}
	rollup := got[0].Subtree
	if rollup.Closed+rollup.Started+rollup.Ready+rollup.DepBlocked+rollup.DeferScheduled != rollup.Total {
		t.Errorf("buckets do not partition the subtree once pinned is excluded: %+v", rollup)
	}
}

// TestInitiativesBlockedAndPinnedRideTheSingleStatement holds the "no second
// query" half of the criterion: both changes are edits to the one recursive-CTE
// statement, not a per-epic fetch. The recording driver fabricates 30 rows, so
// a per-epic loop would show up as 30 extra QueryContext calls.
func TestInitiativesBlockedAndPinnedRideTheSingleStatement(t *testing.T) {
	initiativesRecorder.reset()
	db, err := sql.Open(initiativesRecordingDriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	got, err := queries.InitiativesFor(context.Background(), db, false)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	if len(got) != 30 {
		t.Fatalf("initiatives = %d rows, want 30 fabricated rows", len(got))
	}

	recorded := initiativesRecorder.snapshot()
	if len(recorded) != 1 {
		t.Fatalf("QueryContext calls = %d, want exactly one", len(recorded))
	}
	statement := recorded[0].SQL
	if strings.Count(statement, "WITH RECURSIVE") != 1 {
		t.Fatalf("blocked/pinned must ride the one statement, found %d statement starts:\n%s",
			strings.Count(statement, "WITH RECURSIVE"), statement)
	}
	if !strings.Contains(statement,
		"END = 'deps' OR c.status = '"+queries.StatusBlocked+"' THEN 1 ELSE 0 END) AS dep_blocked") {
		t.Errorf("the single statement does not union blocked into dep_blocked:\n%s", statement)
	}
	if !strings.Contains(statement,
		"LEFT JOIN issues c ON c.id = dsc.node AND c.status <> '"+queries.StatusPinned+"'") {
		t.Errorf("the single statement does not drop pinned descendants from the join:\n%s", statement)
	}
	// No new SUM column: pinned is excluded, not surfaced.
	if strings.Contains(statement, "AS pinned") {
		t.Errorf("pinned gained a rollup column; it must be excluded instead:\n%s", statement)
	}
	// Readiness still comes only from readiness.go — blocked is routed as a
	// status bucket, never as a new not-ready token.
	if strings.Contains(statement, "THEN '"+queries.StatusBlocked+"'") {
		t.Errorf("blocked must not become a readiness verdict:\n%s", statement)
	}
}
