package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/config"
	"github.com/kurofune/beads-loom/internal/queries"
	"github.com/kurofune/beads-loom/internal/readcache"
	"github.com/kurofune/beads-loom/internal/registry"
	"github.com/kurofune/beads-loom/internal/sse"
)

// countingDB is a counting driver: it records every statement and answers the
// hash read and Stats' reads from a tiny scripted backlog. open is the number
// of open issues; "bd create" is open++ with a new hash.
type countingDB struct {
	mu         sync.Mutex
	hash       string
	open       int64
	statements []string
}

func (c *countingDB) create() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.open++
	c.hash += "+"
}

func (c *countingDB) recorded() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.statements...)
}

func (c *countingDB) Connect(context.Context) (driver.Conn, error) { return countingDBConn{c}, nil }
func (c *countingDB) Driver() driver.Driver                        { return nil }

type countingDBConn struct{ c *countingDB }

func (countingDBConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("no prepare") }
func (countingDBConn) Close() error                        { return nil }
func (countingDBConn) Begin() (driver.Tx, error)           { return nil, errors.New("no tx") }

func (k countingDBConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	c := k.c
	c.mu.Lock()
	c.statements = append(c.statements, q)
	hash, open := c.hash, c.open
	c.mu.Unlock()

	rows := func(cols []string, vals ...[]driver.Value) driver.Rows { return &fixedRows{cols: cols, vals: vals} }
	switch {
	case strings.Contains(q, "dolt_hashof_db()"):
		return rows([]string{"h"}, []driver.Value{hash}), nil
	case strings.Contains(q, "ready_issues"):
		return rows([]string{"id"}, []driver.Value{"a"}), nil
	case strings.Contains(q, "GROUP BY i.status"):
		return rows([]string{"s", "n"}, []driver.Value{"open", open}), nil
	case strings.Contains(q, "blocked_transitively"):
		return rows([]string{"id", "status", "b", "d"}, []driver.Value{"a", "open", int64(0), int64(0)}), nil
	case strings.Contains(q, "INTERVAL 7 DAY"):
		return rows([]string{"n"}, []driver.Value{int64(0)}), nil
	case strings.Contains(q, "CURDATE() AS CHAR"):
		return rows([]string{"d"}, []driver.Value{"2026-11-01"}), nil
	case strings.Contains(q, "FROM events"):
		return rows([]string{"day", "o", "c"}), nil
	}
	return nil, errors.New("countingDB: unscripted statement: " + q)
}

var _ driver.QueryerContext = countingDBConn{}

type fixedRows struct {
	cols []string
	vals [][]driver.Value
	i    int
}

func (r *fixedRows) Columns() []string { return r.cols }
func (*fixedRows) Close() error        { return nil }
func (r *fixedRows) Next(dest []driver.Value) error {
	if r.i >= len(r.vals) {
		return io.EOF
	}
	copy(dest, r.vals[r.i])
	r.i++
	return nil
}

// newCountingServer serves project "fx" from a countingDB, with the result
// cache keyed by the same dolt_hashof_db() read the SSE prober makes.
func newCountingServer(t *testing.T) (*Server, *countingDB) {
	t.Helper()
	cdb := &countingDB{hash: "h1", open: 3}
	db := sql.OpenDB(cdb)
	t.Cleanup(func() { db.Close() })
	do := func(ctx context.Context, _ string, fn func(context.Context, *sql.DB) error) error { return fn(ctx, db) }
	cache := readcache.New(func(ctx context.Context, project string) (string, error) {
		var h string
		err := do(ctx, project, func(ctx context.Context, db *sql.DB) error {
			return db.QueryRowContext(ctx, "SELECT dolt_hashof_db()").Scan(&h)
		})
		return h, err
	})
	s := New(Options{Registry: registry.New(&config.Config{}), ReadCache: cache})
	s.queryDB = do
	return s, cdb
}

