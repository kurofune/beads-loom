package queries_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/kurofune/beads-loom/internal/queries"
)

// TestChildStatsExcludesPinnedDirectChildren is loom-1ya's live guard.
//
// loom-8qt dropped pinned descendants from the InitiativeRollup subtree, on the
// standing ruling that a pinned bead is a standing reference note rather than
// work. The DIRECT-children ChildStats rollups did not move with it, so an epic
// with a pinned child reported one denominator on the backlog row / board card /
// issue-detail ChildProgress and a different one on the Initiatives view. This
// test is the assertion that the two agree.
//
// The fixture is deliberately one level deep — an epic and two direct children,
// no grandchildren — so the direct-children set and the transitive subtree are
// the same set of rows by construction. Any difference between ChildStats.total
// and InitiativeRollup.total is therefore the pinned drift and nothing else.
//
// Every bead it touches is one it created. It fails — never logs or skips —
// when the fixture cannot be built or when a status bd was asked for did not
// stick, per AGENTS.md: an absent environment skips (openSeeded's gate), a
// missing fixture inside a reachable database fails.
func TestChildStatsExcludesPinnedDirectChildren(t *testing.T) {
	db := openSeeded(t)
	ctx := context.Background()
	if _, err := exec.LookPath("bd"); err != nil {
		t.Fatalf("bd is required to build the pinned child-stats fixture: %v", err)
	}

	fixture := newScratchFixture(t, db)
	epicID := fixture.create(ctx, "(pinned child-stats epic)", "epic", "")
	openChild := fixture.create(ctx, "(pinned child-stats open child)", "task", epicID)
	pinnedChild := fixture.create(ctx, "(pinned child-stats pinned child)", "task", epicID)

	// Before: two freshly created open children and no deeper descendants, so
	// all three surfaces must already agree. This baseline is what makes the
	// assertion below causal — the numbers move because one child is pinned,
	// not because the fixture happens to be shaped that way.
	before, err := epicRollups(ctx, db, epicID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("epic %s (open %s, to-pin %s): before %+v", epicID, openChild, pinnedChild, before)
	if before.childTotal != 2 || before.detailTotal != 2 || before.subtreeTotal != 2 {
		t.Fatalf("scratch epic did not adopt exactly two direct children and nothing deeper: %+v", before)
	}

	fixture.setStatus(ctx, pinnedChild, queries.StatusPinned)

	// Read both statuses back out of the same table the three queries read, so
	// this asserts against the database rather than against what bd was asked
	// for. A --status flag that silently did nothing is a fixture failure.
	statuses, err := descendantStatuses(ctx, db, []string{openChild, pinnedChild})
	if err != nil {
		t.Fatalf("reading scratch child statuses: %v", err)
	}
	if statuses[pinnedChild] != queries.StatusPinned {
		t.Fatalf("scratch child %s has status %q, want %q — the pinned child this test asserts against was never built",
			pinnedChild, statuses[pinnedChild], queries.StatusPinned)
	}
	if statuses[openChild] != queries.StatusOpen {
		t.Fatalf("scratch child %s has status %q, want %q — the surviving child moved too, so a total of one proves nothing",
			openChild, statuses[openChild], queries.StatusOpen)
	}

	after, err := epicRollups(ctx, db, epicID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("epic %s: after %+v", epicID, after)

	// The Initiatives side already excludes pinned (loom-8qt). Anchor on it
	// explicitly so a regression there cannot make the agreement below pass by
	// both denominators drifting together.
	if after.subtreeTotal != 1 {
		t.Fatalf("InitiativeRollup total = %d, want the one non-pinned descendant: %+v", after.subtreeTotal, after)
	}

	if after.childTotal != after.subtreeTotal {
		t.Errorf("ListIssues ChildStats.total = %d but InitiativeRollup total = %d — the backlog row and board card show a different denominator than the Initiatives view for epic %s",
			after.childTotal, after.subtreeTotal, epicID)
	}
	if after.detailTotal != after.subtreeTotal {
		t.Errorf("GetIssue ChildStats.total = %d but InitiativeRollup total = %d — the issue-detail ChildProgress disagrees with the Initiatives view for epic %s",
			after.detailTotal, after.subtreeTotal, epicID)
	}
	if after.childTotal != after.detailTotal {
		t.Errorf("ListIssues total = %d, GetIssue total = %d — the two child-stats subselects drifted from each other",
			after.childTotal, after.detailTotal)
	}
	// The drop is exactly the pinned child: one fewer than the baseline, not a
	// denominator that collapsed for some other reason.
	if after.childTotal != before.childTotal-1 || after.detailTotal != before.detailTotal-1 {
		t.Errorf("child totals went list %d→%d and detail %d→%d when one of two children was pinned, want a drop of exactly one each",
			before.childTotal, after.childTotal, before.detailTotal, after.detailTotal)
	}
	// closed and started keep counting what they counted: pinning a child moves
	// it out of the denominator, it does not reclassify the other one.
	if after.childClosed != before.childClosed || after.detailClosed != before.detailClosed {
		t.Errorf("closed moved: list %d→%d, detail %d→%d; excluding pinned must not touch the closed arm",
			before.childClosed, after.childClosed, before.detailClosed, after.detailClosed)
	}
	if after.childStarted != before.childStarted || after.detailStarted != before.detailStarted {
		t.Errorf("started moved: list %d→%d, detail %d→%d; excluding pinned must not touch the started arm",
			before.childStarted, after.childStarted, before.detailStarted, after.detailStarted)
	}
}
