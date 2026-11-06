package gitread

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimeout bounds one git read.
const DefaultTimeout = 20 * time.Second

// ExecRunner runs the git executable without deciding which subcommands are
// safe; Client owns that policy so tests with fake runners exercise it too.
type ExecRunner struct {
	Path    string
	Timeout time.Duration
}

// Run executes git -C workdir with the supplied argv.
func (r *ExecRunner) Run(ctx context.Context, workdir string, args ...string) (string, string, error) {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	path := r.Path
	if path == "" {
		path = "git"
	}
	cmd := exec.CommandContext(ctx, path, append([]string{"-C", workdir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("git %s timed out after %s", strings.Join(args, " "), timeout)
	}
	return stdout.String(), stderr.String(), err
}
