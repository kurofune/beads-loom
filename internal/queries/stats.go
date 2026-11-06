package queries

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Stats counts the project's backlog. Infra rows are excluded on every count so
// the numbers agree with what the backlog view shows.
//
// depBlocked deserves a note: bd keeps a dependency-blocked issue at stored
// status "open" (the "blocked" status is a manual flag). So the issues that are
// genuinely stuck on a dependency are exactly the open ones missing from
// ready_issues.
//
// ready_issues is bd's recursive view and walks every issue and dependency,
// closed ones included, so on a long-lived backlog it is the slowest thing
// Stats reads. It is read exactly once, as an id set, and both the ready count
// and the dep-blocked split are taken from that set in Go. The independent
// reads run concurrently, so a cold Stats costs roughly its slowest read.
func StatsFor(ctx context.Context, db *sql.DB) (*Stats, error) {
	var (
		s        = &Stats{}
		readyIDs map[string]bool
		open     []openWork
	)
	err := runConcurrently(ctx,
		func(ctx context.Context) (err error) {
			s.ByStatus, err = statusCounts(ctx, db)
			return err
		},
		func(ctx context.Context) (err error) {
			readyIDs, err = readyIDSet(ctx, db)
			return err
		},
		func(ctx context.Context) (err error) {
			open, err = openWorkRows(ctx, db)
			return err
		},
		func(ctx context.Context) error {
			return closedLast7d(ctx, db, &s.ClosedLast7d)
		},
		func(ctx context.Context) (err error) {
			s.Trend, err = trendFor(ctx, db, TrendDays)
			return err
		},
	)
	if err != nil {
		return nil, err
	}

	// Split "open but not ready" into the two reasons it can happen. Counting
	// them together reported defer-scheduled work as blocked (review I2).
	for _, w := range open {
		switch {
		case readyIDs[w.id]:
			// ready_issues also lists infra rows; only visible ones count.
			s.Ready++
		case w.status != StatusOpen:
		case w.depBlocked:
			s.DepBlocked++
		case w.deferScheduled:
			s.DeferScheduled++
		}
	}
	s.ReadyFrontier = s.Ready
	return s, nil
}

// runConcurrently runs every fn at once and returns the first error. The first
// failure cancels the others, so a dead server fails the call once rather than
// once per read.
func runConcurrently(ctx context.Context, fns ...func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error, len(fns))
	for _, fn := range fns {
		go func(fn func(context.Context) error) {
			err := fn(ctx)
			if err != nil {
				cancel()
			}
			errs <- err
		}(fn)
	}
	var first error
	for range fns {
		// A sibling's cancellation is a consequence of the first error, not a
		// cause worth reporting over it.
		if err := <-errs; err != nil && (first == nil || errors.Is(first, context.Canceled)) {
			first = err
		}
	}
	return first
}

// openWork is one visible, unclosed issue and why it might not be ready.
type openWork struct {
	id, status     string
	depBlocked     bool
	deferScheduled bool
}

func statusCounts(ctx context.Context, db *sql.DB) (map[string]int, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT i.status, COUNT(*) FROM issues i WHERE "+VisiblePredicate("i")+" GROUP BY i.status")
	if err != nil {
		return nil, fmt.Errorf("stats by status: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("scan status count: %w", err)
		}
		out[status] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats by status: %w", err)
	}
	return out, nil
}

