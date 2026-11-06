package queries_test

import (
	"context"
	"database/sql"
	"os/exec"
	"testing"

	"github.com/kurofune/beads-loom/internal/queries"
)

// descendantStatuses reads the fixture epic's transitive descendants back out of
// the database the rollup itself reads.
//
// It exists so this test never asserts against remembered content: what the
// partition is checked against is what the `issues` table actually holds for
// these ids at this moment, including whatever bd really did with each
// --status flag.
func descendantStatuses(ctx context.Context, db *sql.DB, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		var status string
		if err := db.QueryRowContext(ctx, "SELECT status FROM issues WHERE id = ?", id).Scan(&status); err != nil {
			return nil, err
		}
		out[id] = status
	}
	return out, nil
}

func initiativeByID(t *testing.T, ctx context.Context, db *sql.DB, id string) (queries.Initiative, []queries.Initiative) {
	t.Helper()
	initiatives, err := queries.InitiativesFor(ctx, db, true)
	if err != nil {
		t.Fatalf("InitiativesFor: %v", err)
	}
	for _, initiative := range initiatives {
		if initiative.ID == id {
			return initiative, initiatives
		}
	}
	t.Fatalf("scratch epic %s is not in the initiatives response", id)
	return queries.Initiative{}, nil
}

// TestInitiativeRollupPartitionsEveryDescendantStatus is loom-8qt's live guard.
//
// It builds one epic holding a child of every writable status — open,
// in_progress, closed, blocked, deferred, pinned — and asserts the three things
// the fix is for:
//
//	(a) closed + started + ready + depBlocked + deferScheduled == total for
//	    EVERY epic in the response, not just this fixture, so the partition is
//	    checked as an invariant rather than spot-checked;
//	(b) total excludes the pinned child specifically — it equals the count of
//	    non-pinned descendants; and
//	(c) the blocked child is counted in depBlocked specifically, proven by the
//	    before/after transition rather than merely by the absence of a mismatch.
//
// Every bead it touches is one it created. It fails — never logs and passes —
// when the fixture cannot be built or a status bd was asked for did not stick.
func TestInitiativeRollupPartitionsEveryDescendantStatus(t *testing.T) {
	// openSeeded applies AGENTS.md's live-database environment gate: an absent
	// or unreachable Dolt server skips with the `bd dolt start -C <workdir>`
	// message. Past it, everything is a fixture and must fail loudly.
	db := openSeeded(t)
	ctx := context.Background()
	if _, err := exec.LookPath("bd"); err != nil {
		t.Fatalf("bd is required to build the partition fixture: %v", err)
	}

	fixture := newScratchFixture(t, db)
	epicID := fixture.create(ctx, "(partition epic)", "epic", "")
	children := map[string]string{
		queries.StatusOpen:       fixture.create(ctx, "(partition open)", "task", epicID),
		queries.StatusInProgress: fixture.create(ctx, "(partition in_progress)", "task", epicID),
		queries.StatusClosed:     fixture.create(ctx, "(partition closed)", "task", epicID),
		queries.StatusBlocked:    fixture.create(ctx, "(partition blocked)", "task", epicID),
		queries.StatusDeferred:   fixture.create(ctx, "(partition deferred)", "task", epicID),
		queries.StatusPinned:     fixture.create(ctx, "(partition pinned)", "task", epicID),
	}
	ids := make([]string, 0, len(children))
	for _, id := range children {
		ids = append(ids, id)
	}

	// Before: six freshly created children, all open, all ready. This is the
	// baseline that makes (b) and (c) causal — the numbers below move because
	// of the statuses, not because of how the fixture happens to be shaped.
	before, _ := initiativeByID(t, ctx, db, epicID)
	if before.Subtree.Total != len(children) {
		t.Fatalf("scratch epic adopted %d descendants, want %d: %+v",
			before.Subtree.Total, len(children), before.Subtree)
	}
	if before.Subtree.DepBlocked != 0 {
		t.Fatalf("depBlocked = %d before any child is blocked: %+v",
			before.Subtree.DepBlocked, before.Subtree)
	}

	fixture.setStatus(ctx, children[queries.StatusInProgress], queries.StatusInProgress)
	fixture.close(ctx, children[queries.StatusClosed])
	fixture.setStatus(ctx, children[queries.StatusBlocked], queries.StatusBlocked)
	fixture.setStatus(ctx, children[queries.StatusDeferred], queries.StatusDeferred)
	fixture.setStatus(ctx, children[queries.StatusPinned], queries.StatusPinned)

	// Read the statuses back from the same table the rollup reads. A bd flag
	// that silently did nothing is a fixture failure, not a passing test.
	statuses, err := descendantStatuses(ctx, db, ids)
	if err != nil {
		t.Fatalf("reading scratch descendant statuses: %v", err)
	}
	for want, id := range children {
		if statuses[id] != want {
			t.Fatalf("scratch child %s has status %q, want %q — the fixture this test asserts against was never built",
				id, statuses[id], want)
		}
	}

	after, all := initiativeByID(t, ctx, db, epicID)
	t.Logf("epic %s: %+v", epicID, after.Subtree)

	// (a) The partition invariant, over every epic in the response.
	for _, initiative := range all {
		sum := initiative.Subtree.Closed + initiative.Subtree.Started + initiative.Subtree.Ready +
			initiative.Subtree.DepBlocked + initiative.Subtree.DeferScheduled
		if sum != initiative.Subtree.Total {
			t.Errorf("%s: closed+started+ready+depBlocked+deferScheduled = %d, total = %d — a descendant is counted in total and no bucket: %+v",
				initiative.ID, sum, initiative.Subtree.Total, initiative.Subtree)
		}
	}

	// (b) total excludes the pinned child specifically.
	nonPinned := 0
	for _, status := range statuses {
		if status != queries.StatusPinned {
			nonPinned++
		}
	}
	if after.Subtree.Total != nonPinned {
		t.Errorf("total = %d, want %d (every descendant except the pinned one); pinned is a standing reference note, not work: %+v",
			after.Subtree.Total, nonPinned, after.Subtree)
	}
	if after.Subtree.Total != before.Subtree.Total-1 {
		t.Errorf("total went %d→%d when one child was pinned, want a drop of exactly one",
			before.Subtree.Total, after.Subtree.Total)
	}

	// (c) The blocked child lands in depBlocked, not merely out of a mismatch.
	if after.Subtree.DepBlocked != 1 {
		t.Errorf("depBlocked = %d, want the one status=blocked child (%s): %+v",
			after.Subtree.DepBlocked, children[queries.StatusBlocked], after.Subtree)
	}
	// The remaining buckets, so "blocked landed somewhere" cannot pass for
	// "blocked landed in depBlocked".
	if after.Subtree.Closed != 1 || after.Subtree.Started != 1 ||
		after.Subtree.Ready != 1 || after.Subtree.DeferScheduled != 1 {
		t.Errorf("buckets = %+v, want exactly one each of closed/started/ready/deferScheduled beside depBlocked",
			after.Subtree)
	}
}
