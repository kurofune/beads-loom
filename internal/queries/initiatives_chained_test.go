package queries_test

import (
	"context"
	"database/sql"
	"os/exec"
	"strings"
	"testing"

	"github.com/kurofune/beads-loom/internal/queries"
)

// TestInitiativesChainedRidesTheSingleStatement holds the shape of the chained
// column: it is one more SUM on the one recursive-CTE statement, read off
// the descendant alias, and it is not routed through readiness. A chained
// descendant is one that sits on either end of a blocks edge, whatever the
// blocker's status, so the column must not read blocked_transitively.
func TestInitiativesChainedRidesTheSingleStatement(t *testing.T) {
	initiativesRecorder.reset()
	db := openRecordingInitiatives(t)
	if _, err := queries.InitiativesFor(context.Background(), db, false); err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	recorded := initiativesRecorder.snapshot()
	if len(recorded) != 1 {
		t.Fatalf("QueryContext calls = %d, want exactly one", len(recorded))
	}
	chainedExpr, err := isolateSumExpr(recorded[0].SQL, "AS chained")
	if err != nil {
		t.Fatal(err)
	}
	for _, readinessToken := range []string{"bt.", "r.", "ready_issues", "'deps'"} {
		if strings.Contains(chainedExpr, readinessToken) {
			t.Errorf("chained is a shape count, not a readiness one, but its SUM references %q: %s",
				readinessToken, chainedExpr)
		}
	}
}

// TestInitiativesChainedScansIntoTheRollup proves the column reaches
// Subtree.Chained rather than being selected and dropped.
func TestInitiativesChainedScansIntoTheRollup(t *testing.T) {
	db := openInitiativesFixture(t, "chained")
	got, err := queries.InitiativesFor(context.Background(), db, true)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("initiatives = %#v, want one epic", got)
	}
	if got[0].Subtree.Chained != 3 {
		t.Errorf("chained = %d, want the scripted 3: %+v", got[0].Subtree.Chained, got[0].Subtree)
	}
	if got[0].Subtree.DepBlocked != 1 {
		t.Errorf("depBlocked = %d, want the scripted 1: %+v", got[0].Subtree.DepBlocked, got[0].Subtree)
	}
}

// TestInitiativeChainedCountsBothEndsWhateverTheBlockerStatus is the live
// proof that Chained is a shape signal and depBlocked a readiness one.
//
// The scratch epic holds, across two levels of the subtree:
//
//	A ── blocked by ──▶ B      both direct children; B is then closed
//	X                         a direct child with no blocks edge
//	└─ G ── blocked by ──▶ Out a grandchild blocked from outside the epic
//
// Once B closes, depBlocked counts only G, while Chained still counts A, B
// and G: the pair whose blocker closed stays in the epic's shape, the
// blocker itself counts, and the grandchild counts because the rollup covers
// the full transitive subtree. X and the outside blocker do not count.
func TestInitiativeChainedCountsBothEndsWhateverTheBlockerStatus(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()
	if _, err := exec.LookPath("bd"); err != nil {
		t.Fatalf("bd is required to build the chained fixture: %v", err)
	}

	fixture := newScratchFixture(t, db)
	epicID := fixture.create(ctx, "(chained epic)", "epic", "")
	a := fixture.create(ctx, "(chained A, blocked)", "task", epicID)
	b := fixture.create(ctx, "(chained B, blocker)", "task", epicID)
	x := fixture.create(ctx, "(chained X, unchained parent)", "task", epicID)
	g := fixture.create(ctx, "(chained G, grandchild)", "task", x)
	out := fixture.create(ctx, "(chained outside blocker)", "task", "")
	fixture.depAdd(ctx, a, b)
	fixture.depAdd(ctx, g, out)

	before, _ := initiativeByID(t, ctx, db, epicID)
	if before.Subtree.Total != 4 {
		t.Fatalf("scratch epic adopted %d descendants, want A, B, X and G: %+v",
			before.Subtree.Total, before.Subtree)
	}
	if before.Subtree.DepBlocked != 2 || before.Subtree.Chained != 3 {
		t.Fatalf("before closing B: depBlocked = %d, chained = %d, want 2 (A, G) and 3 (A, B, G): %+v",
			before.Subtree.DepBlocked, before.Subtree.Chained, before.Subtree)
	}

	fixture.close(ctx, b)
	var status string
	if err := db.QueryRowContext(ctx, "SELECT status FROM issues WHERE id = ?", b).Scan(&status); err != nil {
		t.Fatalf("reading %s status: %v", b, err)
	}
	if status != queries.StatusClosed {
		t.Fatalf("blocker %s has status %q after close, want closed", b, status)
	}

	after, _ := initiativeByID(t, ctx, db, epicID)
	if after.Subtree.DepBlocked != 1 {
		t.Errorf("depBlocked = %d after the blocker closed, want only G: %+v",
			after.Subtree.DepBlocked, after.Subtree)
	}
	if after.Subtree.Chained != 3 {
		t.Errorf("chained = %d after the blocker closed, want A, B and G still: %+v",
			after.Subtree.Chained, after.Subtree)
	}
	if after.Subtree.Total != 4 {
		t.Errorf("total = %d, want 4: %+v", after.Subtree.Total, after.Subtree)
	}
}

