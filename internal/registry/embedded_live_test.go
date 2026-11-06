package registry

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/loomtest/embeddedbd"
)

func TestMain(m *testing.M) { os.Exit(embeddedbd.Main(m)) }

func newEmbeddedRegistry(t *testing.T, w *embeddedbd.Workspace) *Registry {
	t.Helper()
	reg := New(&config.Config{Projects: []config.Project{{Name: "fx", Workdir: w.Workdir}}})
	t.Cleanup(func() { _ = reg.Close() })
	return reg
}

// A workspace fresh from `bd init` — embedded Dolt, no port file — is
// reachable with no reason, and its reads come from the embedded store.
func TestEmbeddedBDInitWorkspaceIsReachable(t *testing.T) {
	w := embeddedbd.New(t)
	id := w.Create(t, "first embedded issue")

	reg := newEmbeddedRegistry(t, w)
	reg.Refresh(context.Background())

	p, err := reg.Get("fx")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Reachable || p.Reason != "" {
		t.Fatalf("Reachable = %v, Reason = %q; want reachable with no reason", p.Reachable, p.Reason)
	}
	if !p.Embedded || p.Database != w.Database || p.Addr() != "" {
		t.Fatalf("project = %+v; want embedded database %q with no server address", p, w.Database)
	}

	var title string
	err = reg.Do(context.Background(), "fx", func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx, "SELECT title FROM issues WHERE id = ?", id).Scan(&title)
	})
	if err != nil {
		t.Fatalf("read issue %s: %v", id, err)
	}
	if title != "first embedded issue" {
		t.Fatalf("title = %q", title)
	}
}

// The store is open read-only below SQL: a write through Loom's handle is
// refused, and the issue count does not move.
func TestEmbeddedStoreRefusesWrites(t *testing.T) {
	w := embeddedbd.Clone(t)
	w.Create(t, "seed")
	reg := newEmbeddedRegistry(t, w)

	count := func() int {
		var n int
		if err := reg.Do(context.Background(), "fx", func(ctx context.Context, db *sql.DB) error {
			return db.QueryRowContext(ctx, "SELECT COUNT(*) FROM issues").Scan(&n)
		}); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	before := count()
	err := reg.Do(context.Background(), "fx", func(ctx context.Context, db *sql.DB) error {
		_, err := db.ExecContext(ctx, "UPDATE issues SET title = 'written by loom'")
		return err
	})
	if err == nil {
		t.Fatal("an UPDATE through the embedded store succeeded; the store is not read-only")
	}
	if after := count(); after != before {
		t.Fatalf("issue count moved from %d to %d", before, after)
	}
	var titles int
	_ = reg.Do(context.Background(), "fx", func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx, "SELECT COUNT(*) FROM issues WHERE title = 'written by loom'").Scan(&titles)
	})
	if titles != 0 {
		t.Fatalf("%d issues carry Loom's write", titles)
	}
	t.Logf("write refused: %v", err)
}

// bd waits for Loom's shared lock rather than failing, and its write shows up
// in the next read.
func TestEmbeddedReadDoesNotBreakBDWrite(t *testing.T) {
	w := embeddedbd.Clone(t)
	reg := newEmbeddedRegistry(t, w)

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- reg.Do(context.Background(), "fx", func(ctx context.Context, db *sql.DB) error {
			close(started)
			time.Sleep(500 * time.Millisecond)
			return db.PingContext(ctx)
		})
	}()
	<-started
	id := w.Create(t, "written while loom reads")
	if err := <-done; err != nil {
		t.Fatalf("read: %v", err)
	}

	var ids []string
	if err := reg.Do(context.Background(), "fx", func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, "SELECT id FROM issues")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return err
			}
			ids = append(ids, s)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(ids, " "), id) {
		t.Fatalf("issue %s missing from %v", id, ids)
	}
}

// firstOpenEnv names the workspace the first-open child process reads.
const firstOpenEnv = "LOOM_TEST_FIRST_OPEN_WORKDIR"

// The first engine open in a process leaves .beads/ byte-identical. The
// driver's side effects run once per process, so the reads happen in a fresh
// child process, and the baseline is taken before Loom touches the workspace.
// The child's environment drops DOLT_METRICS_DISABLED, so only Loom's own
// switch keeps the driver's metrics marker from being created.
func TestEmbeddedFirstOpenLeavesBeadsUntouched(t *testing.T) {
	// No bd write here: bd's own engine creates the marker on a write.
	w := embeddedbd.Clone(t)
	marker := filepath.Join(w.Workdir, ".beads", EmbeddedDirName, w.Database, ".dolt", "temptf", "dolt_embedded_metrics")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the fixture already has %s (%v); the test needs it absent", marker, err)
	}
	before := w.HashBeads(t)

	cmd := exec.Command(os.Args[0], "-test.run=^TestEmbeddedFirstOpenChild$", "-test.count=1", "-test.v")
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "DOLT_METRICS_DISABLED=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, firstOpenEnv+"="+w.Workdir)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: TestEmbeddedFirstOpenChild") {
		t.Fatalf("child process: %v\n%s", err, out)
	}

	after := w.HashBeads(t)
	for path, sum := range before {
		if after[path] != sum {
			t.Errorf("%s changed: %s -> %q", path, sum, after[path])
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("%s appeared", path)
		}
	}
}

// TestEmbeddedFirstOpenChild is the child half of
// TestEmbeddedFirstOpenLeavesBeadsUntouched; it does nothing on its own.
func TestEmbeddedFirstOpenChild(t *testing.T) {
	workdir := os.Getenv(firstOpenEnv)
	if workdir == "" {
		t.Skip("runs only as the child of TestEmbeddedFirstOpenLeavesBeadsUntouched")
	}
	reg := New(&config.Config{Projects: []config.Project{{Name: "fx", Workdir: workdir}}})
	defer reg.Close()
	reg.Refresh(context.Background())
	if p, _ := reg.Get("fx"); !p.Reachable {
		t.Fatalf("project unreachable: %s", p.Reason)
	}
	var n int
	if err := reg.Do(context.Background(), "fx", func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx, "SELECT COUNT(*) FROM issues").Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	// The driver's metrics run in a goroutine after the open; give it time
	// to create its marker before the parent hashes the tree.
	time.Sleep(time.Second)
}
