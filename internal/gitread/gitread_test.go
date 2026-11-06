package gitread

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type recordingRunner struct {
	calls   [][]string
	handler func(args []string) (string, string, error)
}

func TestExecRunnerPassesProjectAndGitArguments(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "record-git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := (&ExecRunner{Path: script}).Run(t.Context(), "/project root", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("Run: %v (stderr %q)", err, stderr)
	}
	if stdout != "-C\n/project root\nrev-parse\n--abbrev-ref\nHEAD\n" {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestLogExcludesMergesAndParsesSeparatorDelimitedCommits(t *testing.T) {
	runner := &recordingRunner{handler: func(args []string) (string, string, error) {
		want := []string{"log", "main", "--no-merges", "--max-count=500", "--pretty=format:%H%x1f%aI%x1f%an%x1f%s%x1e"}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("git args = %v, want %v", args, want)
		}
		return "abc123\x1f2026-08-04T11:30:00+09:00\x1fNate Gross\x1ffix: preserve subjects with spaces — and punctuation\x1e" +
			"def456\x1f2026-08-03T00:00:00Z\x1fRobot\x1fchore: loom-d1q.1 receipt\x1e", "", nil
	}}

	got, err := New(runner).Log(t.Context(), "/project", "main", 500)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	wantTime := time.Date(2026, 8, 4, 2, 30, 0, 0, time.UTC)
	if len(got) != 2 || got[0].SHA != "abc123" || !got[0].AuthoredAt.Equal(wantTime) ||
		got[0].Author != "Nate Gross" || got[0].Subject != "fix: preserve subjects with spaces — and punctuation" {
		t.Fatalf("commits = %#v", got)
	}
}

func (r *recordingRunner) Run(_ context.Context, _ string, args ...string) (string, string, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if r.handler != nil {
		return r.handler(args)
	}
	return "", "", nil
}

func TestClientRejectsForbiddenSubcommandBeforeSpawning(t *testing.T) {
	runner := &recordingRunner{}
	client := New(runner)

	for _, subcommand := range []string{"commit", "push"} {
		if _, _, err := client.run(t.Context(), "/project", subcommand); !errors.Is(err, ErrForbiddenSubcommand) {
			t.Errorf("run(%q) error = %v, want ErrForbiddenSubcommand", subcommand, err)
		}
	}
	if len(runner.calls) != 0 {
		t.Fatalf("forbidden commands reached runner: %v", runner.calls)
	}
}

func TestDefaultBranchFallsBackToCurrentHeadWithMethodEvidence(t *testing.T) {
	runner := &recordingRunner{handler: func(args []string) (string, string, error) {
		switch args[0] {
		case "symbolic-ref":
			return "", "fatal: ref refs/remotes/origin/HEAD is not a symbolic ref\n", errors.New("exit status 1")
		case "rev-parse":
			return "feature/sweep\n", "", nil
		default:
			t.Fatalf("unexpected git args: %v", args)
			return "", "", nil
		}
	}}

	branch, method, err := New(runner).DefaultBranch(t.Context(), "/project")
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if branch != "feature/sweep" || method != "HEAD" {
		t.Fatalf("DefaultBranch = (%q, %q), want (%q, %q)", branch, method, "feature/sweep", "HEAD")
	}
}

func TestLogFailureCarriesGitStderrVerbatim(t *testing.T) {
	const stderr = "fatal: not a git repository (or any parent up to mount point)\n"
	runner := &recordingRunner{handler: func(_ []string) (string, string, error) {
		return "", stderr, errors.New("exit status 128")
	}}

	_, err := New(runner).Log(t.Context(), "/not-a-repo", "main", 5)
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("Log error = %T %v, want *CommandError", err, err)
	}
	if commandErr.Stderr != stderr {
		t.Fatalf("stderr = %q, want verbatim %q", commandErr.Stderr, stderr)
	}
}

func TestLogRejectsFlagShapedBranchBeforeSpawning(t *testing.T) {
	runner := &recordingRunner{}
	_, err := New(runner).Log(t.Context(), "/project", "--output=/tmp/sweep", 5)
	if !errors.Is(err, ErrInvalidRef) {
		t.Fatalf("Log error = %v, want ErrInvalidRef", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("flag-shaped branch reached git: %v", runner.calls)
	}
}
