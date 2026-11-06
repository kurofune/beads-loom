// Package gitread provides bounded, read-only access to a project's git history.
package gitread

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrForbiddenSubcommand is returned before git is spawned for a command that
// is not part of Loom's read-only surface.
var ErrForbiddenSubcommand = errors.New("forbidden git subcommand")

// ErrInvalidRef rejects empty or flag-shaped refs before they can alter git
// log's behavior (for example, --output would turn a read into a file write).
var ErrInvalidRef = errors.New("invalid git ref")

// CommandError is a git invocation that exited non-zero. Stderr stays
// unmodified so a partial sweep can show git's diagnostic verbatim.
type CommandError struct {
	Args   []string
	Stderr string
	err    error
}

func (e *CommandError) Error() string {
	if e.Stderr != "" {
		return "git failed: " + e.Stderr
	}
	return "git failed: " + e.err.Error()
}

func (e *CommandError) Unwrap() error { return e.err }

// Runner executes git in a project workdir.
type Runner interface {
	Run(ctx context.Context, workdir string, args ...string) (stdout, stderr string, err error)
}

// Commit is one non-merge commit from git log.
type Commit struct {
	SHA        string
	AuthoredAt time.Time
	Author     string
	Subject    string
}

// DefaultBranch returns the remote default branch when configured, otherwise
// the currently checked-out branch. The method is included in sweep evidence.
func (c *Client) DefaultBranch(ctx context.Context, workdir string) (branch, method string, err error) {
	stdout, _, err := c.run(ctx, workdir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		return strings.TrimPrefix(strings.TrimSpace(stdout), "origin/"), "origin/HEAD", nil
	}
	stdout, _, err = c.run(ctx, workdir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(stdout), "HEAD", nil
}

// Log returns at most limit non-merge commits from branch.
func (c *Client) Log(ctx context.Context, workdir, branch string, limit int) ([]Commit, error) {
	if strings.TrimSpace(branch) == "" || strings.HasPrefix(branch, "-") {
		return nil, fmt.Errorf("%w: %q", ErrInvalidRef, branch)
	}
	args := []string{
		"log", branch, "--no-merges", fmt.Sprintf("--max-count=%d", limit),
		"--pretty=format:%H%x1f%aI%x1f%an%x1f%s%x1e",
	}
	stdout, _, err := c.run(ctx, workdir, args...)
	if err != nil {
		return nil, err
	}
	commits := []Commit{}
	for _, record := range strings.Split(stdout, "\x1e") {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		fields := strings.SplitN(record, "\x1f", 4)
		if len(fields) != 4 {
			return nil, fmt.Errorf("parse git log record: got %d fields", len(fields))
		}
		authoredAt, err := time.Parse(time.RFC3339, fields[1])
		if err != nil {
			return nil, fmt.Errorf("parse git authored date %q: %w", fields[1], err)
		}
		commits = append(commits, Commit{
			SHA: fields[0], AuthoredAt: authoredAt.UTC(), Author: fields[2], Subject: fields[3],
		})
	}
	return commits, nil
}

// Client exposes only the git reads used by reconciliation sweeps.
type Client struct {
	runner Runner
}

// New returns a Client over runner.
func New(runner Runner) *Client { return &Client{runner: runner} }

func (c *Client) run(ctx context.Context, workdir string, args ...string) (string, string, error) {
	if len(args) == 0 || (args[0] != "log" && args[0] != "rev-parse" && args[0] != "symbolic-ref") {
		return "", "", ErrForbiddenSubcommand
	}
	stdout, stderr, err := c.runner.Run(ctx, workdir, args...)
	if err != nil {
		return stdout, stderr, &CommandError{Args: append([]string(nil), args...), Stderr: stderr, err: err}
	}
	return stdout, stderr, nil
}
