package registry

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/loomtest"
)

// seededRegistry builds a registry pointed at this repo's own beads workspace.
// It skips when the dolt server is not running.
func seededRegistry(t *testing.T) (*Registry, string) {
	t.Helper()

	workdir := moduleRoot(t)
	loomtest.LockFixture(t, workdir)
	requireLive(t, workdir)

	r := New(&config.Config{
		Listen:   config.DefaultListen,
		Projects: []config.Project{{Name: "loom", Workdir: workdir}},
	})
	t.Cleanup(func() { r.Close() })

	r.Refresh(context.Background())
	p, err := r.Get("loom")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Reachable {
		// requireLive just proved the server answers, so this is the narrow race
		// where it stopped in between — still an absent environment, and still a
		// skip that has to say how to bring it back.
		t.Skipf("%s", liveAbsent(workdir, "the registry cannot reach the seeded dolt server: "+p.Reason))
	}
	return r, workdir
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the module root")
		}
		dir = parent
	}
}

func TestRefreshResolvesSeededProject(t *testing.T) {
	r, workdir := seededRegistry(t)

	p, err := r.Get("loom")
	if err != nil {
		t.Fatal(err)
	}
	if p.Workdir != workdir {
		t.Errorf("workdir = %q, want %q", p.Workdir, workdir)
	}
	if p.Database == "" {
		t.Error("database name was not resolved from metadata.json")
	}
	if p.Port == 0 {
		t.Error("port was not resolved from the port file")
	}
	if p.Reason != "" {
		t.Errorf("a reachable project should carry no reason, got %q", p.Reason)
	}

	// A plain query must work through Do.
	var n int
	err = r.Do(context.Background(), "loom", func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx, "SELECT COUNT(*) FROM issues").Scan(&n)
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if n <= 0 {
		t.Error("expected a non-empty seeded database")
	}
}

// TestDoRetriesOnceAfterConnectionError covers the behaviour that exists
// because bd restarts dolt servers on new ports: a connection-level failure
// re-resolves the port file and retries exactly once.
func TestDoRetriesOnceAfterConnectionError(t *testing.T) {
	r, _ := seededRegistry(t)

	before, _ := r.Get("loom")

	calls := 0
	err := r.Do(context.Background(), "loom", func(ctx context.Context, db *sql.DB) error {
		calls++
		if calls == 1 {
			return driver.ErrBadConn
		}
		return db.PingContext(ctx)
	})
	if err != nil {
		t.Fatalf("Do should have recovered on the retry, got %v", err)
	}
	if calls != 2 {
		t.Errorf("callback ran %d times, want 2 (one failure + one retry)", calls)
	}

	after, _ := r.Get("loom")
	if !after.LastCheck.After(before.LastCheck) {
		t.Error("the retry should have re-resolved the project (LastCheck did not advance)")
	}
	if !after.Reachable {
		t.Errorf("project should still be reachable after a successful retry: %s", after.Reason)
	}
}

// TestDoGivesUpAfterOneRetry: a persistent connection error must stop after the
// single retry and mark the project unreachable rather than loop.
func TestDoGivesUpAfterOneRetry(t *testing.T) {
	r, _ := seededRegistry(t)

	calls := 0
	err := r.Do(context.Background(), "loom", func(context.Context, *sql.DB) error {
		calls++
		return driver.ErrBadConn
	})
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("error = %v, want ErrUnreachable", err)
	}
	if calls != 2 {
		t.Errorf("callback ran %d times, want exactly 2", calls)
	}

	p, _ := r.Get("loom")
	if p.Reachable {
		t.Error("project should be marked unreachable after a persistent connection failure")
	}
	if p.Reason == "" {
		t.Error("unreachable project must carry a reason")
	}

	// It must recover once the server answers again.
	r.Refresh(context.Background())
	if p, _ := r.Get("loom"); !p.Reachable {
		t.Errorf("project should recover on the next refresh: %s", p.Reason)
	}
}

// TestDoDoesNotRetryOnSQLErrors: a bad query is the caller's fault, not the
// server's, and must surface immediately.
func TestDoDoesNotRetryOnSQLErrors(t *testing.T) {
	r, _ := seededRegistry(t)

	calls := 0
	err := r.Do(context.Background(), "loom", func(ctx context.Context, db *sql.DB) error {
		calls++
		return db.QueryRowContext(ctx, "SELECT * FROM table_that_does_not_exist").Scan(new(int))
	})
	if err == nil {
		t.Fatal("expected an error for a missing table")
	}
	if errors.Is(err, ErrUnreachable) {
		t.Errorf("a SQL error must not be reported as unreachable: %v", err)
	}
	if calls != 1 {
		t.Errorf("callback ran %d times, want 1 (no retry on SQL errors)", calls)
	}
	if p, _ := r.Get("loom"); !p.Reachable {
		t.Error("a bad query must not mark the project unreachable")
	}
}

// TestPoolIsReusedAcrossRefreshes guards against churning connections on every
// 10s health check.
func TestPoolIsReusedAcrossRefreshes(t *testing.T) {
	r, _ := seededRegistry(t)

	r.mu.RLock()
	first := r.byName["loom"].db
	dsn := r.byName["loom"].dsn
	r.mu.RUnlock()

	r.Refresh(context.Background())

	r.mu.RLock()
	second := r.byName["loom"].db
	dsn2 := r.byName["loom"].dsn
	r.mu.RUnlock()

	if first != second {
		t.Error("refresh replaced the pool even though the address did not change")
	}
	if dsn != dsn2 {
		t.Errorf("DSN changed across refreshes: %q then %q", dsn, dsn2)
	}
}

