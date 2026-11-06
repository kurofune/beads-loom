// Package bdexec is Loom's write path. Every mutation goes through the `bd`
// CLI — never raw SQL — so bd stays the single writer that owns ID allocation,
// event emission, dependency cycle checks and Dolt commit policy.
//
// Reads still go straight to SQL (see internal/queries); this package exists
// only for the verbs that change data.
package bdexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimeout bounds a single bd invocation. bd talks to a local dolt
// server, so anything approaching this is a hang rather than slow work.
const DefaultTimeout = 60 * time.Second

// Runner executes bd in a project workdir.
//
// Run carries the signature the work package specifies. RunStdin exists
// alongside it because `bd comment --stdin` is the one verb whose payload must
// be piped: comment text is arbitrary user prose and belongs on a pipe, not in
// argv.
type Runner interface {
	Run(ctx context.Context, workdir string, args ...string) (stdout, stderr string, err error)
	RunStdin(ctx context.Context, workdir, stdin string, args ...string) (stdout, stderr string, err error)
}

// ExecRunner runs the real bd binary.
type ExecRunner struct {
	// Path is the bd executable. Empty means "bd", resolved via PATH.
	Path string
	// Timeout bounds one invocation. Zero means DefaultTimeout.
	Timeout time.Duration
	// Env replaces the process environment when non-nil. Nil passes the
	// parent's environment through, which is what bd needs to find its config
	// and git identity.
	Env []string
}

// NewExecRunner returns a runner using the bd on PATH.
func NewExecRunner() *ExecRunner { return &ExecRunner{} }

// Run executes `bd -C <workdir> <args...>`.
func (r *ExecRunner) Run(ctx context.Context, workdir string, args ...string) (string, string, error) {
	return r.run(ctx, workdir, nil, args)
}

// RunStdin executes `bd -C <workdir> <args...>` with stdin piped in.
func (r *ExecRunner) RunStdin(ctx context.Context, workdir, stdin string, args ...string) (string, string, error) {
	return r.run(ctx, workdir, strings.NewReader(stdin), args)
}

func (r *ExecRunner) run(ctx context.Context, workdir string, stdin *strings.Reader, args []string) (string, string, error) {
	bin := r.Path
	if bin == "" {
		bin = "bd"
	}
	timeout := r.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// -C goes first so the target workdir is unambiguous regardless of what
	// the verb's own positional arguments look like.
	full := append([]string{"-C", workdir}, args...)

	cmd := exec.CommandContext(ctx, bin, full...)
	if r.Env != nil {
		cmd.Env = r.Env
	} else {
		cmd.Env = os.Environ()
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	stdout, stderr := outBuf.String(), errBuf.String()

	if ctxErr := ctx.Err(); errors.Is(ctxErr, context.DeadlineExceeded) {
		return stdout, stderr, fmt.Errorf("bd %s timed out after %s", strings.Join(args, " "), timeout)
	}
	return stdout, stderr, err
}

// exitCode extracts a process exit code, or -1 when the command never ran.
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
