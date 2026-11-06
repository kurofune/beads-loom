package registry

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/config"
)

// recordingStore is an instrumented embedded opener: it hands out connectors
// that log every statement and count open connections and connectors.
type recordingStore struct {
	mu         sync.Mutex
	failFirst  int
	failWith   error
	opens      int
	attempts   int
	openConns  int
	openConnrs int
	statements []string
}

func (s *recordingStore) open(ctx context.Context, t embeddedTarget) (driver.Connector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.attempts <= s.failFirst {
		return nil, s.failWith
	}
	s.opens++
	s.openConnrs++
	return &recordingConnector{store: s}, nil
}

func (s *recordingStore) snapshot() (attempts, opens, conns, connectors int, statements []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts, s.opens, s.openConns, s.openConnrs, append([]string(nil), s.statements...)
}

type recordingConnector struct {
	store  *recordingStore
	closed bool
}

func (c *recordingConnector) Connect(context.Context) (driver.Conn, error) {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	c.store.openConns++
	return &recordingConn{store: c.store}, nil
}

func (c *recordingConnector) Driver() driver.Driver { return nil }

func (c *recordingConnector) Close() error {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	if !c.closed {
		c.closed = true
		c.store.openConnrs--
	}
	return nil
}

type recordingConn struct{ store *recordingStore }

func (c *recordingConn) log(query string) {
	c.store.mu.Lock()
	c.store.statements = append(c.store.statements, query)
	c.store.mu.Unlock()
}

func (c *recordingConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare not supported")
}
func (c *recordingConn) Begin() (driver.Tx, error) { return nil, errors.New("begin not supported") }
func (c *recordingConn) Close() error {
	c.store.mu.Lock()
	c.store.openConns--
	c.store.mu.Unlock()
	return nil
}
func (c *recordingConn) Ping(context.Context) error { return nil }
func (c *recordingConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.log(query)
	return &oneRow{}, nil
}
func (c *recordingConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.log(query)
	return driver.RowsAffected(0), nil
}

type oneRow struct{ done bool }

func (r *oneRow) Columns() []string { return []string{"v"} }
func (r *oneRow) Close() error      { return nil }
func (r *oneRow) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = int64(1)
	return nil
}

