package queries_test

import (
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"testing"

	"github.com/kurofune/beads-loom/internal/queries"
)

// This file pins the two visible consequences of loom-8qt's ruling that a
// status=pinned bead is a standing reference note rather than work, carried to
// the direct-children ChildStats subselects by loom-1ya. Both edges were left
// defensible-but-unasserted, so a future denominator edit could flip either one
// back in silence. Neither test changes production code; each one is the guard
// that makes the existing behavior deliberate.
//
// Both follow AGENTS.md's live-database rule: openSeeded applies the shared
// environment gate (an absent or unreachable Dolt server skips, naming
// `bd dolt start -C <workdir>`), and everything past that gate is a fixture and
// fails loudly. Every bead either test touches is one it created, with teardown
// registered before the first create. Nothing here reads a clock.

// listChildStats returns the epic's ChildStats as ListIssues reports it,
// preserving nil rather than treating it as an error — nil is exactly what the
// pinned-only test asserts. It fails if the epic is missing from the backlog
// page altogether, which would be a fixture failure rather than a nil rollup.
func listChildStats(ctx context.Context, db *sql.DB, id string) (*queries.ChildStats, error) {
	list, err := queries.ListIssues(ctx, db, queries.Filter{
		IncludeClosed: true,
		Limit:         queries.MaxLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("ListIssues: %w", err)
	}
	for _, issue := range list.Issues {
		if issue.ID == id {
			return issue.ChildStats, nil
		}
	}
	return nil, fmt.Errorf("%s is not in the list response", id)
}

// TestPinnedOnlyOpenDescendantMakesTheEpicCloseEligible pins the first edge.
//
// CloseEligible is computed at internal/queries/initiatives.go:236-238 as
// `status != closed && Subtree.Total > 0 && Subtree.Closed == Subtree.Total`.
// Once loom-8qt dropped pinned descendants from the `c` join, an epic whose only
// non-closed descendant is pinned has Total == Closed, so the flag turns true
// and the board's close-nudge fires for it. That is the defensible reading of
// the ruling — a reference note must not hold an epic open — but nothing
// asserted it, so restoring pinned to the denominator would silently un-nudge
// every such epic.
//
// The fixture is causal rather than a spot check: the same two children are read
// twice, once with one closed and one open (CloseEligible false) and once with
// that second child pinned (CloseEligible true). The only thing that changed
// between the two reads is the pin, so the flip is attributable to it and to
// nothing about how the fixture happens to be shaped. The closed child is what
// keeps the Total > 0 guard honest — an epic that lost every descendant to the
// pinned exclusion would satisfy Closed == Total vacuously, and this test would
// then be proving nothing.
func TestPinnedOnlyOpenDescendantMakesTheEpicCloseEligible(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()
	if _, err := exec.LookPath("bd"); err != nil {
		t.Fatalf("bd is required to build the close-eligible pinned fixture: %v", err)
	}

	fixture := newScratchFixture(t, db)
	epicID := fixture.create(ctx, "(close-eligible epic)", "epic", "")
	closedChild := fixture.create(ctx, "(close-eligible closed child)", "task", epicID)
	pinnedChild := fixture.create(ctx, "(close-eligible pinned child)", "task", epicID)

	fixture.close(ctx, closedChild)

	before, _ := initiativeByID(t, ctx, db, epicID)
	t.Logf("epic %s (closed %s, to-pin %s): before %+v closeEligible=%v",
		epicID, closedChild, pinnedChild, before.Subtree, before.CloseEligible)
	if before.Subtree.Total != 2 || before.Subtree.Closed != 1 {
		t.Fatalf("scratch epic = %+v, want two descendants with one closed — the fixture this test asserts against was never built",
			before.Subtree)
	}
	if before.CloseEligible {
		t.Fatalf("epic %s is already close-eligible with an open descendant (%+v); the flip below would prove nothing",
			epicID, before.Subtree)
	}

	fixture.setStatus(ctx, pinnedChild, queries.StatusPinned)

	// Read both statuses back out of the same table the rollup reads, so this
	// asserts against the database rather than against what bd was asked for. A
	// --status flag that silently did nothing is a fixture failure.
	statuses, err := descendantStatuses(ctx, db, []string{closedChild, pinnedChild})
	if err != nil {
		t.Fatalf("reading scratch child statuses: %v", err)
	}
	if statuses[pinnedChild] != queries.StatusPinned {
		t.Fatalf("scratch child %s has status %q, want %q — the pinned descendant this test asserts against was never built",
			pinnedChild, statuses[pinnedChild], queries.StatusPinned)
	}
	if statuses[closedChild] != queries.StatusClosed {
		t.Fatalf("scratch child %s has status %q, want %q — without a closed descendant the Total > 0 guard is untested",
			closedChild, statuses[closedChild], queries.StatusClosed)
	}

	after, _ := initiativeByID(t, ctx, db, epicID)
	t.Logf("epic %s: after %+v closeEligible=%v", epicID, after.Subtree, after.CloseEligible)

	// The denominator is what moved: the pinned descendant left the subtree, so
	// the one closed descendant is now the whole of it. Reported rather than
	// fatal, so a regression here does not abort before the CloseEligible
	// assertion below — the flag is the behavior this test exists for, and it
	// must be the thing that goes red.
	if after.Subtree.Total != 1 {
		t.Errorf("subtree total = %d, want the one non-pinned descendant (%+v); the pinned exclusion at the `c` join is gone",
			after.Subtree.Total, after.Subtree)
	}
	if after.Subtree.Closed != 1 {
		t.Errorf("subtree closed = %d, want the one closed descendant (%+v)", after.Subtree.Closed, after.Subtree)
	}
	if !after.CloseEligible {
		t.Errorf("closeEligible = false for epic %s with subtree %+v (status %q) — an epic whose only non-closed descendant is pinned must be close-eligible, because a standing reference note is not work holding the epic open (internal/queries/initiatives.go:236-238)",
			epicID, after.Subtree, after.Status)
	}
	// The guard the flag rests on, asserted separately so "close-eligible" cannot
	// pass by way of an emptied subtree.
	if after.Subtree.Total == 0 {
		t.Errorf("subtree total = 0; closeEligible would then be false by its own Total > 0 guard, so this test must never reach that state")
	}
}

// TestPinnedOnlyChildLeavesChildStatsNilWhileTheEpicRollsUpZero pins the second
// edge (was loom-l6u).
//
// loom-1ya dropped pinned direct children from both child-stats subselects
// (internal/queries/issues.go's listSelect and its detail twin). For an epic
// whose ONLY direct child is pinned, those subselects now return no row at all,
// so the scan guard (childTotal.Valid && childTotal.Int64 > 0) leaves ChildStats
// nil on the backlog row, the board card and the issue-detail ChildProgress —
// while InitiativesFor still reports that epic with Subtree.Total = 0.
//
// nil on one surface and 0 on the other is a divergence, and it is the intended
// one: nil renders exactly the way a childless epic renders, which is what an
// epic holding nothing but a reference note is. This test pins it as deliberate,
// and pins it on BOTH read paths, because the two subselects are separate pieces
// of SQL that drift independently.
func TestPinnedOnlyChildLeavesChildStatsNilWhileTheEpicRollsUpZero(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()
	if _, err := exec.LookPath("bd"); err != nil {
		t.Fatalf("bd is required to build the pinned-only child fixture: %v", err)
	}

	fixture := newScratchFixture(t, db)
	epicID := fixture.create(ctx, "(pinned-only epic)", "epic", "")
	onlyChild := fixture.create(ctx, "(pinned-only child)", "task", epicID)

	// Before: one freshly created open child, so all three surfaces report a
	// denominator of one. This baseline is what makes the nil below causal — the
	// rollup vanishes because the child is pinned, not because the epic never
	// adopted it.
	before, err := epicRollups(ctx, db, epicID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("epic %s (to-pin %s): before %+v", epicID, onlyChild, before)
	if before.childTotal != 1 || before.detailTotal != 1 || before.subtreeTotal != 1 {
		t.Fatalf("scratch epic did not adopt exactly one direct child and nothing deeper: %+v", before)
	}

	fixture.setStatus(ctx, onlyChild, queries.StatusPinned)

	statuses, err := descendantStatuses(ctx, db, []string{onlyChild})
	if err != nil {
		t.Fatalf("reading the scratch child's status: %v", err)
	}
	if statuses[onlyChild] != queries.StatusPinned {
		t.Fatalf("scratch child %s has status %q, want %q — the pinned-only epic this test asserts against was never built",
			onlyChild, statuses[onlyChild], queries.StatusPinned)
	}

	// ListIssues: the backlog row and the board card.
	listStats, err := listChildStats(ctx, db, epicID)
	if err != nil {
		t.Fatal(err)
	}
	if listStats != nil {
		t.Errorf("ListIssues ChildStats = %+v for epic %s, want nil — its only direct child is pinned, so the child-stats subselect returns no row and the backlog row must render as a childless epic",
			listStats, epicID)
	}

	// GetIssue: the issue-detail ChildProgress. A separate subselect, asserted
	// separately so the two read paths cannot drift apart.
	detail, err := queries.GetIssue(ctx, db, epicID)
	if err != nil {
		t.Fatalf("GetIssue %s: %v", epicID, err)
	}
	if detail.ChildStats != nil {
		t.Errorf("GetIssue ChildStats = %+v for epic %s, want nil — the issue-detail ChildProgress must match the backlog row's childless rendering",
			detail.ChildStats, epicID)
	}

	// The Initiatives side of the divergence: the epic is still reported, with a
	// subtree of zero rather than being dropped from the response.
	initiative, _ := initiativeByID(t, ctx, db, epicID)
	t.Logf("epic %s: after listStats=%v detailStats=%v subtree=%+v",
		epicID, listStats, detail.ChildStats, initiative.Subtree)
	if initiative.Subtree.Total != 0 {
		t.Errorf("InitiativeRollup total = %d for epic %s, want 0 — the pinned-only descendant set is empty: %+v",
			initiative.Subtree.Total, epicID, initiative.Subtree)
	}
	// Total = 0 is what keeps the close-nudge off a pinned-only epic, which is
	// the other half of the ruling: CloseEligible's Total > 0 guard
	// (internal/queries/initiatives.go:236-238) refuses to nudge an epic that has
	// closed nothing.
	if initiative.CloseEligible {
		t.Errorf("closeEligible = true for epic %s with an empty subtree %+v — the Total > 0 guard must keep the close-nudge off an epic that has closed nothing",
			epicID, initiative.Subtree)
	}
}