// freePort returns a port with nothing listening on it.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// realFixture resolves this repo's live port and database name through the
// environment gate, so a stopped server — whether it took its port file with it
// or left a stale one behind — skips instead of failing the caller downstream.
func realFixture(t *testing.T) (port int, database string) {
	t.Helper()
	env := requireLive(t, moduleRoot(t))
	return env.Port, env.Database
}

// writePortFile points a synthetic workspace at a port.
func writePortFile(t *testing.T, workdir string, port int) {
	t.Helper()
	path := filepath.Join(workdir, ".beads", PortFileName)
	if err := os.WriteFile(path, []byte(strconv.Itoa(port)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPortFileReResolution is the phase-1 promise that this phase depends on:
// bd assigns dolt servers dynamic ports, so when a server reappears on a
// different port the registry must notice by re-reading the port file.
//
// Rather than stopping the shared fixture server (which other tests and agents
// are using), this drives the real mechanism directly: a synthetic workspace
// whose port file Loom re-reads, moved from a dead port to the live one. The
// pool must be rebuilt against the new address, not reused.
func TestPortFileReResolution(t *testing.T) {
	livePort, database := realFixture(t)
	loomtest.LockFixture(t, moduleRoot(t))

	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := []byte(`{"dolt_database":"` + database + `"}`)
	if err := os.WriteFile(filepath.Join(workdir, ".beads", MetadataFileName), meta, 0o644); err != nil {
		t.Fatal(err)
	}

	// Start pointing at a port nothing is listening on.
	dead := freePort(t)
	writePortFile(t, workdir, dead)

	r := New(&config.Config{
		Listen:   config.DefaultListen,
		Projects: []config.Project{{Name: "moving", Workdir: workdir}},
	})
	defer r.Close()

	r.Refresh(context.Background())
	p, _ := r.Get("moving")
	if p.Reachable {
		t.Fatalf("project should be unreachable on a dead port %d", dead)
	}
	if p.Port != dead {
		t.Errorf("resolved port = %d, want the dead port %d", p.Port, dead)
	}
	if !strings.Contains(p.Reason, "cannot connect") {
		t.Errorf("reason = %q, want a connection failure", p.Reason)
	}

	// The "server restarted on a new port" event: the port file now names a
	// live server. No explicit refresh — the next query must pick this up.
	writePortFile(t, workdir, livePort)

	var count int
	err := r.Do(context.Background(), "moving", func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx, "SELECT COUNT(*) FROM issues").Scan(&count)
	})
	if err != nil {
		t.Fatalf("Do did not recover after the port moved: %v", err)
	}
	if count <= 0 {
		t.Errorf("read %d issues through the re-resolved connection", count)
	}

	p, _ = r.Get("moving")
	if !p.Reachable {
		t.Errorf("project should be reachable after re-resolution: %s", p.Reason)
	}
	if p.Port != livePort {
		t.Errorf("resolved port = %d, want the new live port %d", p.Port, livePort)
	}

	// The pool must have been rebuilt against the new address rather than the
	// stale one being reused.
	r.mu.RLock()
	dsn := r.byName["moving"].dsn
	r.mu.RUnlock()
	if !strings.Contains(dsn, strconv.Itoa(livePort)) {
		t.Errorf("pool DSN = %q, want it to use the new port %d", dsn, livePort)
	}
	if strings.Contains(dsn, strconv.Itoa(dead)) {
		t.Errorf("pool DSN = %q still points at the dead port", dsn)
	}
}

// TestPortFileMovingAwayMarksUnreachable is the reverse transition: a live
// project whose server vanishes must retry once, then degrade with a reason.
func TestPortFileMovingAwayMarksUnreachable(t *testing.T) {
	livePort, database := realFixture(t)
	loomtest.LockFixture(t, moduleRoot(t))

	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := []byte(`{"dolt_database":"` + database + `"}`)
	if err := os.WriteFile(filepath.Join(workdir, ".beads", MetadataFileName), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	writePortFile(t, workdir, livePort)

	r := New(&config.Config{
		Listen:   config.DefaultListen,
		Projects: []config.Project{{Name: "vanishing", Workdir: workdir}},
	})
	defer r.Close()

	r.Refresh(context.Background())
	if p, _ := r.Get("vanishing"); !p.Reachable {
		// realFixture already proved the server answers, so reaching here means
		// it went away mid-test: an absent environment, named with its cure.
		t.Skipf("%s", liveAbsent(moduleRoot(t), "the fixture dolt server stopped answering mid-test: "+p.Reason))
	}

	// The server "goes away": the port file now names a dead port.
	writePortFile(t, workdir, freePort(t))

	// Force the pool to fail the way a killed server would.
	calls := 0
	err := r.Do(context.Background(), "vanishing", func(ctx context.Context, db *sql.DB) error {
		calls++
		return driver.ErrBadConn
	})
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("error = %v, want ErrUnreachable", err)
	}
	// Only one attempt, and that is correct: re-resolution re-read the port
	// file, found nothing listening, and marked the project unreachable. A
	// second call against a server we just confirmed is gone would be pure
	// latency. The retry-and-succeed path is covered by
	// TestDoRetriesOnceAfterConnectionError, where re-resolution finds a live
	// server.
	if calls != 1 {
		t.Errorf("callback ran %d times, want 1 (no retry once the server is confirmed gone)", calls)
	}
	p, _ := r.Get("vanishing")
	if p.Reachable {
		t.Error("project should be marked unreachable once the port is dead")
	}
	if p.Reason == "" {
		t.Error("unreachable project must carry a reason")
	}
}