func openRecordingInitiatives(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(initiativesRecordingDriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestInitiativeChainedCountsAnEdgeWhoseOtherEndIsPinned guards both edge
// directions when the opposite endpoint is pinned. Pinned leaves the
// descendant set, but it does not erase the edge: the blockers graph still
// draws a pinned neighbour, so the row's in-chain count must agree with it.
//
//	A ── blocked by ──▶ Out (pinned)   A counts: its blocker is pinned
//	Out (pinned) ── blocked by ──▶ B   B counts: the issue it blocks is pinned
//	Pin (pinned, inside the epic) ── blocked by ──▶ Out
//
// Pin stays out of both Total and Chained, so Total = 2 and Chained = 2.
func TestInitiativeChainedCountsAnEdgeWhoseOtherEndIsPinned(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()
	if _, err := exec.LookPath("bd"); err != nil {
		t.Fatalf("bd is required to build the pinned-endpoint fixture: %v", err)
	}

	fixture := newScratchFixture(t, db)
	epicID := fixture.create(ctx, "(chained-pinned epic)", "epic", "")
	a := fixture.create(ctx, "(chained-pinned A, blocked by pinned)", "task", epicID)
	b := fixture.create(ctx, "(chained-pinned B, blocks pinned)", "task", epicID)
	pin := fixture.create(ctx, "(chained-pinned Pin, pinned descendant)", "task", epicID)
	out := fixture.create(ctx, "(chained-pinned outside, pinned)", "task", "")
	fixture.depAdd(ctx, a, out)
	fixture.depAdd(ctx, out, b)
	fixture.depAdd(ctx, pin, out)
	fixture.setStatus(ctx, out, queries.StatusPinned)
	fixture.setStatus(ctx, pin, queries.StatusPinned)

	for _, id := range []string{out, pin} {
		var status string
		if err := db.QueryRowContext(ctx, "SELECT status FROM issues WHERE id = ?", id).Scan(&status); err != nil {
			t.Fatalf("reading %s status: %v", id, err)
		}
		if status != queries.StatusPinned {
			t.Fatalf("%s has status %q, want pinned", id, status)
		}
	}

	got, _ := initiativeByID(t, ctx, db, epicID)
	if got.Subtree.Total != 2 {
		t.Fatalf("total = %d, want A and B only (Pin is pinned): %+v", got.Subtree.Total, got.Subtree)
	}
	if got.Subtree.Chained != 2 {
		t.Errorf("chained = %d, want A (blocked by a pinned issue) and B (blocking a pinned issue): %+v",
			got.Subtree.Chained, got.Subtree)
	}

	graph, err := queries.GraphFor(ctx, db, epicID, true)
	if err != nil {
		t.Fatalf("GraphFor: %v", err)
	}
	drawn := map[string]bool{}
	for _, e := range graph.Edges {
		if e.Type == "blocks" {
			drawn[e.From+"->"+e.To] = true
		}
	}
	for _, edge := range []string{a + "->" + out, out + "->" + b} {
		if !drawn[edge] {
			t.Errorf("blockers graph lacks %s, so Chained would count an edge the drill does not draw: %+v",
				edge, graph.Edges)
		}
	}
}
