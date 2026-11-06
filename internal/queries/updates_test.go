package queries_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kurofune/beads-loom/internal/queries"
)

// existingIDsConn records the statement and its bound arguments so the test can
// prove the ids travel as placeholders rather than as interpolated SQL.
type existingIDsConn struct {
	query string
	args  []string
}

func (*existingIDsConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Prepare should not be called")
}
func (*existingIDsConn) Close() error { return nil }
func (*existingIDsConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

var _ driver.QueryerContext = (*existingIDsConn)(nil)

func (c *existingIDsConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.query = query
	c.args = nil
	for _, a := range args {
		s, ok := a.Value.(string)
		if !ok {
			return nil, errors.New("non-string argument bound to the issue-existence query")
		}
		c.args = append(c.args, s)
	}
	// Only loom-d1q.1 and loom-9dx are real; the rest are prose that merely
	// looks like an id.
	rows := &existingIDsRows{columns: []string{"id"}}
	for _, id := range c.args {
		if id == "loom-d1q.1" || id == "loom-9dx" {
			rows.values = append(rows.values, []driver.Value{id})
		}
	}
	return rows, nil
}

type existingIDsRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *existingIDsRows) Columns() []string { return r.columns }
func (*existingIDsRows) Close() error        { return nil }
func (r *existingIDsRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

var existingIDsDriverSeq atomic.Int64

func openExistingIDsFixture(t *testing.T) (*sql.DB, *existingIDsConn) {
	t.Helper()
	conn := &existingIDsConn{}
	// A fresh driver name per fixture: sql.Register panics on a duplicate, so a
	// name derived from the test would blow up under -count=2.
	name := fmt.Sprintf("loom-existing-ids-%d", existingIDsDriverSeq.Add(1))
	sql.Register(name, singleConnDriver{conn})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, conn
}

type singleConnDriver struct{ conn *existingIDsConn }

func (d singleConnDriver) Open(string) (driver.Conn, error) { return d.conn, nil }

func TestExistingIssueIDsReturnsOnlyTheIdsThatResolve(t *testing.T) {
	db, conn := openExistingIDsFixture(t)

	got, err := queries.ExistingIssueIDs(context.Background(), db,
		[]string{"loom-d1q.1", "read-only", "loom-9dx", "follow-up"})
	if err != nil {
		t.Fatalf("ExistingIssueIDs: %v", err)
	}
	want := []string{"loom-9dx", "loom-d1q.1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExistingIssueIDs = %#v, want %#v", got, want)
	}
	if !strings.Contains(conn.query, "FROM issues") || !strings.Contains(conn.query, "IN (?, ?, ?, ?)") {
		t.Fatalf("query = %q, want one placeholder per id", conn.query)
	}
	if len(conn.args) != 4 {
		t.Fatalf("bound args = %#v, want all four ids bound", conn.args)
	}
}

func TestExistingIssueIDsSkipsTheQueryWhenThereIsNothingToCheck(t *testing.T) {
	db, conn := openExistingIDsFixture(t)

	got, err := queries.ExistingIssueIDs(context.Background(), db, nil)
	if err != nil {
		t.Fatalf("ExistingIssueIDs: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("ExistingIssueIDs = %#v, want an empty non-nil slice", got)
	}
	if conn.query != "" {
		t.Fatalf("query = %q, want no round trip for an empty id set", conn.query)
	}
}

func TestExistingIssueIDsBoundsTheIdSet(t *testing.T) {
	db, conn := openExistingIDsFixture(t)

	ids := make([]string, queries.MaxExistenceIDs+50)
	for i := range ids {
		ids[i] = fmt.Sprintf("loom-%04d", i)
	}
	if _, err := queries.ExistingIssueIDs(context.Background(), db, ids); err != nil {
		t.Fatalf("ExistingIssueIDs: %v", err)
	}
	if len(conn.args) != queries.MaxExistenceIDs {
		t.Fatalf("bound args = %d, want the set capped at %d", len(conn.args), queries.MaxExistenceIDs)
	}
}
