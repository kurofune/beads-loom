package registry

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-sql-driver/mysql"

	"github.com/kurofune/beads-loom/internal/config"
)

// writeWorkspace creates a fake beads workspace and returns its workdir.
func writeWorkspace(t *testing.T, opts ...func(dir string)) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, o := range opts {
		o(dir)
	}
	return dir
}

func withPort(port string) func(string) {
	return func(dir string) {
		if err := os.WriteFile(filepath.Join(dir, ".beads", PortFileName), []byte(port), 0o644); err != nil {
			panic(err)
		}
	}
}

func withMetadata(body string) func(string) {
	return func(dir string) {
		if err := os.WriteFile(filepath.Join(dir, ".beads", MetadataFileName), []byte(body), 0o644); err != nil {
			panic(err)
		}
	}
}

func withDjinn() func(string) {
	return func(dir string) {
		if err := os.MkdirAll(filepath.Join(dir, ".djinn"), 0o755); err != nil {
			panic(err)
		}
	}
}

// dotDjinnDetector stands in for the djinn adapter, which this package cannot
// import: it applies exactly when the workdir has a .djinn directory.
type dotDjinnDetector struct{ calls *atomic.Int64 }

func (dotDjinnDetector) Name() string { return "djinn" }

func (d dotDjinnDetector) Capabilities(workdir string) Capabilities {
	d.calls.Add(1)
	if !dirExists(filepath.Join(workdir, ".djinn")) {
		return Capabilities{}
	}
	return Capabilities{Present: true, Capabilities: []string{"runs", "evidence"}}
}

func TestReadPortFile(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T) string
		wantPort   int
		wantReason string // substring
	}{
		{
			name:     "valid port",
			setup:    func(t *testing.T) string { return writeWorkspace(t, withPort("55933")) },
			wantPort: 55933,
		},
		{
			name:     "trailing whitespace tolerated",
			setup:    func(t *testing.T) string { return writeWorkspace(t, withPort("3310\n")) },
			wantPort: 3310,
		},
		{
			name:       "no port file",
			setup:      func(t *testing.T) string { return writeWorkspace(t) },
			wantReason: "dolt server not running",
		},
		{
			name:       "no beads dir",
			setup:      func(t *testing.T) string { return t.TempDir() },
			wantReason: "not a beads workspace",
		},
		{
			name:       "garbage contents",
			setup:      func(t *testing.T) string { return writeWorkspace(t, withPort("not-a-port")) },
			wantReason: "does not contain a port number",
		},
		{
			name:       "out of range",
			setup:      func(t *testing.T) string { return writeWorkspace(t, withPort("99999")) },
			wantReason: "out-of-range port",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.setup(t)
			port, reason := readPortFile(dir)
			if tc.wantReason != "" {
				if !strings.Contains(reason, tc.wantReason) {
					t.Fatalf("reason = %q, want it to contain %q", reason, tc.wantReason)
				}
				return
			}
			if reason != "" {
				t.Fatalf("unexpected reason %q", reason)
			}
			if port != tc.wantPort {
				t.Errorf("port = %d, want %d", port, tc.wantPort)
			}
		})
	}
}

func TestReadDatabaseName(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(t *testing.T) string
		wantDB       string
		wantUser     string
		wantPassword string
		wantReason   string
	}{
		{
			name: "reads dolt_database",
			setup: func(t *testing.T) string {
				return writeWorkspace(t, withMetadata(`{"dolt_database":"loom","database":"dolt"}`))
			},
			wantDB: "loom",
		},
		{
			name:       "missing file",
			setup:      func(t *testing.T) string { return writeWorkspace(t) },
			wantReason: "cannot determine database name",
		},
		{
			name:       "malformed json",
			setup:      func(t *testing.T) string { return writeWorkspace(t, withMetadata(`{`)) },
			wantReason: "cannot parse",
		},
		{
			name:       "no dolt_database key",
			setup:      func(t *testing.T) string { return writeWorkspace(t, withMetadata(`{"database":"dolt"}`)) },
			wantReason: "no dolt_database key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.setup(t)
			db, reason := readDatabaseName(dir)
			if tc.wantReason != "" {
				if !strings.Contains(reason, tc.wantReason) {
					t.Fatalf("reason = %q, want it to contain %q", reason, tc.wantReason)
				}
				return
			}
			if reason != "" {
				t.Fatalf("unexpected reason %q", reason)
			}
			if db != tc.wantDB {
				t.Errorf("db = %q, want %q", db, tc.wantDB)
			}
		})
	}
}

