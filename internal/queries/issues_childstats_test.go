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

const childStatsDriverName = "loom-child-stats"

func init() {
	sql.Register(childStatsDriverName, childStatsDriver{})
}

// childStatsDriver answers both the backlog list statement and the detail
// statement, asserting on the way through that the child-stats subselect counts
// started direct children from the shared status constants.
type childStatsDriver struct{}

func (childStatsDriver) Open(string) (driver.Conn, error) { return childStatsConn{}, nil }

type childStatsConn struct{}

func (childStatsConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Prepare should not be called")
}
func (childStatsConn) Close() error { return nil }
func (childStatsConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

var _ driver.QueryerContext = childStatsConn{}

// startedSUM is the exact expression both child-stats subselects must carry.
// Written out in full so a test failure names the drift rather than hinting at
// it, and so hardcoding one literal (or dropping 'hooked') turns this red.
const startedSUM = "SUM(CASE WHEN c.status IN ('in_progress', 'hooked') THEN 1 ELSE 0 END) AS started"

// closedSUM is the closed arm, spelled out so the pinned exclusion below cannot
// be satisfied by narrowing this CASE instead of the subselect's WHERE. closed
// and started must keep counting exactly the direct children they counted
// before pinned left the denominator.
const closedSUM = "SUM(CASE WHEN c.status = 'closed' THEN 1 ELSE 0 END) AS closed"

// pinnedExclusionWHERE is the arm both child-stats subselects must carry so a
// pinned direct child stops inflating ChildStats.total. It mirrors the
// join-level exclusion InitiativesFor already applies to its descendant set
// (internal/queries/initiatives.go), which is what keeps the backlog/board
// denominator and the Initiatives denominator reporting the same number.
//
// Built from queries.StatusPinned rather than an inline literal, and pinned to
// its position in the WHERE clause so the exclusion cannot drift onto some
// other predicate that happens to mention the status.
const pinnedExclusionWHERE = "WHERE d.type = 'parent-child' AND c.status <> '" + queries.StatusPinned + "'"

// childStatsSubselect isolates the `cs` LEFT JOIN so an assertion about it
// cannot be satisfied by a started SUM that lives somewhere else in the query.
func childStatsSubselect(query string) (string, error) {
	start := strings.Index(query, "LEFT JOIN (\n  SELECT d.depends_on_issue_id AS id,\n         COUNT(*) AS total,")
	if start < 0 {
		return "", errors.New("query has no child-stats subselect")
	}
	rest := query[start:]
	end := strings.Index(rest, ") cs ON cs.id = i.id")
	if end < 0 {
		return "", errors.New("child-stats subselect is not joined as cs")
	}
	return rest[:end], nil
}

func checkChildStatsSubselect(query string) error {
	subselect, err := childStatsSubselect(query)
	if err != nil {
		return err
	}
	if !strings.Contains(subselect, startedSUM) {
		return errors.New("child-stats subselect does not sum started from the status constants:\n" + subselect)
	}
	if !strings.Contains(subselect, pinnedExclusionWHERE) {
		return errors.New("child-stats subselect still counts pinned direct children in total; " +
			"its WHERE must carry " + pinnedExclusionWHERE + ":\n" + subselect)
	}
	if !strings.Contains(subselect, closedSUM) {
		return errors.New("child-stats subselect's closed arm changed; pinned leaves via the WHERE, " +
			"not by narrowing closed:\n" + subselect)
	}
	if !strings.Contains(subselect, "cs.total, cs.closed, cs.started") && !strings.Contains(query, "cs.total, cs.closed, cs.started") {
		return errors.New("the select list does not project cs.started")
	}
	// DIRECT children only: the rollup walks one parent-child edge, never the
	// recursive subtree CTE that InitiativesFor uses. A grandchild therefore
	// cannot reach this SUM.
	for _, transitive := range []string{"RECURSIVE", "subtree", "depth"} {
		if strings.Contains(subselect, transitive) {
			return errors.New("child-stats subselect went transitive via " + transitive + ":\n" + subselect)
		}
	}
	return nil
}

// childStatsDue is the scripted epic's due date. ClassifyHealth reads the real
// clock, so a fixed literal would silently change the expected health state as
// the wall clock passed it; 30 days of runway keeps the epic at-risk whichever
// side of UTC midnight the fixture and the query land on.
func childStatsDue() time.Time {
	return time.Now().UTC().AddDate(0, 0, 30)
}

func (childStatsConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	due := childStatsDue()
	switch {
	case strings.Contains(query, "SELECT COUNT(*)"):
		return &scriptedRows{columns: []string{"count"}, values: [][]driver.Value{{int64(3)}}}, nil

	// The detail statement also opens with "SELECT\n  i.id", so it is matched
	// first by its prose columns.
	case strings.Contains(query, "i.description, i.acceptance_criteria"):
		if err := checkChildStatsSubselect(query); err != nil {
			return nil, err
		}
		return &scriptedRows{columns: issueDetailColumns(), values: [][]driver.Value{
			{"epic-root", "Epic root", "epic", "open", int64(1), "", nil, now, now, nil, due, nil, int64(0), false, nil, int64(0), int64(0), nil, nil, int64(3), int64(1), int64(2),
				"desc", "ac", "design", "notes", "", ""},
		}}, nil

	case strings.Contains(query, "epics AS ("):
		if len(args) != 2 || args[0].Value != "epic-root" {
			return nil, errors.New("epic health query must bind epic-root before its cutoff")
		}
		// The transitive rollup is deliberately WIDER than the direct-children
		// rollup above: 4 descendants (1 closed, that one inside the 14-day
		// window) against 3 direct children. Health computed from the wrong
		// denominator therefore names a different remaining count and a
		// different state, instead of coinciding by accident.
		return &scriptedRows{columns: initiativeColumns(), values: [][]driver.Value{
			{"epic-root", "Epic root", "open", int64(1), "", due, now, now, int64(4), int64(1), int64(1), int64(2), int64(0), int64(1), int64(0)},
		}}, nil

	case strings.Contains(query, "SELECT\n  i.id"):
		if err := checkChildStatsSubselect(query); err != nil {
			return nil, err
		}
		// epic-root has three direct children: one closed, two started. The
		// grandchild below is in_progress and belongs to direct-child, so it
		// must never reach epic-root's rollup.
		return &scriptedRows{columns: issueListColumns(), values: [][]driver.Value{
			{"epic-root", "Epic root", "epic", "open", int64(1), "", nil, now, now, nil, nil, nil, int64(0), false, nil, int64(0), int64(0), nil, nil, int64(3), int64(1), int64(2)},
			{"direct-child", "Direct child", "task", "in_progress", int64(1), "", nil, now, now, nil, nil, nil, int64(0), false, nil, int64(0), int64(0), "epic-root", "Epic root", int64(1), int64(0), int64(1)},
			{"grandchild", "Grandchild", "task", "in_progress", int64(2), "", nil, now, now, nil, nil, nil, int64(0), false, nil, int64(0), int64(0), "direct-child", "Direct child", nil, nil, nil},
		}}, nil

	case strings.HasPrefix(query, "SELECT issue_id, label"):
		return &scriptedRows{columns: []string{"issue_id", "label"}}, nil

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

func issueDetailColumns() []string {
	return append(issueBaseColumns(),
		"description", "acceptance_criteria", "design", "notes", "close_reason", "owner")
}

func openChildStatsFixture(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(childStatsDriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestListIssuesChildStatsCarriesStarted pins AC-1 for the list statement: the
// backlog's child-stats rollup projects started, and the value survives the
// scan onto ChildStats.
func TestListIssuesChildStatsCarriesStarted(t *testing.T) {
	db := openChildStatsFixture(t)

	got, err := queries.ListIssues(context.Background(), db, queries.Filter{
		IncludeClosed: true,
		Limit:         queries.MaxLimit,
	})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(got.Issues) != 3 {
		t.Fatalf("issues = %d, want the three fabricated rows", len(got.Issues))
	}

	checked := 0
	for _, issue := range got.Issues {
		switch issue.ID {
		case "epic-root":
			checked++
			if issue.ChildStats == nil {
				t.Fatalf("epic-root has no child stats")
			}
			if issue.ChildStats.Started != 2 {
				t.Errorf("epic-root started = %d, want 2 direct in-flight children", issue.ChildStats.Started)
			}
			if issue.ChildStats.Closed != 1 {
				t.Errorf("epic-root closed = %d, want 1 — started must never be folded into closed", issue.ChildStats.Closed)
			}
			if issue.ChildStats.Total != 3 {
				t.Errorf("epic-root total = %d, want 3", issue.ChildStats.Total)
			}
		case "direct-child":
			checked++
			// The grandchild is in_progress; it is direct-child's child, so it
			// counts here and NOT on epic-root above. That is the whole
			// direct-vs-subtree distinction.
			if issue.ChildStats == nil {
				t.Fatalf("direct-child has no child stats")
			}
			if issue.ChildStats.Started != 1 {
				t.Errorf("direct-child started = %d, want its own in_progress child", issue.ChildStats.Started)
			}
		case "grandchild":
			checked++
			if issue.ChildStats != nil {
				t.Errorf("grandchild has child stats %+v, want none", issue.ChildStats)
			}
		}
	}
	if checked != 3 {
		t.Fatalf("checked %d rows, want all three — the fixture drifted", checked)
	}
}

// TestGetIssueChildStatsCarriesStarted pins AC-1 for the detail statement — the
// twin subselect is a separate piece of SQL and drifts independently.
func TestGetIssueChildStatsCarriesStarted(t *testing.T) {
	db := openChildStatsFixture(t)

	got, err := queries.GetIssue(context.Background(), db, "epic-root")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.ChildStats == nil {
		t.Fatal("detail response has no child stats")
	}
	if got.ChildStats.Started != 2 {
		t.Errorf("detail started = %d, want 2", got.ChildStats.Started)
	}
	if got.ChildStats.Closed != 1 {
		t.Errorf("detail closed = %d, want 1", got.ChildStats.Closed)
	}
	if got.ChildStats.Total != 3 {
		t.Errorf("detail total = %d, want 3", got.ChildStats.Total)
	}

	// Detail health must ride the transitive InitiativeFor rollup (4 descendants,
	// 3 still open) and never the direct ChildStats one (3 children, 2 still
	// open) — the two-healths-from-two-denominators dishonesty the bead exists to
	// prevent. Asserted here, on the fixture driver, because every live health
	// test skips when no Dolt server is attached.
	if got.Health == nil {
		t.Fatal("detail response has no health; the epic detail dropped its initiative rollup")
	}
	if got.Health.State != "at-risk" {
		t.Errorf("detail health state = %q, want at-risk (explanation: %q)", got.Health.State, got.Health.Explanation)
	}
	if !strings.Contains(got.Health.Explanation, "closed 1 in 14d, 3 remain") {
		t.Errorf("detail health explanation = %q, want the subtree denominator: 3 of 4 descendants still open",
			got.Health.Explanation)
	}
}
