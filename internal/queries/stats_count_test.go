package queries_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/kurofune/beads-loom/internal/queries"
)

// statsDriver is a counting driver: it records every statement Stats sends
// and answers each by its shape with a small scripted backlog.
//
// The backlog, by id:
//
//	a  open         ready
//	b  in_progress  ready (a custom-active status reads the same way)
//	c  open         dep-blocked
//	d  open         defer-scheduled only
//	e  open         dep-blocked and defer-scheduled: counts as blocked
//	x  (infra)      listed by ready_issues, hidden from Loom
type statsDriver struct {
	mu         sync.Mutex
	statements []string
}

func (d *statsDriver) Open(string) (driver.Conn, error) { return statsConn{d}, nil }

func (d *statsDriver) record(q string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.statements = append(d.statements, q)
}

func (d *statsDriver) recorded() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.statements...)
}

type statsConn struct{ d *statsDriver }

func (statsConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("no prepare") }
func (statsConn) Close() error                        { return nil }
func (statsConn) Begin() (driver.Tx, error)           { return nil, errors.New("no tx") }

func (c statsConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	c.d.record(q)
	rows := func(cols []string, vals ...[]driver.Value) driver.Rows {
		return &scriptedRows{columns: cols, values: vals}
	}
	switch {
	case strings.Contains(q, "ready_issues"):
		return rows([]string{"id"}, []driver.Value{"a"}, []driver.Value{"b"}, []driver.Value{"x"}), nil
	case strings.Contains(q, "GROUP BY i.status"):
		return rows([]string{"status", "n"},
			[]driver.Value{"open", int64(4)}, []driver.Value{"in_progress", int64(1)}, []driver.Value{"closed", int64(7)}), nil
	case strings.Contains(q, "blocked_transitively"):
		return rows([]string{"id", "status", "dep_blocked", "defer_scheduled"},
			[]driver.Value{"a", "open", int64(0), int64(0)},
			[]driver.Value{"b", "in_progress", int64(0), int64(0)},
			[]driver.Value{"c", "open", int64(1), int64(0)},
			[]driver.Value{"d", "open", int64(0), int64(1)},
			[]driver.Value{"e", "open", int64(1), int64(1)},
		), nil
	case strings.Contains(q, "INTERVAL 7 DAY"):
		return rows([]string{"n"}, []driver.Value{int64(3)}), nil
	case strings.Contains(q, "CURDATE() AS CHAR"):
		return rows([]string{"d"}, []driver.Value{"2026-11-01"}), nil
	case strings.Contains(q, "FROM events"):
		return rows([]string{"day", "opened", "closed"}, []driver.Value{"2026-11-01", int64(2), int64(1)}), nil
	}
	return nil, errors.New("statsDriver: unscripted statement: " + q)
}

var _ driver.QueryerContext = statsConn{}

func openStatsFixture(t *testing.T) (*sql.DB, *statsDriver) {
	t.Helper()
	d := &statsDriver{}
	db := sql.OpenDB(statsConnector{d})
	t.Cleanup(func() { db.Close() })
	return db, d
}

type statsConnector struct{ d *statsDriver }

func (c statsConnector) Connect(context.Context) (driver.Conn, error) { return statsConn{c.d}, nil }
func (c statsConnector) Driver() driver.Driver                        { return c.d }

// Stats reads the recursive ready_issues view once per call: the ready count
// and the dep-blocked split both come from that one materialized id set.
func TestStatsReadsReadyIssuesOnce(t *testing.T) {
	db, d := openStatsFixture(t)

	for call := 1; call <= 2; call++ {
		s, err := queries.StatsFor(context.Background(), db)
		if err != nil {
			t.Fatalf("StatsFor: %v", err)
		}
		n := 0
		for _, q := range d.recorded() {
			if strings.Contains(q, "ready_issues") {
				n++
			}
		}
		if n != call {
			t.Fatalf("after %d Stats calls, %d statements referenced ready_issues; want exactly one per call", call, n)
		}

		if s.Ready != 2 || s.ReadyFrontier != 2 {
			t.Errorf("ready = %d frontier %d, want 2: x is infra and hidden", s.Ready, s.ReadyFrontier)
		}
		if s.DepBlocked != 2 {
			t.Errorf("depBlocked = %d, want 2 (c, and e which is also deferred)", s.DepBlocked)
		}
		if s.DeferScheduled != 1 {
			t.Errorf("deferScheduled = %d, want 1 (d)", s.DeferScheduled)
		}
		if s.ByStatus["open"] != 4 || s.ByStatus["closed"] != 7 || s.ClosedLast7d != 3 {
			t.Errorf("byStatus %v closed7d %d", s.ByStatus, s.ClosedLast7d)
		}
		if len(s.Trend) != queries.TrendDays || s.Trend[len(s.Trend)-1].Opened != 2 {
			t.Errorf("trend = %d points ending %+v", len(s.Trend), s.Trend[len(s.Trend)-1])
		}
	}
}