func TestDSN(t *testing.T) {
	got := DSN("127.0.0.1", 55933, "loom")
	want := "root@tcp(127.0.0.1:55933)/loom?parseTime=true&timeout=2s&readTimeout=60s"
	if got != want {
		t.Errorf("DSN = %q, want %q", got, want)
	}
	// The DSN must be parseable by the driver we hand it to.
	if _, err := mysql.ParseDSN(got); err != nil {
		t.Errorf("driver cannot parse DSN %q: %v", got, err)
	}
}

func TestResolveEmbeddedWithoutPort(t *testing.T) {
	dir := writeWorkspace(t, withMetadata(`{"dolt_database":"pit","dolt_mode":"embedded"}`))
	r := New(&config.Config{})

	res, reason := r.resolve(config.Project{Name: "pit", Workdir: dir})
	want := "embedded database " + filepath.Join(dir, ".beads", EmbeddedDirName, "pit") + " does not exist"
	if res.Embedded == nil || res.Port != 0 || reason != want {
		t.Fatalf("resolve = (%+v, %q), want the embedded store and reason %q", res, reason, want)
	}

	if err := os.MkdirAll(filepath.Join(dir, ".beads", EmbeddedDirName, "pit", ".dolt"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, reason = r.resolve(config.Project{Name: "pit", Workdir: dir})
	if reason != "" || res.Embedded == nil || res.Database != "pit" || res.Port != 0 {
		t.Fatalf("resolve = (%+v, %q), want embedded database pit and no reason", res, reason)
	}
}

func TestResolveMissingPortOtherModes(t *testing.T) {
	for _, metadata := range []string{
		`{"dolt_database":"pit","dolt_mode":"server"}`,
		`{"dolt_database":"pit"}`,
		`{"dolt_database":"pit","dolt_mode":"unknown"}`,
	} {
		t.Run(metadata, func(t *testing.T) {
			dir := writeWorkspace(t, withMetadata(metadata))
			r := New(&config.Config{})
			res, reason := r.resolve(config.Project{Name: "pit", Workdir: dir})
			want := fmt.Sprintf("no %s (dolt server not running? try: bd dolt start -C %s)", filepath.Join(dir, ".beads", PortFileName), dir)
			if res.Port != 0 || reason != want {
				t.Errorf("resolve = (%d, %q), want (0, %q)", res.Port, reason, want)
			}
		})
	}
}

// Metadata declaring embedded mode wins over any port file beside it, valid
// or not: bd in embedded mode never reads one.
func TestResolveEmbeddedIgnoresPortFile(t *testing.T) {
	for _, port := range []string{"55933", "not-a-port"} {
		t.Run(port, func(t *testing.T) {
			dir := writeWorkspace(t, withMetadata(`{"dolt_database":"pit","dolt_mode":"embedded"}`), withPort(port))
			if err := os.MkdirAll(filepath.Join(dir, ".beads", EmbeddedDirName, "pit", ".dolt"), 0o755); err != nil {
				t.Fatal(err)
			}
			r := New(&config.Config{})
			res, reason := r.resolve(config.Project{Name: "pit", Workdir: dir})
			if reason != "" || res.Embedded == nil || res.Port != 0 || res.Host != "" || res.Database != "pit" {
				t.Errorf("resolve = (%+v, %q), want embedded database pit, no port, no reason", res, reason)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	good := writeWorkspace(t, withPort("55933"), withMetadata(`{"dolt_database":"loom"}`))
	noPort := writeWorkspace(t, withMetadata(`{"dolt_database":"loom"}`))
	noMeta := writeWorkspace(t, withPort("3310"))

	cfg := &config.Config{
		Listen: config.DefaultListen,
		Servers: []config.Server{
			{Name: "shared", Host: "10.0.0.5", Port: 3308, Kind: config.KindRemote,
				User: "loomuser", Password: "s3cr3t"},
			{Name: "localish", Kind: config.KindLocal},
		},
		Projects: []config.Project{{Name: "x", Workdir: good}},
	}
	r := New(cfg)

	tests := []struct {
		name         string
		pc           config.Project
		wantHost     string
		wantPort     int
		wantDB       string
		wantUser     string
		wantPassword string
		wantReason   string
	}{
		{
			name:     "local resolves from port file and metadata",
			pc:       config.Project{Name: "loom", Workdir: good},
			wantHost: "127.0.0.1", wantPort: 55933, wantDB: "loom",
		},
		{
			name:     "explicit database overrides metadata",
			pc:       config.Project{Name: "loom", Workdir: good, Database: "other"},
			wantHost: "127.0.0.1", wantPort: 55933, wantDB: "other",
		},
		{
			name:     "remote server uses configured host, port and credentials",
			pc:       config.Project{Name: "loom", Workdir: good, Server: "shared"},
			wantHost: "10.0.0.5", wantPort: 3308, wantDB: "loom",
			wantUser: "loomuser", wantPassword: "s3cr3t",
		},
		{
			name:     "kind local server still resolves from port file",
			pc:       config.Project{Name: "loom", Workdir: good, Server: "localish"},
			wantHost: "127.0.0.1", wantPort: 55933, wantDB: "loom",
		},
		{
			name:       "missing port file",
			pc:         config.Project{Name: "loom", Workdir: noPort},
			wantReason: "dolt server not running",
		},
		{
			name:       "missing metadata",
			pc:         config.Project{Name: "loom", Workdir: noMeta},
			wantReason: "cannot determine database name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, reason := r.resolve(tc.pc)
			if tc.wantReason != "" {
				if !strings.Contains(reason, tc.wantReason) {
					t.Fatalf("reason = %q, want it to contain %q", reason, tc.wantReason)
				}
				return
			}
			if reason != "" {
				t.Fatalf("unexpected reason %q", reason)
			}
			if res.Host != tc.wantHost || res.Port != tc.wantPort || res.Database != tc.wantDB {
				t.Errorf("resolve = (%s, %d, %s), want (%s, %d, %s)",
					res.Host, res.Port, res.Database, tc.wantHost, tc.wantPort, tc.wantDB)
			}
			if tc.wantUser != "" && res.User != tc.wantUser {
				t.Errorf("user = %q, want %q", res.User, tc.wantUser)
			}
			if tc.wantPassword != "" && res.Password != tc.wantPassword {
				t.Errorf("password = %q, want %q", res.Password, tc.wantPassword)
			}
		})
	}
}

// TestRefreshIsolatesFailures is the key degradation guarantee: an unreachable
// project must never break a healthy one.
func TestRefreshIsolatesFailures(t *testing.T) {
	// A listener stands in for a dolt server so the dial succeeds.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())

	up := writeWorkspace(t, withPort(portStr), withMetadata(`{"dolt_database":"loom"}`), withDjinn())
	down := writeWorkspace(t) // no port file at all

	cfg := &config.Config{
		Listen: config.DefaultListen,
		Projects: []config.Project{
			{Name: "down", Workdir: down},
			{Name: "up", Workdir: up},
		},
	}
	calls := &atomic.Int64{}
	r := New(cfg, dotDjinnDetector{calls: calls})
	defer r.Close()

	r.Refresh(context.Background())

	snap := r.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("got %d projects, want 2", len(snap))
	}
	if snap[0].Name != "down" || snap[1].Name != "up" {
		t.Fatalf("snapshot is not in config order: %v", []string{snap[0].Name, snap[1].Name})
	}

	if snap[0].Reachable {
		t.Error("project down should be unreachable")
	}
	if snap[0].Reason == "" {
		t.Error("unreachable project must carry a reason")
	}
	if _, ok := snap[0].Integrations["djinn"]; ok {
		t.Errorf("project down has no .djinn but reports integrations %+v", snap[0].Integrations)
	}
	if calls.Load() != 2 {
		t.Errorf("detector ran %d times, want once per project per refresh", calls.Load())
	}

	// The "up" project dials fine but the fake server speaks no MySQL, so the
	// ping fails. What matters here is that it got that far independently and
	// recorded its own reason rather than inheriting "down"'s failure.
	djinn, ok := snap[1].Integrations["djinn"]
	if !ok {
		t.Fatalf("project up has .djinn; integrations = %+v, want a djinn key", snap[1].Integrations)
	}
	if !djinn.Has("runs") || !djinn.Has("evidence") {
		t.Errorf("djinn capabilities = %v, want runs and evidence", djinn.Capabilities)
	}
	if snap[1].Port != mustAtoi(t, portStr) {
		t.Errorf("port = %d, want %s", snap[1].Port, portStr)
	}
	if snap[1].Database != "loom" {
		t.Errorf("database = %q, want loom", snap[1].Database)
	}
	if snap[1].LastCheck.IsZero() {
		t.Error("LastCheck should be set after a refresh")
	}
}

func TestGetUnknownProject(t *testing.T) {
	r := New(&config.Config{Listen: config.DefaultListen, Projects: []config.Project{{Name: "a", Workdir: t.TempDir()}}})
	defer r.Close()

	if _, err := r.Get("nope"); !errors.Is(err, ErrUnknownProject) {
		t.Errorf("Get(nope) error = %v, want ErrUnknownProject", err)
	}
	if err := r.Do(context.Background(), "nope", nil); !errors.Is(err, ErrUnknownProject) {
		t.Errorf("Do(nope) error = %v, want ErrUnknownProject", err)
	}
}

// TestDoOnUnreachableProject: Do must fail fast with ErrUnreachable and never
// invoke the callback when there is no server to talk to.
func TestDoOnUnreachableProject(t *testing.T) {
	r := New(&config.Config{
		Listen:   config.DefaultListen,
		Projects: []config.Project{{Name: "a", Workdir: t.TempDir()}},
	})
	defer r.Close()

	called := false
	err := r.Do(context.Background(), "a", func(context.Context, *sql.DB) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("Do error = %v, want ErrUnreachable", err)
	}
	if called {
		t.Error("callback must not run when the project is unreachable")
	}
	if !strings.Contains(err.Error(), "not a beads workspace") {
		t.Errorf("error = %q, want it to carry the resolution reason", err)
	}
}

func TestIsConnErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"driver bad conn", driver.ErrBadConn, true},
		{"wrapped bad conn", fmt.Errorf("query: %w", driver.ErrBadConn), true},
		{"mysql invalid conn", mysql.ErrInvalidConn, true},
		{"net op error", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, true},
		{"connection refused text", errors.New("dial tcp 127.0.0.1:1: connect: connection refused"), true},
		{"broken pipe text", errors.New("write: broken pipe"), true},
		// A timeout is classified by isTimeout and checked for liveness, never
		// taken as proof the server is gone.
		{"i/o timeout", errors.New("read tcp: i/o timeout"), false},
		{"mysql server error is not a conn error", &mysql.MySQLError{Number: 1146, Message: "Table 'x' doesn't exist"}, false},
		{"plain sql error", errors.New("syntax error near SELEC"), false},
		{"sql.ErrNoRows-like", errors.New("sql: no rows in result set"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isConnErr(tc.err); got != tc.want {
				t.Errorf("isConnErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestProjectAddr(t *testing.T) {
	if got := (Project{}).Addr(); got != "" {
		t.Errorf("unresolved Addr = %q, want empty", got)
	}
	if got := (Project{Host: "127.0.0.1", Port: 3310}).Addr(); got != "127.0.0.1:3310" {
		t.Errorf("Addr = %q", got)
	}
}

func TestSetDiscoveredReplacesDynamicProjects(t *testing.T) {
	configuredDir := t.TempDir()
	r := New(&config.Config{Projects: []config.Project{{Name: "configured", Workdir: configuredDir}}})
	defer r.Close()

	aDir, zDir := t.TempDir(), t.TempDir()
	added, removed := r.SetDiscovered([]config.Project{
		{Name: "zeta", Workdir: zDir},
		{Name: "configured", Workdir: t.TempDir()},
		{Name: "same-workdir", Workdir: filepath.Join(configuredDir, ".")},
		{Name: "alpha", Workdir: aDir},
	})
	if got := strings.Join(added, ","); got != "alpha,zeta" {
		t.Fatalf("added = %v, want [alpha zeta]", added)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want none", removed)
	}
	if got := strings.Join(r.Names(), ","); got != "configured,alpha,zeta" {
		t.Fatalf("Names = %v, want config first then sorted discovery", r.Names())
	}
	snap := r.Snapshot()
	if snap[0].Source != SourceConfig || snap[1].Source != SourceDiscovered || snap[2].Source != SourceDiscovered {
		t.Fatalf("sources = [%q %q %q]", snap[0].Source, snap[1].Source, snap[2].Source)
	}

	entry := r.byName["alpha"]
	entry.Generation = 7
	entry.Reachable = true
	entry.Host = "127.0.0.1"
	entry.Port = 3308
	connector := &countingConnector{}
	db := sql.OpenDB(connector)
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	entry.db = db
	entry.dsn = "test"

	added, removed = r.SetDiscovered([]config.Project{
		{Name: "alpha", Workdir: aDir},
		{Name: "zeta", Workdir: zDir},
	})
	if len(added) != 0 || len(removed) != 0 {
		t.Fatalf("identical set changed membership: added=%v removed=%v", added, removed)
	}
	if r.byName["alpha"] != entry || r.byName["alpha"].db != db || r.byName["alpha"].Generation != 7 {
		t.Fatal("identical discovered entry did not preserve identity, pool, and generation")
	}

	added, removed = r.SetDiscovered([]config.Project{{Name: "zeta", Workdir: zDir}})
	if len(added) != 0 || strings.Join(removed, ",") != "alpha" {
		t.Fatalf("removal = added %v removed %v", added, removed)
	}
	if connector.closes.Load() != 1 {
		t.Fatalf("removed pool closes = %d, want 1", connector.closes.Load())
	}
}

// A discovered project that keeps its name but moves to a new workdir is a
// replacement, not an update: the stale record (and its pool) must go, and the
// fresh workdir must win. This is the branch main.go handles as Remove-then-Add
// against the monitor and watcher set.
func TestSetDiscoveredRelocatedWorkdirReplaces(t *testing.T) {
	r := New(&config.Config{Projects: []config.Project{{Name: "configured", Workdir: t.TempDir()}}})
	defer r.Close()

	oldDir, newDir := t.TempDir(), t.TempDir()
	if added, _ := r.SetDiscovered([]config.Project{{Name: "roving", Workdir: oldDir}}); strings.Join(added, ",") != "roving" {
		t.Fatalf("setup added = %v", added)
	}

	entry := r.byName["roving"]
	entry.Generation = 3
	connector := &countingConnector{}
	db := sql.OpenDB(connector)
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	entry.db = db
	entry.dsn = "test"

	added, removed := r.SetDiscovered([]config.Project{{Name: "roving", Workdir: newDir}})
	if strings.Join(added, ",") != "roving" || strings.Join(removed, ",") != "roving" {
		t.Fatalf("relocation = added %v removed %v, want roving in both", added, removed)
	}
	got, err := r.Get("roving")
	if err != nil {
		t.Fatal(err)
	}
	if got.Workdir != newDir {
		t.Fatalf("workdir = %q, want relocated %q", got.Workdir, newDir)
	}
	if got.Generation != 0 || r.byName["roving"] == entry {
		t.Fatal("relocated project kept the stale record instead of being replaced")
	}
	if connector.closes.Load() != 1 {
		t.Fatalf("stale pool closes = %d, want 1", connector.closes.Load())
	}
	if entry.active {
		t.Fatal("stale record still marked active; an in-flight refresh could resurrect it")
	}
}

func TestSetDiscoveredConcurrentAccess(t *testing.T) {
	r := New(&config.Config{Projects: []config.Project{{Name: "configured", Workdir: t.TempDir()}}})
	defer r.Close()
	discovered := config.Project{Name: "dynamic", Workdir: t.TempDir()}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if i%2 == 0 {
				r.SetDiscovered([]config.Project{discovered})
			} else {
				r.SetDiscovered(nil)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = r.Do(context.Background(), "dynamic", func(context.Context, *sql.DB) error { return nil })
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = r.Snapshot()
			_ = r.Names()
		}
	}()
	wg.Wait()
}

type countingConnector struct {
	closes atomic.Int32
}

func (c *countingConnector) Connect(context.Context) (driver.Conn, error) {
	return &countingConn{closes: &c.closes}, nil
}

func (c *countingConnector) Driver() driver.Driver { return countingDriver{} }

type countingDriver struct{}

func (countingDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unused") }

type countingConn struct {
	closes *atomic.Int32
}

func (*countingConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c *countingConn) Close() error {
	c.closes.Add(1)
	return nil
}
func (*countingConn) Begin() (driver.Tx, error)  { return nil, errors.New("unused") }
func (*countingConn) Ping(context.Context) error { return nil }

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestMarkWrittenBumpsGeneration: writes must invalidate anything derived from
// project data without waiting for the 10s health tick.
func TestMarkWrittenBumpsGeneration(t *testing.T) {
	r := New(&config.Config{
		Listen:   config.DefaultListen,
		Projects: []config.Project{{Name: "a", Workdir: t.TempDir()}},
	})
	defer r.Close()

	gen, ok := r.Generation("a")
	if !ok {
		t.Fatal("project a should exist")
	}
	if gen != 0 {
		t.Errorf("initial generation = %d, want 0", gen)
	}

	r.MarkWritten("a")
	r.MarkWritten("a")

	gen, _ = r.Generation("a")
	if gen != 2 {
		t.Errorf("generation = %d, want 2 after two writes", gen)
	}
	if snap := r.Snapshot(); snap[0].Generation != 2 {
		t.Errorf("snapshot generation = %d, want 2", snap[0].Generation)
	}

	// An unknown project must be a silent no-op, not a panic.
	r.MarkWritten("nope")
	if _, ok := r.Generation("nope"); ok {
		t.Error("Generation reported an unknown project as existing")
	}
}

// TestI4DSNCarriesCredentials is the regression test for review finding I4:
// user/password were parsed from config, documented in the shipped comments,
// and then silently dropped, so a credentialed remote could only ever report
// "unreachable".
func TestI4DSNCarriesCredentials(t *testing.T) {
	tests := []struct {
		name     string
		user     string
		password string
		want     string
	}{
		{
			name: "defaults to root with no password (bd's own servers)",
			want: "root@tcp(127.0.0.1:3308)/loom?parseTime=true&timeout=2s&readTimeout=60s",
		},
		{
			name: "user only",
			user: "loomuser",
			want: "loomuser@tcp(127.0.0.1:3308)/loom?parseTime=true&timeout=2s&readTimeout=60s",
		},
		{
			name: "user and password", user: "loomuser", password: "s3cr3t",
			want: "loomuser:s3cr3t@tcp(127.0.0.1:3308)/loom?parseTime=true&timeout=2s&readTimeout=60s",
		},
		{
			name: "empty user with a password still defaults the user", password: "s3cr3t",
			want: "root:s3cr3t@tcp(127.0.0.1:3308)/loom?parseTime=true&timeout=2s&readTimeout=60s",
		},
		{
			// The driver parses credentials positionally and does no
			// percent-decoding, so these characters pass through verbatim and
			// still round-trip. Asserted below via mysql.ParseDSN.
			name: "password with DSN metacharacters", user: "loomuser", password: "p:s/w@rd",
			want: "loomuser:p:s/w@rd@tcp(127.0.0.1:3308)/loom?parseTime=true&timeout=2s&readTimeout=60s",
		},
		{
			name: "username containing @", user: "a@b", password: "pw",
			want: "a@b:pw@tcp(127.0.0.1:3308)/loom?parseTime=true&timeout=2s&readTimeout=60s",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DSNWithCredentials("127.0.0.1", 3308, "loom", tc.user, tc.password)
			if got != tc.want {
				t.Errorf("DSN = %q\nwant %q", got, tc.want)
			}
			cfg, err := mysql.ParseDSN(got)
			if err != nil {
				t.Fatalf("driver cannot parse the DSN: %v", err)
			}
			wantUser := tc.user
			if wantUser == "" {
				wantUser = "root"
			}
			if cfg.User != wantUser {
				t.Errorf("parsed user = %q, want %q", cfg.User, wantUser)
			}
			if cfg.Passwd != tc.password {
				t.Errorf("parsed password = %q, want %q", cfg.Passwd, tc.password)
			}
		})
	}

	// The no-credentials helper keeps one fixed shape; readTimeout outlasts
	// QueryTimeout so a slow query ends on its context deadline instead.
	if DSN("127.0.0.1", 55933, "loom") !=
		"root@tcp(127.0.0.1:55933)/loom?parseTime=true&timeout=2s&readTimeout=60s" {
		t.Errorf("DSN() changed shape: %q", DSN("127.0.0.1", 55933, "loom"))
	}
}

// TestN5EOFSubstringNoLongerMisclassifies: a SQL parse error mentioning EOF is
// not a connection failure and must not trigger the reconnect path.
func TestN5EOFSubstringNoLongerMisclassifies(t *testing.T) {
	sqlErrors := []string{
		"syntax error at position 12: unexpected EOF in statement",
		"Error 1064: unexpected EOF",
		"parse error: unexpected eof while scanning",
	}
	for _, msg := range sqlErrors {
		t.Run(msg, func(t *testing.T) {
			if isConnErr(errors.New(msg)) {
				t.Errorf("isConnErr(%q) = true; a SQL error must not be treated as a dead connection", msg)
			}
		})
	}

	// The genuine connection failures must still be caught.
	for _, msg := range []string{
		"dial tcp 127.0.0.1:1: connect: connection refused",
		"write: broken pipe",
		"invalid connection",
	} {
		if !isConnErr(errors.New(msg)) {
			t.Errorf("isConnErr(%q) = false; this is a real connection failure", msg)
		}
	}
}

func TestRefreshMissingWorkdirNamesConfig(t *testing.T) {
	workdir := filepath.Join(t.TempDir(), "gone")
	r := New(&config.Config{
		Path:     "/tmp/x/config.yaml",
		Projects: []config.Project{{Name: "gone", Workdir: workdir}},
	})
	defer r.Close()
	r.Refresh(context.Background())
	snap := r.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot = %v, want one project", snap)
	}
	if snap[0].Reachable {
		t.Fatal("missing workdir must be unreachable")
	}
	for _, want := range []string{workdir, "does not exist", `"gone"`, "/tmp/x/config.yaml"} {
		if !strings.Contains(snap[0].Reason, want) {
			t.Errorf("reason = %q, want %q", snap[0].Reason, want)
		}
	}
	if strings.Contains(snap[0].Reason, "not a beads workspace") {
		t.Errorf("misleading reason: %q", snap[0].Reason)
	}
}

func TestRefreshMissingWorkdirWithoutConfigPath(t *testing.T) {
	r := New(&config.Config{Projects: []config.Project{{Name: "gone", Workdir: filepath.Join(t.TempDir(), "gone")}}})
	defer r.Close()
	r.Refresh(context.Background())
	got := r.Snapshot()[0]
	if got.Reachable || !strings.Contains(got.Reason, "does not exist") || !strings.Contains(got.Reason, "Loom config") {
		t.Fatalf("missing workdir without config path: %+v", got)
	}
	if strings.Contains(got.Reason, `in ""`) || strings.HasSuffix(got.Reason, "in ") {
		t.Fatalf("empty config reference: %q", got.Reason)
	}
}

func TestRefreshFileWorkdirNamesConfig(t *testing.T) {
	workdir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(workdir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := New(&config.Config{
		Path:     "/tmp/x/config.yaml",
		Projects: []config.Project{{Name: "file", Workdir: workdir}},
	})
	defer r.Close()
	r.Refresh(context.Background())
	got := r.Snapshot()[0]
	if got.Reachable {
		t.Fatal("file workdir must be unreachable")
	}
	for _, want := range []string{workdir, "is not a directory", `"file"`, "/tmp/x/config.yaml"} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("reason = %q, want %q", got.Reason, want)
		}
	}
}

func TestRefreshExistingWorkdirWithoutBeads(t *testing.T) {
	r := New(&config.Config{
		Path:     "/tmp/x/config.yaml",
		Projects: []config.Project{{Name: "empty", Workdir: t.TempDir()}},
	})
	defer r.Close()
	r.Refresh(context.Background())
	got := r.Snapshot()[0]
	if got.Reachable || !strings.Contains(got.Reason, "no .beads directory in") || strings.Contains(got.Reason, "does not exist") {
		t.Fatalf("existing directory should retain its missing .beads reason: %+v", got)
	}
}