// embeddedWorkdir lays out the files bd 1.3.2's `bd init` leaves for an
// embedded workspace: metadata.json declaring embedded mode, and the database
// directory. It writes no port file.
func embeddedWorkdir(t *testing.T, database string) string {
	t.Helper()
	dir := t.TempDir()
	beads := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(filepath.Join(beads, EmbeddedDirName, database, ".dolt", "noms"), 0o755); err != nil {
		t.Fatal(err)
	}
	md := fmt.Sprintf(`{"database":"dolt","backend":"dolt","dolt_mode":"embedded","dolt_database":%q}`, database)
	if err := os.WriteFile(filepath.Join(beads, MetadataFileName), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newRecordingRegistry(t *testing.T, workdir string, store *recordingStore) *Registry {
	t.Helper()
	reg := New(&config.Config{Projects: []config.Project{{Name: "fx", Workdir: workdir}}})
	reg.embeddedOpen = store.open
	reg.dialFunc = func(network, addr string, timeout time.Duration) (net.Conn, error) {
		t.Errorf("an embedded project dialed %s", addr)
		return nil, errors.New("dial forbidden in this test")
	}
	t.Cleanup(func() { _ = reg.Close() })
	return reg
}

func lockedErr() error {
	return errors.New("Error 1105: the database is locked by another dolt process")
}

func TestEmbeddedOpenRetriesWhileLocked(t *testing.T) {
	target := embeddedTarget{Dir: t.TempDir(), Database: "fx"}

	t.Run("succeeds once the lock frees", func(t *testing.T) {
		store := &recordingStore{failFirst: 2, failWith: lockedErr()}
		_, closeDB, err := openEmbedded(context.Background(), store.open, target)
		if err != nil {
			t.Fatalf("open after 2 locked attempts: %v", err)
		}
		_ = closeDB()
		if attempts, _, _, _, _ := store.snapshot(); attempts != 3 {
			t.Fatalf("attempts = %d, want 3", attempts)
		}
	})

	t.Run("gives up after five attempts with the error unchanged", func(t *testing.T) {
		want := lockedErr()
		store := &recordingStore{failFirst: 100, failWith: want}
		start := time.Now()
		_, _, err := openEmbedded(context.Background(), store.open, target)
		elapsed := time.Since(start)
		if err != want {
			t.Fatalf("err = %v (%T), want the opener's error unchanged", err, err)
		}
		if attempts, _, _, _, _ := store.snapshot(); attempts != 5 {
			t.Fatalf("attempts = %d, want 5", attempts)
		}
		if elapsed >= 2*time.Second {
			t.Fatalf("retries took %s, want under 2s", elapsed)
		}
	})

	t.Run("does not retry other errors", func(t *testing.T) {
		want := errors.New("no database found")
		store := &recordingStore{failFirst: 100, failWith: want}
		_, _, err := openEmbedded(context.Background(), store.open, target)
		if err != want {
			t.Fatalf("err = %v, want %v", err, want)
		}
		if attempts, _, _, _, _ := store.snapshot(); attempts != 1 {
			t.Fatalf("attempts = %d, want 1", attempts)
		}
	})
}

// ddlOrInit matches any statement that would change the store's schema or
// initialise a database.
var ddlOrInit = regexp.MustCompile(`(?i)\b(create|alter|drop)\b|dolt_init|\binit\b`)

// Each Do opens its own read scope and closes the database and connector
// before returning, whether fn succeeds or fails, and Loom sends no DDL.
func TestEmbeddedDoClosesItsScope(t *testing.T) {
	store := &recordingStore{}
	reg := newRecordingRegistry(t, embeddedWorkdir(t, "fx"), store)
	ctx := context.Background()

	assertClosed := func(step string) {
		t.Helper()
		_, _, conns, connectors, _ := store.snapshot()
		if conns != 0 || connectors != 0 {
			t.Fatalf("%s: %d connections and %d connectors still open", step, conns, connectors)
		}
	}

	reg.Refresh(ctx)
	assertClosed("refresh")

	for i := 0; i < 3; i++ {
		err := reg.Do(ctx, "fx", func(ctx context.Context, db *sql.DB) error {
			var v int
			if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM issues").Scan(&v); err != nil {
				return err
			}
			return db.QueryRowContext(ctx, "SELECT dolt_hashof_db()").Scan(&v)
		})
		if err != nil {
			t.Fatalf("Do %d: %v", i, err)
		}
		assertClosed("Do " + strconv.Itoa(i))
	}

	boom := errors.New("query failed")
	if err := reg.Do(ctx, "fx", func(context.Context, *sql.DB) error { return boom }); err != boom {
		t.Fatalf("Do returned %v, want fn's error", err)
	}
	assertClosed("failing Do")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = reg.Do(ctx, "fx", func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1")
				return err
			})
		}()
	}
	wg.Wait()
	assertClosed("concurrent Dos")

	_, opens, _, _, statements := store.snapshot()
	if opens < 13 {
		t.Fatalf("opens = %d, want one per refresh and per Do (13)", opens)
	}
	for _, s := range statements {
		if ddlOrInit.MatchString(s) {
			t.Fatalf("Loom sent %q to the embedded store", s)
		}
	}
	if len(statements) == 0 {
		t.Fatal("statement log is empty; the instrumented opener saw nothing")
	}
}

