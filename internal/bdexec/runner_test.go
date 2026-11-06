package bdexec

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/loomtest"
	"github.com/kurofune/beads-loom/internal/loomtest/livedolt"
)

// requireLiveBD gates the tests that drive the real bd binary against this
// repo and returns the workdir to point it at.
//
// bd itself talks to this project's dolt server, so without one every
// invocation fails with a circuit-breaker error that says nothing about the
// runner under test — and one of these tests would even pass for the wrong
// reason, "proving" bd rejects an unknown issue when in fact bd never reached
// the database. Both are the absent ENVIRONMENT, so both skip
// (AGENTS.md -> "Testing"). What bd then says about a real database is fixture,
// and those assertions still fail.
func requireLiveBD(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd is not on PATH")
	}
	workdir := loomtest.RepoRoot(t)
	livedolt.Require(t, workdir)
	return workdir
}

// TestExecRunnerReadOnlyVerb proves the runner actually drives the real binary
// and prepends -C, using a verb that changes nothing.
func TestExecRunnerReadOnlyVerb(t *testing.T) {
	workdir := requireLiveBD(t)

	r := NewExecRunner()
	stdout, stderr, err := r.Run(context.Background(), workdir, "stats")
	if err != nil {
		t.Fatalf("bd stats failed: %v (stderr: %s)", err, stderr)
	}
	if !strings.Contains(stdout, "Total Issues") {
		t.Errorf("unexpected bd stats output: %q", stdout)
	}
}

// TestExecRunnerMapsFailureToBdError checks the error path against real bd, so
// the 422 the API returns is known to carry bd's own words.
func TestExecRunnerMapsFailureToBdError(t *testing.T) {
	workdir := requireLiveBD(t)

	c := New(NewExecRunner())
	// A well-formed but nonexistent id is a safe way to make bd itself fail
	// (a malformed id would be rejected by validation before bd ever runs).
	err := c.Tag(context.Background(), workdir, "loom-zzzz9", "x")
	if err == nil {
		t.Fatal("expected bd to reject an unknown issue")
	}
	be, ok := AsBdError(err)
	if !ok {
		t.Fatalf("error is not a *BdError: %T %v", err, err)
	}
	if be.Verb != "tag" {
		t.Errorf("Verb = %q, want tag", be.Verb)
	}
	if be.Message() == "" {
		t.Error("BdError carries no message for the UI to show")
	}
	if be.ExitCode == 0 {
		t.Errorf("ExitCode = %d, want non-zero", be.ExitCode)
	}
	t.Logf("bd said: %s", be.Message())
}

func TestExecRunnerStdin(t *testing.T) {
	workdir := requireLiveBD(t)

	// `bd` with no args prints help and ignores stdin; this only asserts that
	// piping does not break the invocation path.
	r := NewExecRunner()
	_, _, err := r.RunStdin(context.Background(), workdir, "ignored", "stats")
	if err != nil {
		t.Fatalf("piping stdin broke a normal invocation: %v", err)
	}
}

func TestExecRunnerTimeout(t *testing.T) {
	// A binary that outlives the timeout must produce a timeout error rather
	// than hang the request.
	r := &ExecRunner{Path: "sleep", Timeout: 100 * time.Millisecond}
	// Path is invoked as `sleep -C <workdir> 5`, which sleep rejects fast on
	// some platforms, so only assert we come back promptly with an error.
	start := time.Now()
	_, _, err := r.Run(context.Background(), "/tmp", "5")
	if err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("runner took %s; the timeout did not fire", elapsed)
	}
}

func TestExecRunnerMissingBinary(t *testing.T) {
	r := &ExecRunner{Path: "definitely-not-a-real-binary-loom"}
	_, _, err := r.Run(context.Background(), "/tmp", "stats")
	if err == nil {
		t.Fatal("expected an error for a missing binary")
	}
	// It must map to a BdError through the client so the API still answers 422
	// rather than panicking.
	be := newBdError([]string{"stats"}, "", "", err)
	if be.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1 when the process never ran", be.ExitCode)
	}
	if be.Message() == "" {
		t.Error("expected a message")
	}
}