func getStats(t *testing.T, s *Server) []byte {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/fx/stats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stats = %d: %s", rec.Code, rec.Body)
	}
	return rec.Body.Bytes()
}

// Two stats requests under one dolt_hashof_db(): the second sends no SQL but
// the hash read and returns the same JSON. A create moves the hash, and the
// third request recomputes.
func TestStatsCacheServesUnchangedHashWithOnlyTheHashRead(t *testing.T) {
	s, cdb := newCountingServer(t)

	first := getStats(t, s)
	afterFirst := len(cdb.recorded())
	if afterFirst < 2 {
		t.Fatalf("first request sent %d statements; it should read the hash and compute", afterFirst)
	}

	second := getStats(t, s)
	sent := cdb.recorded()[afterFirst:]
	if len(sent) != 1 || !strings.Contains(sent[0], "dolt_hashof_db()") {
		t.Fatalf("second request under the same hash sent %q, want only the hash read", sent)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("cached JSON differs:\n%s\n%s", first, second)
	}

	cdb.create()
	afterSecond := len(cdb.recorded())
	third := getStats(t, s)
	sent = cdb.recorded()[afterSecond:]
	readyReads := 0
	for _, q := range sent {
		if strings.Contains(q, "ready_issues") {
			readyReads++
		}
	}
	if readyReads != 1 {
		t.Fatalf("after the hash moved the request sent %d ready_issues reads, want a recompute (1): %q", readyReads, sent)
	}
	var got queries.Stats
	if err := json.Unmarshal(third, &got); err != nil {
		t.Fatal(err)
	}
	if got.ByStatus["open"] != 4 {
		t.Errorf("recomputed byStatus = %v, want the created issue counted (open 4)", got.ByStatus)
	}
}

// Live: a real `bd create` on an embedded workspace moves the hash the prober
// reads, and the stats cache recomputes; until then it serves without reading.
func TestEmbeddedStatsCacheFollowsBdCreate(t *testing.T) {
	f := newEmbeddedFixture(t)
	prober := sse.NewRegistryProber(f.reg)
	f.server = New(Options{Registry: f.reg, ReadCache: prober.Cache()})
	var computes atomic.Int32
	f.server.queryDB = func(ctx context.Context, project string, fn func(context.Context, *sql.DB) error) error {
		computes.Add(1)
		return f.reg.Do(ctx, project, fn)
	}
	stats := func() queries.Stats {
		code, body := f.get(t, "/api/projects/fx/stats")
		if code != http.StatusOK {
			t.Fatalf("GET /stats = %d: %s", code, body)
		}
		var s queries.Stats
		if err := json.Unmarshal(body, &s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	first := stats()
	second := stats()
	if computes.Load() != 1 {
		t.Fatalf("two requests under one hash computed stats %d times, want 1", computes.Load())
	}
	if first.ByStatus["open"] != second.ByStatus["open"] {
		t.Fatalf("cached stats differ: %v vs %v", first.ByStatus, second.ByStatus)
	}

	f.w.Create(t, "Created after the cache filled")
	third := stats()
	if computes.Load() != 2 {
		t.Fatalf("after bd create stats computed %d times in all, want 2", computes.Load())
	}
	if third.ByStatus["open"] != first.ByStatus["open"]+1 {
		t.Errorf("after bd create open = %d, want %d", third.ByStatus["open"], first.ByStatus["open"]+1)
	}
}

// A request whose deadline passes while its cached read is still computing
// answers 504 query_timeout, the same as a query that ran out of time itself,
// never the 500 a bare context.DeadlineExceeded would map to.
func TestCachedReadWaiterDeadlineIs504(t *testing.T) {
	s, _ := newCountingServer(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	s.queryDB = func(ctx context.Context, _ string, _ func(context.Context, *sql.DB) error) error {
		<-release
		return errors.New("released")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/fx/stats", nil).WithContext(ctx))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504; body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"code":"query_timeout"`) {
		t.Errorf("body = %s, want code query_timeout", rec.Body)
	}
}
