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

const issueReceiptsDriverName = "loom-issue-receipts"

func init() {
	sql.Register(issueReceiptsDriverName, issueReceiptsDriver{})
}

type issueReceiptsDriver struct{}

func (issueReceiptsDriver) Open(string) (driver.Conn, error) { return issueReceiptsConn{}, nil }

type issueReceiptsConn struct{}

func (issueReceiptsConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Prepare should not be called")
}
func (issueReceiptsConn) Close() error { return nil }
func (issueReceiptsConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (issueReceiptsConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "SELECT COUNT(*)"):
		if !strings.Contains(query, "i.status = 'closed'") {
			return nil, errors.New("no-receipt count did not exclude open issues")
		}
		return &scriptedRows{columns: []string{"count"}, values: [][]driver.Value{{int64(2)}}}, nil
	case strings.Contains(query, "SELECT\n  i.id"):
		if !strings.Contains(query, "i.status = 'closed'") {
			return nil, errors.New("no-receipt list did not exclude open issues")
		}
		for _, want := range []string{"AS missing_acceptance", "AS missing_close_reason"} {
			if !strings.Contains(query, want) {
				return nil, errors.New("list query did not classify " + want)
			}
		}
		now := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
		columns := issueListColumns()
		return &scriptedRows{columns: columns, values: [][]driver.Value{
			{"no-ac", "No acceptance", "task", "closed", int64(2), "", nil, now, now, now, nil, nil, int64(0), false, nil, int64(0), int64(0), nil, nil, nil, nil, nil, true, false},
			{"neither", "Neither receipt", "task", "closed", int64(2), "", nil, now, now, now, nil, nil, int64(0), false, nil, int64(0), int64(0), nil, nil, nil, nil, nil, true, true},
		}}, nil
	case strings.HasPrefix(query, "SELECT issue_id, label"):
		return &scriptedRows{columns: []string{"issue_id", "label"}}, nil
	default:
		return nil, errors.New("unexpected query: " + query)
	}
}

var _ driver.QueryerContext = issueReceiptsConn{}

func TestListIssuesIdentifiesEachMissingReceipt(t *testing.T) {
	db, err := sql.Open(issueReceiptsDriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	got, err := queries.ListIssues(context.Background(), db, queries.Filter{NoReceipt: true})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if got.Total != 2 || len(got.Issues) != 2 {
		t.Fatalf("list = %+v, want two receipt-less closes", got)
	}
	if !got.Issues[0].MissingAcceptance || got.Issues[0].MissingCloseReason {
		t.Errorf("no-ac classification = %+v, want acceptance only", got.Issues[0])
	}
	if !got.Issues[1].MissingAcceptance || !got.Issues[1].MissingCloseReason {
		t.Errorf("neither classification = %+v, want both receipts missing", got.Issues[1])
	}
}
