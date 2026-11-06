package queries

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// StaleAfter is how long an issue may sit untouched in in_progress before Loom
// flags it. A day is the point at which "someone is working on this right now"
// stops being a credible claim — an agent run that has not written anything in
// 24h has almost certainly died.
const StaleAfter = 24 * time.Hour

// NowIssue is one in-progress issue in the cross-project Now view.
type NowIssue struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Status    string     `json:"status"`
	Priority  int        `json:"priority"`
	Ready     bool       `json:"ready"`
	Assignee  string     `json:"assignee"`
	StartedAt *time.Time `json:"startedAt"`
}

// NowEntry pairs an in-progress issue with the project it lives in.
type NowEntry struct {
	Project string   `json:"project"`
	Issue   NowIssue `json:"issue"`
	// InStatusSince is started_at, falling back to the most recent
	// status_changed event, and finally to updated_at.
	InStatusSince *time.Time `json:"inStatusSince"`
	// Stale means nothing has touched the issue in StaleAfter.
	Stale bool `json:"stale"`
}

// nowSelect finds in-progress work. inStatusSince prefers started_at and falls
// back to the last status_changed event, because bd only began populating
// started_at recently and older beads have a NULL there.
const nowSelect = `
SELECT
  i.id, i.title, i.status, i.priority,
  COALESCE(i.assignee, ''),
  i.started_at,
  COALESCE(
    i.started_at,
    (SELECT MAX(e.created_at) FROM events e
      WHERE e.issue_id = i.id AND e.event_type = 'status_changed'),
    i.updated_at
  ) AS in_status_since,
  i.updated_at,
  (r.id IS NOT NULL) AS ready
FROM issues i
LEFT JOIN ready_issues r ON r.id = i.id
WHERE %s
  AND i.status = ?
ORDER BY in_status_since ASC, i.id ASC`

// NowFor returns every in-progress issue in one project, newest work last.
// The caller stamps the project name and combines results across projects.
func NowFor(ctx context.Context, db *sql.DB, project string, now time.Time) ([]NowEntry, error) {
	rows, err := db.QueryContext(ctx,
		fmt.Sprintf(nowSelect, VisiblePredicate("i")), StatusInProgress)
	if err != nil {
		return nil, fmt.Errorf("now query: %w", err)
	}
	defer rows.Close()

	out := []NowEntry{}
	for rows.Next() {
		var (
			e         NowEntry
			started   sql.NullTime
			since     sql.NullTime
			updated   time.Time
			readyFlag bool
		)
		if err := rows.Scan(
			&e.Issue.ID, &e.Issue.Title, &e.Issue.Status, &e.Issue.Priority,
			&e.Issue.Assignee, &started, &since, &updated, &readyFlag,
		); err != nil {
			return nil, fmt.Errorf("scan now row: %w", err)
		}
		e.Project = project
		e.Issue.Ready = readyFlag
		e.Issue.StartedAt = nullTime(started)
		e.InStatusSince = nullTime(since)
		e.Stale = now.Sub(updated.UTC()) > StaleAfter
		out = append(out, e)
	}
	return out, rows.Err()
}