// readyIDSet materializes ready_issues once. The view lists only open and
// custom-active issues, a few dozen even on a backlog of thousands.
func readyIDSet(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT id FROM ready_issues")
	if err != nil {
		return nil, fmt.Errorf("stats ready: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("stats ready: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats ready: %w", err)
	}
	return out, nil
}

// openWorkRows lists every visible issue that is not closed, with whether a
// dependency or a schedule holds it back. Closed issues are never ready, so
// the scan skips the bulk of a long-lived backlog.
func openWorkRows(ctx context.Context, db *sql.DB) ([]openWork, error) {
	rows, err := db.QueryContext(ctx, `
WITH RECURSIVE `+blockedTransitiveCTE+`
SELECT i.id, i.status,
  CASE WHEN bt.issue_id IS NOT NULL THEN 1 ELSE 0 END AS dep_blocked,
  CASE WHEN `+deferScheduledExpr("i", "dfp")+` THEN 1 ELSE 0 END AS defer_scheduled
FROM issues i
LEFT JOIN (SELECT DISTINCT issue_id FROM blocked_transitively) bt ON bt.issue_id = i.id
`+deferParentJoin("i", "dfp")+`
WHERE `+VisiblePredicate("i")+`
  AND i.status <> ?`, StatusClosed)
	if err != nil {
		return nil, fmt.Errorf("stats dep-blocked: %w", err)
	}
	defer rows.Close()
	var out []openWork
	for rows.Next() {
		var w openWork
		var blocked, deferred int64
		if err := rows.Scan(&w.id, &w.status, &blocked, &deferred); err != nil {
			return nil, fmt.Errorf("stats dep-blocked: %w", err)
		}
		w.depBlocked, w.deferScheduled = blocked != 0, deferred != 0
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats dep-blocked: %w", err)
	}
	return out, nil
}

func closedLast7d(ctx context.Context, db *sql.DB, n *int) error {
	err := db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM issues i
WHERE `+VisiblePredicate("i")+`
  AND i.status = ?
  AND i.closed_at IS NOT NULL
  AND i.closed_at >= UTC_TIMESTAMP() - INTERVAL 7 DAY`, StatusClosed).Scan(n)
	if err != nil {
		return fmt.Errorf("stats closed-last-7d: %w", err)
	}
	return nil
}

// trendFor computes the opened/closed trend from the events table.
//
// `created` and `closed` events are the source rather than issues.created_at /
// closed_at, because an issue that was closed and reopened contributes to both
// series on the days it actually moved — which is what a trend is for. Rows are
// emitted for every day in the window, including zero days, so the UI can plot
// without filling gaps itself.
//
// The window is anchored to the DATABASE's current date, not Go's. That is not
// fussiness: `events.created_at` stores the writing process's LOCAL wall-clock
// time while `issues.created_at` stores UTC — verified on the seeded db, where
// bead loom-txm's `created` event reads 2026-08-16 00:28:50 in events and
// 2026-08-16 07:28:51 in issues, the -7h of the machine that wrote it.
// CURDATE() is in the same local day space (Dolt resolves it from the server's
// system zone, and unlike NOW() it ignores the session time zone), so anchor
// and buckets share one clock. Bucketing DATE(e.created_at) against a
// UTC-derived day series silently produced an all-zero trend, because every
// real event landed on a date the series did not contain.
//
// The window bound compares the raw column against the first day's midnight
// rather than DATE(e.created_at) against a date: the same days, but a bound
// on the column lets Dolt skip the events outside the window instead of
// computing DATE() for every event, 0.4s instead of 4.6s on 32,000 events.
func trendFor(ctx context.Context, db *sql.DB, days int) ([]TrendPoint, error) {
	if days <= 0 {
		days = TrendDays
	}

	// CURDATE() comes back as a DATE, which the driver hands over as bytes
	// rather than a time.Time even with parseTime=true.
	var todayRaw string
	if err := db.QueryRowContext(ctx, "SELECT CAST(CURDATE() AS CHAR)").Scan(&todayRaw); err != nil {
		return nil, fmt.Errorf("stats trend: read database date: %w", err)
	}
	dbToday, err := time.Parse("2006-01-02", todayRaw)
	if err != nil {
		return nil, fmt.Errorf("stats trend: parse database date %q: %w", todayRaw, err)
	}

	rows, err := db.QueryContext(ctx, `
SELECT CAST(DATE(e.created_at) AS CHAR) AS day,
       SUM(CASE WHEN e.event_type = 'created' THEN 1 ELSE 0 END) AS opened,
       SUM(CASE WHEN e.event_type = 'closed'  THEN 1 ELSE 0 END) AS closed
FROM events e
JOIN issues i ON i.id = e.issue_id
WHERE `+VisiblePredicate("i")+`
  AND e.event_type IN ('created', 'closed')
  AND e.created_at >= CURDATE() - INTERVAL ? DAY
GROUP BY DATE(e.created_at)`, days-1)
	if err != nil {
		return nil, fmt.Errorf("stats trend: %w", err)
	}
	defer rows.Close()

	byDay := map[string]trendCounts{}
	for rows.Next() {
		var (
			day            string
			opened, closed sql.NullInt64
		)
		if err := rows.Scan(&day, &opened, &closed); err != nil {
			return nil, fmt.Errorf("scan trend row: %w", err)
		}
		// The day arrives as the database's own YYYY-MM-DD text: DATE(...)
		// carries no zone, so the key is the day the database grouped by, and
		// the text form scans the same from the server and embedded drivers
		// (the embedded one hands DATE back as a string, not a time).
		byDay[day] = trendCounts{int(opened.Int64), int(closed.Int64)}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats trend: %w", err)
	}

	return trendSeries(dbToday, days, byDay), nil
}

// trendCounts is one day's tally, keyed by day in trendSeries.
type trendCounts struct{ opened, closed int }

// trendSeries lays the window out as one point per day, oldest first, ending on
// anchor. Days with no activity are emitted as zeros so the UI can plot without
// filling gaps itself, and a bucket outside the window is dropped rather than
// stretching the series — a day the window does not contain is a day the trend
// does not claim to describe.
func trendSeries(anchor time.Time, days int, byDay map[string]trendCounts) []TrendPoint {
	out := make([]TrendPoint, 0, days)
	start := anchor.AddDate(0, 0, -(days - 1))
	for i := 0; i < days; i++ {
		key := start.AddDate(0, 0, i).Format("2006-01-02")
		c := byDay[key]
		out = append(out, TrendPoint{Date: key, Opened: c.opened, Closed: c.closed})
	}
	return out
}

// StaleFor returns open or in-progress issues untouched for at least days.
//
// Closed and deferred work is excluded by design: an issue nobody has touched
// because it is finished or deliberately parked is not stale, it is done or
// waiting.
func StaleFor(ctx context.Context, db *sql.DB, days int, now time.Time) ([]StaleIssue, error) {
	list, err := StaleCandidates(ctx, db)
	if err != nil {
		return nil, err
	}
	return StaleFrom(list, days, now), nil
}

// StaleCandidates reads the issues staleness is judged over: every open or
// in-progress issue, least recently updated first. It does not depend on the
// clock, so a caller may keep it for as long as the data has not changed.
func StaleCandidates(ctx context.Context, db *sql.DB) (*IssueList, error) {
	return ListIssues(ctx, db, Filter{
		Statuses: []string{StatusOpen, StatusInProgress},
		Limit:    MaxLimit,
		Sort:     SortUpdated,
	})
}

// StaleFrom keeps the candidates untouched for at least days as of now,
// stalest first. It builds a new slice and leaves list unchanged.
func StaleFrom(list *IssueList, days int, now time.Time) []StaleIssue {
	if days <= 0 {
		days = DefaultStaleDays
	}
	cutoff := now.UTC().AddDate(0, 0, -days)
	out := []StaleIssue{}
	for _, iss := range list.Issues {
		if iss.Updated.After(cutoff) {
			continue
		}
		out = append(out, StaleIssue{
			Issue:     iss,
			StaleDays: int(now.UTC().Sub(iss.Updated).Hours() / 24),
		})
	}
	// Stalest first: the thing that has been ignored longest leads.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.Before(out[j].Updated) })
	return out
}

// DefaultStaleDays is the staleness threshold when the caller does not choose.
const DefaultStaleDays = 14

// MaxStaleDays bounds the ?days= parameter.
const MaxStaleDays = 3650
