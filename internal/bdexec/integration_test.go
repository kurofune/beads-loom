package bdexec_test

import (
	"context"
	"database/sql"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/loomtest"
	"github.com/kurofune/beads-loom/internal/loomtest/livedolt"
	"github.com/kurofune/beads-loom/internal/queries"
)

// scratchTitle marks the bead this test creates. It is deleted in a t.Cleanup
// that runs even if an assertion fails partway through, so the fixture is left
// exactly as it was found.
const scratchTitle = loomtest.ScratchTitlePrefix + " bdexec write-path integration test"

// openSeeded connects to this repo's seeded loom database through the shared
// environment gate (AGENTS.md -> "Testing"): no dolt server means skip, since
// nothing in the checkout can start one. Rows missing inside the database it
// returns are fixture, and those still fail.
func openSeeded(t *testing.T) *sql.DB {
	t.Helper()

	workdir := loomtest.RepoRoot(t)
	// Serialize with the read-path tests in internal/queries, which iterate
	// this same database from a separate test binary.
	loomtest.LockFixture(t, workdir)

	env := livedolt.Require(t, workdir)
	db, err := sql.Open("mysql", env.DSN())
	if err != nil {
		// The gate already connected to this DSN, so a failure here is a
		// defect rather than an absent environment.
		t.Fatalf("cannot open %s after the environment gate passed: %v", env.Database, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// assertNoTrace verifies that none of the beads this test created survive.
//
// It deliberately does NOT compare absolute row counts: the fixture is a live
// database that agents also write to, so "the count is unchanged" is not a
// property this test can own. "Nothing I created is still here" is.
func assertNoTrace(t *testing.T, db *sql.DB, ids []string) {
	t.Helper()
	for _, id := range ids {
		if _, err := queries.GetIssue(context.Background(), db, id); err == nil {
			t.Errorf("CLEANUP FAILED — scratch bead %s is still in the fixture", id)
		} else if !errors.Is(err, queries.ErrNotFound) {
			t.Errorf("could not confirm %s was deleted: %v", id, err)
		}
	}

	// Nothing this suite created may linger under the scratch prefix either.
	rows, err := db.Query("SELECT id, title FROM issues WHERE title LIKE ?", loomtest.ScratchTitlePrefix+"%")
	if err != nil {
		t.Fatalf("scan for leftover scratch beads: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, title string
		if err := rows.Scan(&id, &title); err != nil {
			t.Fatal(err)
		}
		for _, mine := range ids {
			if id == mine {
				t.Errorf("CLEANUP FAILED — scratch bead %s (%q) survived", id, title)
			}
		}
	}
}

// TestWritePathAgainstLiveDB drives the whole write surface against the real bd
// binary and the real dolt server: create → update → comment → close → reopen →
// delete, verifying every transition with a SQL read-back rather than trusting
// bd's exit code.
func TestWritePathAgainstLiveDB(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd is not on PATH")
	}
	db := openSeeded(t)
	workdir := loomtest.RepoRoot(t)
	ctx := context.Background()

	c := bdexec.New(bdexec.NewExecRunner())

	// --- create ----------------------------------------------------------
	prio := 4
	estimate := 7
	id, err := c.Create(ctx, workdir, bdexec.CreateSpec{
		Title:       scratchTitle,
		Type:        "chore",
		Priority:    &prio,
		Description: "Created by the phase-2 integration test. Deleted before the test ends.",
		Acceptance:  "criterion one\ncriterion two",
		Design:      "internal/bdexec",
		Notes:       "safe to delete",
		Estimate:    &estimate,
		Labels:      []string{"loom-scratch-test"},
	})
	// Registered immediately so a failure anywhere below still cleans up.
	t.Cleanup(func() {
		if id == "" {
			return
		}
		if err := c.Delete(context.Background(), workdir, []string{id}); err != nil {
			t.Errorf("CLEANUP FAILED — scratch bead %s is still in the fixture: %v", id, err)
			return
		}
		assertNoTrace(t, db, []string{id})
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatal("Create returned an empty id")
	}
	t.Logf("scratch bead: %s", id)

	created, err := queries.GetIssue(ctx, db, id)
	if err != nil {
		t.Fatalf("read back after create: %v", err)
	}
	if created.Title != scratchTitle {
		t.Errorf("title = %q, want %q", created.Title, scratchTitle)
	}
	if created.Type != "chore" {
		t.Errorf("type = %q, want chore", created.Type)
	}
	if created.Priority != 4 {
		t.Errorf("priority = %d, want 4", created.Priority)
	}
	if created.Status != queries.StatusOpen {
		t.Errorf("status = %q, want open", created.Status)
	}
	if created.Estimate == nil || *created.Estimate != 7 {
		t.Errorf("estimate = %v, want 7", created.Estimate)
	}
	if !strings.Contains(created.Description, "phase-2 integration test") {
		t.Errorf("description = %q", created.Description)
	}
	if created.AcceptanceCriteria != "criterion one\ncriterion two" {
		t.Errorf("acceptanceCriteria = %q", created.AcceptanceCriteria)
	}
	if created.Design != "internal/bdexec" {
		t.Errorf("design = %q", created.Design)
	}
	if !contains(created.Labels, "loom-scratch-test") {
		t.Errorf("labels = %v, want the tag to have been applied", created.Labels)
	}
	// A fresh standalone open bead has nothing blocking it.
	if !created.Ready {
		t.Errorf("a new unblocked open issue should be ready")
	}

	// --- update ----------------------------------------------------------
	status := queries.StatusInProgress
	assignee := "loom-integration-test"
	newPrio := 2
	if err := c.Update(ctx, workdir, []string{id}, bdexec.UpdateFields{
		Status:    &status,
		Assignee:  &assignee,
		Priority:  &newPrio,
		AddLabels: []string{"loom-scratch-extra"},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	updated, err := queries.GetIssue(ctx, db, id)
	if err != nil {
		t.Fatalf("read back after update: %v", err)
	}
	if updated.Status != queries.StatusInProgress {
		t.Errorf("status = %q, want in_progress", updated.Status)
	}
	if updated.Assignee != assignee {
		t.Errorf("assignee = %q, want %q", updated.Assignee, assignee)
	}
	if updated.Priority != 2 {
		t.Errorf("priority = %d, want 2", updated.Priority)
	}
	if !contains(updated.Labels, "loom-scratch-extra") {
		t.Errorf("labels = %v, want the added label", updated.Labels)
	}
	// in_progress is not an "open" status, so readiness must have flipped.
	if updated.Ready {
		t.Error("an in_progress issue must not be reported ready")
	}

	// --- label removal ---------------------------------------------------
	if err := c.Update(ctx, workdir, []string{id}, bdexec.UpdateFields{
		RemoveLabels: []string{"loom-scratch-extra"},
	}); err != nil {
		t.Fatalf("Update (remove label): %v", err)
	}
	afterRemove, err := queries.GetIssue(ctx, db, id)
	if err != nil {
		t.Fatal(err)
	}
	if contains(afterRemove.Labels, "loom-scratch-extra") {
		t.Errorf("labels = %v, want the removed label gone", afterRemove.Labels)
	}
	if !contains(afterRemove.Labels, "loom-scratch-test") {
		t.Errorf("labels = %v, removing one label dropped another", afterRemove.Labels)
	}

	// --- prose fields ----------------------------------------------------
	// PATCH's contract: a pointer updates the field, a pointer to "" clears
	// it. Both must round-trip through the real bd binary (loom-jx3).
	desc := "Rewritten by the loom-jx3 integration test.\n- with a bullet"
	accept := "criterion three\ncriterion four"
	design := "internal/bdexec/client.go"
	notes := "-dash-leading notes survive the flag parser"
	if err := c.Update(ctx, workdir, []string{id}, bdexec.UpdateFields{
		Description: &desc,
		Acceptance:  &accept,
		Design:      &design,
		Notes:       &notes,
	}); err != nil {
		t.Fatalf("Update (prose): %v", err)
	}
	prose, err := queries.GetIssue(ctx, db, id)
	if err != nil {
		t.Fatalf("read back after prose update: %v", err)
	}
	if prose.Description != desc {
		t.Errorf("description = %q, want %q", prose.Description, desc)
	}
	if prose.AcceptanceCriteria != accept {
		t.Errorf("acceptanceCriteria = %q, want %q", prose.AcceptanceCriteria, accept)
	}
	if prose.Design != design {
		t.Errorf("design = %q, want %q", prose.Design, design)
	}
	if prose.Notes != notes {
		t.Errorf("notes = %q, want %q", prose.Notes, notes)
	}

	empty := ""
	if err := c.Update(ctx, workdir, []string{id}, bdexec.UpdateFields{
		Design: &empty,
		Notes:  &empty,
	}); err != nil {
		t.Fatalf("Update (clear prose): %v", err)
	}
	cleared, err := queries.GetIssue(ctx, db, id)
	if err != nil {
		t.Fatalf("read back after prose clear: %v", err)
	}
	if cleared.Design != "" {
		t.Errorf("design = %q, want it cleared by the empty string", cleared.Design)
	}
	if cleared.Notes != "" {
		t.Errorf("notes = %q, want it cleared by the empty string", cleared.Notes)
	}
	// The fields not named in the clearing patch must be untouched.
	if cleared.Description != desc {
		t.Errorf("description = %q, want it untouched by clearing other fields", cleared.Description)
	}
	if cleared.AcceptanceCriteria != accept {
		t.Errorf("acceptanceCriteria = %q, want it untouched by clearing other fields", cleared.AcceptanceCriteria)
	}

	// Description and acceptance must clear the same way — bd special-cases an
	// empty description on its stdin/file path, so the inline clear is proven
	// live rather than inferred from the design/notes result above.
	if err := c.Update(ctx, workdir, []string{id}, bdexec.UpdateFields{
		Description: &empty,
		Acceptance:  &empty,
	}); err != nil {
		t.Fatalf("Update (clear description/acceptance): %v", err)
	}
	allCleared, err := queries.GetIssue(ctx, db, id)
	if err != nil {
		t.Fatalf("read back after second prose clear: %v", err)
	}
	if allCleared.Description != "" {
		t.Errorf("description = %q, want it cleared by the empty string", allCleared.Description)
	}
	if allCleared.AcceptanceCriteria != "" {
		t.Errorf("acceptanceCriteria = %q, want it cleared by the empty string", allCleared.AcceptanceCriteria)
	}

	// --- comment ---------------------------------------------------------
	const commentText = "Integration test comment.\nSecond line with \"quotes\" and --flags."
	if err := c.Comment(ctx, workdir, id, commentText); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	commented, err := queries.GetIssue(ctx, db, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(commented.Comments) != 1 {
		t.Fatalf("got %d comments, want 1", len(commented.Comments))
	}
	// The multi-line text with quotes and leading dashes must survive the pipe
	// intact — this is why Comment uses --stdin.
	if commented.Comments[0].Text != commentText {
		t.Errorf("comment text = %q, want %q", commented.Comments[0].Text, commentText)
	}

	// --- close -----------------------------------------------------------
	// The update step assigned this bead to loom-integration-test, which is not
	// the acting user, so bd 1.3+ closes it only with --force
	// (TestForceCloseOfAnotherActorsClaim covers the refusal itself).
	const reason = "integration test finished"
	if err := c.Close(ctx, workdir, []string{id}, bdexec.CloseOptions{Reason: reason, Force: true}); err != nil {
		t.Fatalf("Close: %v", err)
	}
	closed, err := queries.GetIssue(ctx, db, id)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != queries.StatusClosed {
		t.Errorf("status = %q, want closed", closed.Status)
	}
	if closed.Closed == nil {
		t.Error("closed_at was not set")
	}
	if !strings.Contains(closed.CloseReason, reason) {
		t.Errorf("closeReason = %q, want it to contain %q", closed.CloseReason, reason)
	}
	if closed.Ready {
		t.Error("a closed issue must not be ready")
	}

	// --- reopen ----------------------------------------------------------
	if err := c.Reopen(ctx, workdir, []string{id}, "reopened by the integration test"); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	reopened, err := queries.GetIssue(ctx, db, id)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != queries.StatusOpen {
		t.Errorf("status = %q, want open", reopened.Status)
	}
	if reopened.Closed != nil {
		t.Errorf("closed_at = %v, want it cleared on reopen", reopened.Closed)
	}
	if !reopened.Ready {
		t.Error("a reopened unblocked issue should be ready again")
	}

	// The history feed must record what happened, since the detail panel is
	// built on it.
	if len(reopened.Events) <= len(created.Events) {
		t.Errorf("events did not grow: %d after the run, %d at create",
			len(reopened.Events), len(created.Events))
	}
	var sawStatusChange bool
	for _, e := range reopened.Events {
		if e.Type == "status_changed" || e.Type == "closed" || e.Type == "reopened" {
			sawStatusChange = true
		}
	}
	if !sawStatusChange {
		t.Errorf("no status-transition event was recorded; got types %v", eventTypes(reopened.Events))
	}

	// The scratch bead is removed by the t.Cleanup registered above.
}

// actorRunner runs bd as a named actor, so a test can hold a claim the acting
// user does not own.
type actorRunner struct {
	inner bdexec.Runner
	actor string
}

func (r actorRunner) Run(ctx context.Context, workdir string, args ...string) (string, string, error) {
	return r.inner.Run(ctx, workdir, append([]string{"--actor", r.actor}, args...)...)
}

func (r actorRunner) RunStdin(ctx context.Context, workdir, stdin string, args ...string) (string, string, error) {
	return r.inner.RunStdin(ctx, workdir, stdin, append([]string{"--actor", r.actor}, args...)...)
}

// TestForceCloseOfAnotherActorsClaim is the manager closing a teammate's
// claimed issue: bd 1.3+ refuses the plain close and names the assignee, and
// the forced close goes through.
func TestForceCloseOfAnotherActorsClaim(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd is not on PATH")
	}
	db := openSeeded(t)
	workdir := loomtest.RepoRoot(t)
	ctx := context.Background()

	const otherActor = "other-actor"
	me := bdexec.New(bdexec.NewExecRunner())
	other := bdexec.New(actorRunner{inner: bdexec.NewExecRunner(), actor: otherActor})

	id, err := other.Create(ctx, workdir, bdexec.CreateSpec{
		Title: scratchTitle + " (claimed by another actor)",
		Type:  "chore",
	})
	t.Cleanup(func() {
		if id == "" {
			return
		}
		if err := me.Delete(context.Background(), workdir, []string{id}); err != nil {
			t.Errorf("CLEANUP FAILED — scratch bead %s is still in the fixture: %v", id, err)
			return
		}
		assertNoTrace(t, db, []string{id})
	})
	if err != nil {
		t.Fatalf("Create as %s: %v", otherActor, err)
	}
	if _, stderr, err := other.Runner().Run(ctx, workdir, "update", id, "--claim"); err != nil {
		t.Fatalf("claim as %s: %v: %s", otherActor, err, stderr)
	}
	claimed, err := queries.GetIssue(ctx, db, id)
	if err != nil {
		t.Fatalf("read back after claim: %v", err)
	}
	if claimed.Assignee != otherActor || claimed.Status != queries.StatusInProgress {
		t.Fatalf("after claim: assignee %q status %q, want %q in_progress", claimed.Assignee, claimed.Status, otherActor)
	}

	err = me.Close(ctx, workdir, []string{id}, bdexec.CloseOptions{Reason: "closed without force"})
	assignee, refused := bdexec.ClaimedByAnother(err)
	if !refused {
		t.Fatalf("plain close of another actor's claim: err = %v, want bd's reclaim-or-force refusal", err)
	}
	if assignee != otherActor {
		t.Errorf("refusal names assignee %q, want %q", assignee, otherActor)
	}
	stillOpen, err := queries.GetIssue(ctx, db, id)
	if err != nil {
		t.Fatal(err)
	}
	if stillOpen.Status != queries.StatusInProgress {
		t.Fatalf("status after the refused close = %q, want in_progress", stillOpen.Status)
	}

	if err := me.Close(ctx, workdir, []string{id}, bdexec.CloseOptions{Reason: "closed anyway", Force: true}); err != nil {
		t.Fatalf("forced Close: %v", err)
	}
	closed, err := queries.GetIssue(ctx, db, id)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != queries.StatusClosed {
		t.Errorf("status after the forced close = %q, want closed", closed.Status)
	}
	if !strings.Contains(closed.CloseReason, "closed anyway") {
		t.Errorf("closeReason = %q, want the forced close's reason", closed.CloseReason)
	}
}

// TestDepWritesAgainstLiveDB exercises dep add/remove between two scratch beads
// so the fixture's own dependency graph is never touched.
func TestDepWritesAgainstLiveDB(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd is not on PATH")
	}
	db := openSeeded(t)
	workdir := loomtest.RepoRoot(t)
	ctx := context.Background()

	c := bdexec.New(bdexec.NewExecRunner())

	var ids []string
	t.Cleanup(func() {
		if len(ids) == 0 {
			return
		}
		if err := c.Delete(context.Background(), workdir, ids); err != nil {
			t.Errorf("CLEANUP FAILED — scratch beads %v remain: %v", ids, err)
			return
		}
		assertNoTrace(t, db, ids)
	})

	blocker, err := c.Create(ctx, workdir, bdexec.CreateSpec{Title: scratchTitle + " (blocker)"})
	if blocker != "" {
		ids = append(ids, blocker)
	}
	if err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	blocked, err := c.Create(ctx, workdir, bdexec.CreateSpec{Title: scratchTitle + " (blocked)"})
	if blocked != "" {
		ids = append(ids, blocked)
	}
	if err != nil {
		t.Fatalf("create blocked: %v", err)
	}

	// blocked depends on blocker.
	if err := c.DepAdd(ctx, workdir, blocked, blocker, "blocks"); err != nil {
		t.Fatalf("DepAdd: %v", err)
	}

	got, err := queries.GetIssue(ctx, db, blocked)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Deps.BlockedBy) != 1 || got.Deps.BlockedBy[0].ID != blocker {
		t.Errorf("blockedBy = %+v, want just %s", got.Deps.BlockedBy, blocker)
	}
	if got.DepCounts.BlockedBy != 1 {
		t.Errorf("depCounts.blockedBy = %d, want 1", got.DepCounts.BlockedBy)
	}
	// The whole point of the dependency: it must suppress readiness.
	if got.Ready {
		t.Error("an issue with an open blocker must not be ready")
	}

	fromBlocker, err := queries.GetIssue(ctx, db, blocker)
	if err != nil {
		t.Fatal(err)
	}
	if len(fromBlocker.Deps.Blocks) != 1 || fromBlocker.Deps.Blocks[0].ID != blocked {
		t.Errorf("blocks = %+v, want just %s", fromBlocker.Deps.Blocks, blocked)
	}

	// --- remove ----------------------------------------------------------
	if err := c.DepRemove(ctx, workdir, blocked, blocker); err != nil {
		t.Fatalf("DepRemove: %v", err)
	}
	after, err := queries.GetIssue(ctx, db, blocked)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Deps.BlockedBy) != 0 {
		t.Errorf("blockedBy = %+v, want empty after removal", after.Deps.BlockedBy)
	}
	if !after.Ready {
		t.Error("removing the blocker should make the issue ready again")
	}
}

// TestBulkUpdateAgainstLiveDB proves one bd invocation really does update every
// id it is given.
func TestBulkUpdateAgainstLiveDB(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd is not on PATH")
	}
	db := openSeeded(t)
	workdir := loomtest.RepoRoot(t)
	ctx := context.Background()

	c := bdexec.New(bdexec.NewExecRunner())

	var ids []string
	t.Cleanup(func() {
		if len(ids) == 0 {
			return
		}
		if err := c.Delete(context.Background(), workdir, ids); err != nil {
			t.Errorf("CLEANUP FAILED — scratch beads %v remain: %v", ids, err)
			return
		}
		assertNoTrace(t, db, ids)
	})

	for i := 0; i < 3; i++ {
		id, err := c.Create(ctx, workdir, bdexec.CreateSpec{
			Title: scratchTitle + " (bulk " + strconv.Itoa(i) + ")",
		})
		if id != "" {
			ids = append(ids, id)
		}
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	prio := 0
	assignee := "bulk-test"
	if err := c.Update(ctx, workdir, ids, bdexec.UpdateFields{
		Priority:  &prio,
		Assignee:  &assignee,
		AddLabels: []string{"loom-scratch-bulk"},
	}); err != nil {
		t.Fatalf("bulk Update: %v", err)
	}

	for _, id := range ids {
		got, err := queries.GetIssue(ctx, db, id)
		if err != nil {
			t.Fatalf("read back %s: %v", id, err)
		}
		if got.Priority != 0 {
			t.Errorf("%s: priority = %d, want 0", id, got.Priority)
		}
		if got.Assignee != assignee {
			t.Errorf("%s: assignee = %q, want %q", id, got.Assignee, assignee)
		}
		if !contains(got.Labels, "loom-scratch-bulk") {
			t.Errorf("%s: labels = %v, want the bulk label", id, got.Labels)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func eventTypes(events []queries.Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}
