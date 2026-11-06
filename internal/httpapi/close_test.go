package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/kurofune/beads-loom/internal/bdexec"
	"github.com/kurofune/beads-loom/internal/loomtest"
	"github.com/kurofune/beads-loom/internal/queries"
)

// bd13ClaimedStderr is bd 1.3.2's refusal to close an issue another actor
// holds, verbatim from a live run.
const bd13ClaimedStderr = `cannot close loom-1: assignee is "alice", actor is "Nate Gross"; reclaim or use --force to override` + "\n"

// TestCloseOfAnotherActorsClaimIs409NamingTheAssignee: without force, bd's
// refusal becomes a 409 the UI turns into a "Close anyway?" confirm, and Loom
// never adds --force on its own.
func TestCloseOfAnotherActorsClaimIs409NamingTheAssignee(t *testing.T) {
	s, mock, _ := newWriteServer(t)
	mock.Stdout = ""
	mock.Stderr = bd13ClaimedStderr
	mock.Err = &exec.ExitError{}

	rec := do(t, s, http.MethodPost, "/api/projects/loom/issues/loom-1/close", `{"reason":"shipped"}`)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	var body closeConflictResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(body.Error, "alice") {
		t.Errorf("error = %q, want it to name the assignee alice", body.Error)
	}
	if body.Assignee != "alice" {
		t.Errorf("assignee = %q, want alice", body.Assignee)
	}
	if body.Stderr != strings.TrimSpace(bd13ClaimedStderr) {
		t.Errorf("stderr = %q, want bd's message verbatim", body.Stderr)
	}

	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("got %d bd invocations, want exactly 1: %v", len(calls), calls)
	}
	if slices.Contains(calls[0].Args, "--force") {
		t.Errorf("argv %v carries --force although the request did not ask for it", calls[0].Args)
	}
}

// TestCloseRefusalForAnotherReasonStays422: only the claim refusal is a 409.
func TestCloseRefusalForAnotherReasonStays422(t *testing.T) {
	s, mock, _ := newWriteServer(t)
	mock.Stdout = ""
	mock.Stderr = "Error: cannot close epic loom-1: 2 open children; use --force to override\n"
	mock.Err = &exec.ExitError{}

	rec := do(t, s, http.MethodPost, "/api/projects/loom/issues/loom-1/close", `{"reason":"shipped"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body: %s)", rec.Code, rec.Body.String())
	}
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

// TestCloseOfAnotherActorsClaimAgainstLiveDB drives the confirm flow over the
// real HTTP stack, real bd and the seeded database: the plain close is a 409
// naming the assignee read from the issue row, and the forced close is a 200
// carrying status closed.
func TestCloseOfAnotherActorsClaimAgainstLiveDB(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd is not on PATH")
	}
	me := bdexec.New(bdexec.NewExecRunner())
	s := newLiveHTTPServerWithBD(t, me)
	workdir := loomtest.RepoRoot(t)
	ctx := context.Background()

	const otherActor = "other-actor"
	other := bdexec.New(actorRunner{inner: bdexec.NewExecRunner(), actor: otherActor})
	id, err := other.Create(ctx, workdir, bdexec.CreateSpec{
		Title: loomtest.ScratchTitlePrefix + " httpapi close-claimed live test",
		Type:  "chore",
	})
	t.Cleanup(func() {
		if id == "" {
			return
		}
		if err := me.Delete(context.Background(), workdir, []string{id}); err != nil {
			t.Errorf("CLEANUP FAILED — scratch bead %s is still in the fixture: %v", id, err)
		}
	})
	if err != nil {
		t.Fatalf("Create as %s: %v", otherActor, err)
	}
	if _, stderr, err := other.Runner().Run(ctx, workdir, "update", id, "--claim"); err != nil {
		t.Fatalf("claim as %s: %v: %s", otherActor, err, stderr)
	}

	path := "/api/projects/loom/issues/" + id
	var before queries.IssueDetail
	if rec := do(t, s, http.MethodGet, path, ""); rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body.String())
	} else if err := json.Unmarshal(rec.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	if before.Assignee == "" {
		t.Fatalf("scratch bead %s has no assignee after the claim", id)
	}

	rec := do(t, s, http.MethodPost, path+"/close", `{"reason":"closed without force"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("plain close = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	var conflict closeConflictResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &conflict); err != nil {
		t.Fatal(err)
	}
	if conflict.Assignee != before.Assignee || !strings.Contains(conflict.Error, before.Assignee) {
		t.Errorf("409 = %+v, want it to name the row's assignee %q", conflict, before.Assignee)
	}

	rec = do(t, s, http.MethodPost, path+"/close", `{"reason":"closed anyway","force":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("forced close = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var closed queries.IssueDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &closed); err != nil {
		t.Fatal(err)
	}
	if closed.Status != queries.StatusClosed {
		t.Errorf("status after the forced close = %q, want closed", closed.Status)
	}
}
