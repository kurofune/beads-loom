package queries_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/beads-loom/internal/queries"
)

const issueHealthDriverName = "loom-issue-health"

func init() {
	sql.Register(issueHealthDriverName, issueHealthDriver{})
}

type issueHealthDriver struct{}

func (issueHealthDriver) Open(string) (driver.Conn, error) { return issueHealthConn{}, nil }

type issueHealthConn struct{}

func (issueHealthConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Prepare should not be called")
}
func (issueHealthConn) Close() error { return nil }
func (issueHealthConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

var _ driver.QueryerContext = issueHealthConn{}

func (issueHealthConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	switch {
	case strings.Contains(query, "i.description, i.acceptance_criteria"):
		return &scriptedRows{columns: issueDetailColumns(), values: [][]driver.Value{
			{"ephemeral-epic", "Ephemeral epic", "epic", "open", int64(2), "", nil, now, now, nil, nil, nil, int64(0), false, nil, int64(0), int64(0), nil, nil, nil, nil, nil,
				"", "", "", "", "", ""},
		}}, nil
	case strings.Contains(query, "epics AS ("):
		return &scriptedRows{columns: initiativeColumns()}, nil
	case strings.Contains(query, "SELECT kind, t.id"):
		return &scriptedRows{columns: []string{"kind", "id", "title", "status", "priority", "ready"}}, nil
	case strings.HasPrefix(query, "SELECT label FROM labels"):
		return &scriptedRows{columns: []string{"label"}}, nil
	case strings.Contains(query, "FROM comments"):
		return &scriptedRows{columns: []string{"id", "author", "text", "created_at"}}, nil
	case strings.Contains(query, "FROM events"):
		return &scriptedRows{columns: []string{"event_type", "actor", "old_value", "new_value", "comment", "created_at"}}, nil
	default:
		return nil, errors.New("unexpected query: " + query)
	}
}

func TestGetIssueDegradesWhenInitiativeHealthIsNotVisible(t *testing.T) {
	db, err := sql.Open(issueHealthDriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	got, err := queries.GetIssue(context.Background(), db, "ephemeral-epic")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.ID != "ephemeral-epic" {
		t.Fatalf("issue id = %q, want ephemeral-epic", got.ID)
	}
	if got.Health != nil {
		t.Fatalf("health = %#v, want nil when initiative enrichment is not visible", got.Health)
	}
}
