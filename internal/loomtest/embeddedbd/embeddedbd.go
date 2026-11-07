// Package embeddedbd builds throwaway embedded-Dolt beads workspaces for live
// tests: `bd init` in a temp dir, which is what a stranger's first workspace
// looks like under bd 1.3.2.
//
// The environment is `bd` on PATH and nothing else — no Dolt server. Its
// absence skips, naming what makes the test run. Anything bd fails to do once
// it is present is a finding, so it fails.
package embeddedbd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Prefix is the issue prefix every fixture workspace is initialised with.
const Prefix = "fx"

// commandTimeout bounds one bd invocation. bd waits on the embedded store's
// lock rather than failing, so a wedged peer shows up as this timeout. The
// lock grants no order, so one bd in a test's writer pool can wait behind every
// other writer: under a full parallel `go test ./...` a write takes about 6s,
// and 20 queued writers ran past 2 minutes. 5 minutes covers that queue and
// stays under go test's 10-minute package timeout.
const commandTimeout = 5 * time.Minute

// Workspace is one temp-dir embedded beads workspace.
type Workspace struct {
	Workdir  string
	Database string
}

// RequireBD skips the test when bd is not on PATH.
func RequireBD(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd is not on PATH; install bd 1.3.2 or later (https://github.com/gastownhall/beads) to run the embedded-Dolt live tests")
	}
}

// New runs `bd init` in a fresh temp dir and checks that it produced an
// embedded workspace. It costs bd's ~10s init; Clone is the cheap form.
func New(t testing.TB) *Workspace {
	t.Helper()
	RequireBD(t)
	w := &Workspace{Workdir: t.TempDir()}
	w.initialise(t)
	return w
}

var template struct {
	once sync.Once
	dir  string
	db   string
	err  string
}

// Clone returns a private copy of one `bd init` workspace made once per test
// binary. A copied workspace is a working one: bd writes to it and Loom reads
// it exactly as it would the original. Call Main from TestMain so the
// template is removed when the binary exits.
func Clone(t testing.TB) *Workspace {
	t.Helper()
	RequireBD(t)
	template.once.Do(func() {
		dir, err := os.MkdirTemp("", "loom-embeddedbd-template-")
		if err != nil {
			template.err = err.Error()
			return
		}
		tw := &Workspace{Workdir: dir}
		if msg := tw.tryInitialise(); msg != "" {
			template.err = msg
			return
		}
		template.dir, template.db = dir, tw.Database
	})
	if template.err != "" {
		t.Fatalf("embedded template workspace: %s", template.err)
	}
	w := &Workspace{Workdir: t.TempDir(), Database: template.db}
	if err := os.CopyFS(w.Workdir, os.DirFS(template.dir)); err != nil {
		t.Fatalf("copy template workspace: %v", err)
	}
	return w
}

// Main runs the package's tests and removes the Clone template afterwards.
// Use it as `func TestMain(m *testing.M) { os.Exit(embeddedbd.Main(m)) }`.
func Main(m *testing.M) int {
	code := m.Run()
	if template.dir != "" {
		_ = os.RemoveAll(template.dir)
	}
	return code
}

func (w *Workspace) initialise(t testing.TB) {
	t.Helper()
	if msg := w.tryInitialise(); msg != "" {
		t.Fatal(msg)
	}
}

// tryInitialise runs `bd init` and checks the result, returning a failure
// message or "".
func (w *Workspace) tryInitialise() string {
	if out, err := w.Exec("init", "--prefix", Prefix, "--non-interactive", "--quiet", "--skip-agents", "--skip-hooks"); err != nil {
		return fmt.Sprintf("bd init: %v\n%s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(w.Workdir, ".beads", "metadata.json"))
	if err != nil {
		return fmt.Sprintf("bd init wrote no metadata.json: %v", err)
	}
	var md struct {
		DoltMode     string `json:"dolt_mode"`
		DoltDatabase string `json:"dolt_database"`
	}
	if err := json.Unmarshal(data, &md); err != nil {
		return fmt.Sprintf("parse metadata.json: %v", err)
	}
	if md.DoltMode != "embedded" || md.DoltDatabase == "" {
		return fmt.Sprintf("bd init did not create an embedded workspace: metadata.json = %s", data)
	}
	if _, err := os.Stat(filepath.Join(w.Workdir, ".beads", "dolt-server.port")); err == nil {
		return "bd init left a dolt-server.port in an embedded workspace"
	}
	w.Database = md.DoltDatabase
	return ""
}

// Run runs bd in the workspace and returns its trimmed stdout. A non-zero exit
// fails the test.
func (w *Workspace) Run(t testing.TB, args ...string) string {
	t.Helper()
	out, err := w.Exec(args...)
	if err != nil {
		t.Fatalf("bd %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// Exec runs bd in the workspace and returns its trimmed stdout, or an error
// carrying stderr. It is safe to call from many goroutines at once.
func (w *Workspace) Exec(args ...string) (string, error) {
	bin, err := exec.LookPath("bd")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = w.Workdir
	cmd.Env = append(os.Environ(), "BD_NON_INTERACTIVE=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(stdout.String()), &ExecError{Args: args, Err: err, Stderr: stderr.String()}
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Create runs `bd create` and returns the id bd printed.
func (w *Workspace) Create(t testing.TB, title string, extra ...string) string {
	t.Helper()
	id := w.Run(t, append([]string{"create", title, "--silent"}, extra...)...)
	if id == "" {
		t.Fatalf("bd create %q printed no id", title)
	}
	return id
}

// ManifestPath is the embedded store's manifest, which bd rewrites on every
// write.
func (w *Workspace) ManifestPath() string {
	return filepath.Join(w.Workdir, ".beads", "embeddeddolt", w.Database, ".dolt", "noms", "manifest")
}

// HashBeads returns the SHA-256 of every regular file under .beads/, keyed by
// slash-separated relative path. A file appearing or vanishing changes the
// key set, so comparing two results catches both edits and new files.
func (w *Workspace) HashBeads(t testing.TB) map[string]string {
	t.Helper()
	root := filepath.Join(w.Workdir, ".beads")
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			out[filepath.ToSlash(rel)+"/"] = "dir"
			return nil
		}
		if !d.Type().IsRegular() {
			out[filepath.ToSlash(rel)] = "special:" + d.Type().String()
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("hash .beads: %v", err)
	}
	return out
}

// ExecError is a failed bd invocation.
type ExecError struct {
	Args   []string
	Err    error
	Stderr string
}

func (e *ExecError) Error() string {
	return "bd " + strings.Join(e.Args, " ") + ": " + e.Err.Error() + ": " + strings.TrimSpace(e.Stderr)
}

func (e *ExecError) Unwrap() error { return e.Err }
