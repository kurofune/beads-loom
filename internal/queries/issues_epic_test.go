package queries_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/queries"
)

const epicScopeDriverName = "loom-epic-scope"

func init() {
	sql.Register(epicScopeDriverName, epicScopeDriver{})
}

type epicScopeDriver struct{}

func (epicScopeDriver) Open(name string) (driver.Conn, error) {
	return epicScopeConn{childless: name == "childless", nonEpic: name == "non-epic"}, nil
}

type epicScopeConn struct {
	childless bool
	nonEpic   bool
}

func (epicScopeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Prepare should not be called")
}

func (epicScopeConn) Close() error { return nil }
func (epicScopeConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (c epicScopeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "SELECT COUNT(*)"):
		if !strings.Contains(query, "WITH RECURSIVE subtree") || !c.startsWithScopeID(args) || len(args) != 1 {
			return nil, errors.New("count query did not scope through the recursive subtree")
		}
		if c.childless || (c.nonEpic && strings.Contains(query, "issue_type = 'epic'")) {
			return &scriptedRows{columns: []string{"count"}, values: [][]driver.Value{{int64(0)}}}, nil
		}
		return &scriptedRows{columns: []string{"count"}, values: [][]driver.Value{{int64(2)}}}, nil
	case strings.Contains(query, "SELECT\n  i.id"):
		if !strings.Contains(query, "WITH RECURSIVE subtree") || !c.startsWithScopeID(args) || len(args) != 3 {
			return nil, errors.New("list query did not scope through the recursive subtree")
		}
		now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		return &scriptedRows{columns: issueListColumns(), values: [][]driver.Value{
			{"direct-child", "Direct child", "task", "open", int64(1), "", nil, now, now, nil, nil, nil, int64(0), true, nil, int64(0), int64(0), "epic-root", "Epic root", nil, nil, nil},
			{"grandchild", "Grandchild", "task", "open", int64(2), "", nil, now, now, nil, nil, nil, int64(0), true, nil, int64(0), int64(0), "direct-child", "Direct child", nil, nil, nil},
		}}, nil
	case strings.HasPrefix(query, "SELECT issue_id, label"):
		if !startsWithDescendantIDs(args) || len(args) != 2 {
			return nil, errors.New("label query was not scoped to the two descendant ids")
		}
		return &scriptedRows{columns: []string{"issue_id", "label"}}, nil
	case strings.Contains(query, "WITH RECURSIVE subtree"):
		return nil, errors.New("epic descendants were materialized in a separate query")
	default:
		return nil, errors.New("unexpected query: " + query)
	}
}

func (c epicScopeConn) startsWithScopeID(args []driver.NamedValue) bool {
	want := "epic-root"
	if c.nonEpic {
		want = "feature-root"
	}
	return len(args) >= 1 && args[0].Value == want
}

func startsWithDescendantIDs(args []driver.NamedValue) bool {
	return len(args) >= 2 && args[0].Value == "direct-child" && args[1].Value == "grandchild"
}

func TestListIssuesEpicScopeChildlessIsEmpty(t *testing.T) {
	db, err := sql.Open(epicScopeDriverName, "childless")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	got, err := queries.ListIssues(context.Background(), db, queries.Filter{
		Epic:          "epic-root",
		IncludeClosed: true,
	})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if got.Total != 0 || len(got.Issues) != 0 {
		t.Fatalf("childless epic scope = %+v, want an empty page", got)
	}
}

func TestListIssuesEpicScopeRejectsNonEpicTarget(t *testing.T) {
	db, err := sql.Open(epicScopeDriverName, "non-epic")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	got, err := queries.ListIssues(context.Background(), db, queries.Filter{
		Epic:          "feature-root",
		IncludeClosed: true,
	})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if got.Total != 0 || len(got.Issues) != 0 {
		t.Fatalf("non-epic scope = %+v, want an empty page", got)
	}
}

var _ driver.QueryerContext = epicScopeConn{}

type scriptedRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *scriptedRows) Columns() []string { return r.columns }
func (*scriptedRows) Close() error        { return nil }
func (r *scriptedRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

func issueListColumns() []string {
	return append(issueBaseColumns(), "missing_acceptance", "missing_close_reason")
}

func issueBaseColumns() []string {
	return []string{
		"id", "title", "issue_type", "status", "priority", "assignee", "estimated_minutes",
		"created_at", "updated_at", "closed_at", "due_at", "defer_until", "pinned", "ready",
		"not_ready", "blocked_by", "blocks", "parent_id", "parent_title",
		"child_total", "child_closed", "child_started",
	}
}

func TestListIssuesEpicScopeUsesInStatementTransitiveScope(t *testing.T) {
	db, err := sql.Open(epicScopeDriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	got, err := queries.ListIssues(context.Background(), db, queries.Filter{
		Epic:          "epic-root",
		IncludeClosed: true,
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if got.Total != 2 || len(got.Issues) != 2 {
		t.Fatalf("epic scope = %+v, want two descendants", got)
	}
	if got.Issues[0].ID != "direct-child" || got.Issues[1].ID != "grandchild" {
		t.Fatalf("ids = [%s, %s], want direct child and grandchild", got.Issues[0].ID, got.Issues[1].ID)
	}
}