// A port file left from an earlier server-mode life names a dead port. The
// embedded metadata wins and the port is never dialed.
func TestStalePortFileBesideEmbeddedMetadata(t *testing.T) {
	workdir := embeddedWorkdir(t, "fx")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if err := os.WriteFile(filepath.Join(workdir, ".beads", PortFileName), []byte(strconv.Itoa(port)), 0o644); err != nil {
		t.Fatal(err)
	}

	store := &recordingStore{}
	reg := newRecordingRegistry(t, workdir, store)

	res, reason := reg.resolve(config.Project{Name: "fx", Workdir: workdir})
	if reason != "" || res.Embedded == nil || res.Port != 0 {
		t.Fatalf("resolve = %+v, %q; want the embedded store and no port", res, reason)
	}
	if res.Embedded.Database != "fx" || res.Embedded.Dir != filepath.Join(workdir, ".beads", EmbeddedDirName) {
		t.Fatalf("embedded target = %+v", *res.Embedded)
	}

	reg.Refresh(context.Background())
	p, _ := reg.Get("fx")
	if !p.Reachable || p.Reason != "" || !p.Embedded {
		t.Fatalf("project = %+v; want reachable embedded with no reason", p)
	}
	if err := reg.Do(context.Background(), "fx", func(ctx context.Context, db *sql.DB) error {
		return db.PingContext(ctx)
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
}

func TestEmbeddedWorkspaceWithoutDatabaseDirIsUnreachable(t *testing.T) {
	workdir := embeddedWorkdir(t, "fx")
	if err := os.RemoveAll(filepath.Join(workdir, ".beads", EmbeddedDirName)); err != nil {
		t.Fatal(err)
	}
	store := &recordingStore{}
	reg := newRecordingRegistry(t, workdir, store)
	reg.Refresh(context.Background())
	p, _ := reg.Get("fx")
	if p.Reachable || p.Reason == "" {
		t.Fatalf("project = %+v; want unreachable with a reason", p)
	}
	if attempts, _, _, _, _ := store.snapshot(); attempts != 0 {
		t.Fatalf("opened a missing store %d times", attempts)
	}
}

// A store that fails to open for a reason other than bd's lock marks the
// project unreachable; one still locked after every retry returns ErrStoreBusy
// and leaves it reachable.
func TestEmbeddedDoOpenFailures(t *testing.T) {
	store := &recordingStore{}
	reg := newRecordingRegistry(t, embeddedWorkdir(t, "fx"), store)
	reg.Refresh(context.Background())

	store.mu.Lock()
	store.failFirst, store.failWith = store.attempts+100, lockedErr()
	store.mu.Unlock()
	err := reg.Do(context.Background(), "fx", func(context.Context, *sql.DB) error { return nil })
	if !errors.Is(err, ErrStoreBusy) || !isEmbeddedLocked(err) {
		t.Fatalf("err = %v, want ErrStoreBusy wrapping dolt's lock error", err)
	}
	if errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v; a locked store is busy, not unreachable", err)
	}
	if p, _ := reg.Get("fx"); !p.Reachable {
		t.Fatalf("a locked store marked the project unreachable: %q", p.Reason)
	}

	store.mu.Lock()
	store.failWith = errors.New("corrupt manifest")
	store.mu.Unlock()
	err = reg.Do(context.Background(), "fx", func(context.Context, *sql.DB) error { return nil })
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
	if p, _ := reg.Get("fx"); p.Reachable {
		t.Fatal("a store that cannot open left the project reachable")
	}
}

// A panic in fn still closes the database and connector before the gate
// opens, so the shared store lock is not leaked past a recovered request and
// the next read opens normally.
func TestEmbeddedDoClosesItsScopeOnPanic(t *testing.T) {
	store := &recordingStore{}
	reg := newRecordingRegistry(t, embeddedWorkdir(t, "fx"), store)
	ctx := context.Background()
	reg.Refresh(ctx)

	func() {
		defer func() {
			if r := recover(); r != "handler bug" {
				t.Fatalf("recovered %v, want fn's panic", r)
			}
		}()
		_ = reg.Do(ctx, "fx", func(ctx context.Context, db *sql.DB) error {
			if _, err := db.ExecContext(ctx, "SELECT 1"); err != nil {
				return err
			}
			panic("handler bug")
		})
	}()
	if _, _, conns, connectors, _ := store.snapshot(); conns != 0 || connectors != 0 {
		t.Fatalf("after a panic in fn: %d connections and %d connectors still open", conns, connectors)
	}

	_, opensBefore, _, _, _ := store.snapshot()
	doCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := reg.Do(doCtx, "fx", func(ctx context.Context, db *sql.DB) error {
		_, err := db.ExecContext(ctx, "SELECT 1")
		return err
	}); err != nil {
		t.Fatalf("Do after a recovered panic: %v", err)
	}
	if _, opens, conns, connectors, _ := store.snapshot(); opens != opensBefore+1 || conns != 0 || connectors != 0 {
		t.Fatalf("Do after a recovered panic: opens %d -> %d, %d connections and %d connectors open", opensBefore, opens, conns, connectors)
	}
}

// listTree returns every path under root, so a test can see a file appear.
func listTree(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	if err := filepath.WalkDir(root, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return paths
}

// Where flock does not exist the production opener refuses the store: the
// driver is never reached, nothing is written, and the project reports why.
func TestEmbeddedOpenRefusedWithoutFlock(t *testing.T) {
	workdir := embeddedWorkdir(t, "fx")
	reg := New(&config.Config{Projects: []config.Project{{Name: "fx", Workdir: workdir}}})
	t.Cleanup(func() { _ = reg.Close() })
	reg.embeddedOpen = func(ctx context.Context, tg embeddedTarget) (driver.Connector, error) {
		return openDoltEmbeddedLocked(ctx, tg, refuseShareLock)
	}
	before := listTree(t, workdir)

	reg.Refresh(context.Background())
	p, _ := reg.Get("fx")
	if p.Reachable || !strings.Contains(p.Reason, errNoShareLock.Error()) {
		t.Fatalf("project = %+v; want unreachable naming the missing flock", p)
	}
	if after := listTree(t, workdir); !slices.Equal(before, after) {
		t.Fatalf("the refused open changed the tree:\nbefore %v\nafter  %v", before, after)
	}
}
