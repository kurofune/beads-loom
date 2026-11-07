package queries

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// InitiativeRollup counts an epic's full parent-child subtree — every
// transitive descendant, not just direct children.
type InitiativeRollup struct {
	Total  int `json:"total"`
	Closed int `json:"closed"`
	// Started counts descendants whose work is under way. Before it existed an
	// in_progress descendant landed in Total and no other bucket at all: it is
	// absent from ready_issues, and notReadyExpr yields NULL for any status
	// other than open, so neither DepBlocked nor DeferScheduled claimed it.
	Started        int `json:"started"`
	Ready          int `json:"ready"`
	DepBlocked     int `json:"depBlocked"`
	DeferScheduled int `json:"deferScheduled"`
	// Chained counts descendants on either end of a blocks dependency, whatever
	// the other end's status. It is a shape count, not a bucket: it overlaps
	// every bucket above, and it is wider than DepBlocked because it keeps the
	// blockers themselves and the pairs whose blocker has closed. A low Chained
	// against a high Total says the epic is a pile, not a chain.
	Chained int `json:"chained"`
}

// NoReceipt counts closed descendants missing their promised-vs-landed
// receipts. Total counts a descendant missing either field once, so it is not
// the sum of NoAcceptance and NoCloseReason when one bead is missing both.
type NoReceipt struct {
	Total         int `json:"total"`
	NoAcceptance  int `json:"noAcceptance"`
	NoCloseReason int `json:"noCloseReason"`
}

// Initiative is one epic row on the Initiatives view.
type Initiative struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Status   string     `json:"status"`
	Priority int        `json:"priority"`
	Assignee string     `json:"assignee"`
	Due      *time.Time `json:"due"`
	Updated  time.Time  `json:"updated"`
	// LastActivity is MAX(updated) across the epic AND its whole subtree.
	LastActivity time.Time        `json:"lastActivity"`
	Subtree      InitiativeRollup `json:"subtree"`
	// CloseEligible requires an open epic with at least one transitive descendant
	// and every transitive descendant closed. This is deliberately stricter than
	// bd's direct-children rule, which does not inspect the full subtree or the
	// epic's own status. Computed here rather than on each client so the board
	// nudge and the Initiatives view cannot drift on what "ready to close" means.
	CloseEligible bool             `json:"closeEligible"`
	Health        InitiativeHealth `json:"health"`
	// NeedsTarget is the missing-target nudge's firing condition: an open epic
	// with descendants, no due date, and no NoTargetLabel opt-out. Narrower
	// than Health.State == "no-target" on purpose — see NeedsTarget.
	NeedsTarget bool      `json:"needsTarget"`
	NoReceipt   NoReceipt `json:"noReceipt"`
}

// InitiativesFor returns the epic rows used by the Initiatives view in one
// statement. Every rollup covers the same visible, transitive descendant set.
func InitiativesFor(ctx context.Context, db *sql.DB, includeClosed bool) ([]Initiative, error) {
	now := time.Now().UTC()
	return initiativesFor(ctx, db, includeClosed, nil, now)
}

// InitiativeFor returns one epic using the same transitive rollup and health
// classifier as InitiativesFor.
func InitiativeFor(ctx context.Context, db *sql.DB, id string) (*Initiative, error) {
	now := time.Now().UTC()
	initiatives, err := initiativesFor(ctx, db, true, &id, now)
	if err != nil {
		return nil, err
	}
	if len(initiatives) == 0 {
		return nil, fmt.Errorf("%w: %s is not an epic", ErrNotFound, id)
	}
	return &initiatives[0], nil
}

func initiativesFor(
	ctx context.Context,
	db *sql.DB,
	includeClosed bool,
	rootID *string,
	now time.Time,
) ([]Initiative, error) {
	closedFilter := ""
	if !includeClosed {
		closedFilter = " AND i.status <> '" + StatusClosed + "'"
	}
	rootFilter := ""
	args := make([]any, 0, 2)
	if rootID != nil {
		rootFilter = " AND i.id = ?"
		args = append(args, *rootID)
	}
	args = append(args, now.Add(-HealthWindowDays*24*time.Hour))

	query := fmt.Sprintf(`
WITH RECURSIVE %s,
epics AS (
  SELECT i.id
  FROM issues i
  WHERE i.issue_type = 'epic' AND %s%s%s
),
subtree AS (
  SELECT e.id AS root, e.id AS node, 0 AS depth
  FROM epics e
  UNION ALL
  SELECT s.root, d.issue_id, s.depth + 1
  FROM subtree s
  JOIN dependencies d
    ON d.depends_on_issue_id = s.node AND d.type = 'parent-child'
  WHERE s.depth < 50
),
descendants AS (
  SELECT DISTINCT root, node
  FROM subtree
  WHERE depth > 0
)
SELECT e.id, e.title, e.status, e.priority, COALESCE(e.assignee, ''),
       e.due_at, e.updated_at,
       GREATEST(e.updated_at, COALESCE(MAX(c.updated_at), e.updated_at)) AS last_activity,
       COUNT(c.id) AS total,
       SUM(CASE WHEN c.status = 'closed' THEN 1 ELSE 0 END) AS closed,
       SUM(CASE WHEN c.status = 'closed' AND c.closed_at IS NOT NULL AND c.closed_at >= ? THEN 1 ELSE 0 END) AS closed_in_window,
       SUM(CASE WHEN %s THEN 1 ELSE 0 END) AS started,
       SUM(CASE WHEN r.id IS NOT NULL THEN 1 ELSE 0 END) AS ready,
       SUM(CASE WHEN %s = 'deps' OR c.status = '%s' THEN 1 ELSE 0 END) AS dep_blocked,
       SUM(CASE WHEN %s = 'deps' THEN 0
                WHEN c.status = 'deferred' OR %s = 'deferred' THEN 1
                ELSE 0 END) AS defer_scheduled,
       SUM(CASE WHEN %s THEN 1 ELSE 0 END) AS no_receipt,
       SUM(CASE WHEN %s THEN 1 ELSE 0 END) AS no_acceptance,
       SUM(CASE WHEN %s THEN 1 ELSE 0 END) AS no_close_reason,
       (SELECT l.label FROM labels l WHERE l.issue_id = e.id AND l.label = '%s' LIMIT 1) AS optout_label,
       SUM(CASE WHEN ch.id IS NOT NULL THEN 1 ELSE 0 END) AS chained
FROM issues e
JOIN epics ON epics.id = e.id
LEFT JOIN descendants dsc ON dsc.root = e.id
LEFT JOIN issues c ON c.id = dsc.node AND c.status <> '%s' AND %s
LEFT JOIN ready_issues r ON r.id = c.id
LEFT JOIN (SELECT DISTINCT issue_id FROM blocked_transitively) bt ON bt.issue_id = c.id
LEFT JOIN %s ch ON ch.id = c.id
%s
GROUP BY e.id, e.title, e.status, e.priority, e.assignee, e.due_at, e.updated_at
ORDER BY e.id`,
		blockedTransitiveCTE,
		VisiblePredicate("i"),
		closedFilter,
		rootFilter,
		startedPredicate("c"),
		notReadyExpr("c", "r", "bt", "dfp"),
		// blocked is a STATUS bucket, unioned into dep_blocked exactly the way
		// status='deferred' is unioned into defer_scheduled below. It is not
		// routed through readiness: notReadyExpr short-circuits to NULL for any
		// status other than open, so before this arm a status=blocked
		// descendant was counted in total and in no bucket at all — the same
		// silent hole loom-g1v.1 closed for in_progress/hooked. One CASE with
		// one THEN, so a descendant that is both dep-blocked and status=blocked
		// still counts once.
		StatusBlocked,
		notReadyExpr("c", "r", "bt", "dfp"),
		notReadyExpr("c", "r", "bt", "dfp"),
		noReceiptPredicate("c"),
		missingAcceptanceReceiptPredicate("c"),
		missingCloseReasonReceiptPredicate("c"),
		// A package constant, not user input, and interpolated for the same
		// reason StatusClosed is above: keeping it out of the bind list means
		// the placeholder order stays "optional root id, then close cutoff".
		NoTargetLabel,
		// pinned leaves the descendant set entirely rather than gaining a
		// bucket. A pinned bead is a standing reference note that bd classes
		// with closed and the board hides outright (web/src/lib/board.test.ts),
		// so counting it as open work would inflate every epic's denominator
		// and dilute the headline closed/total ratio.
		StatusPinned,
		VisiblePredicate("c"),
		chainedTable(),
		deferParentJoin("c", "dfp"),
	)

	// SQL placeholders bind in textual order: an optional root id appears in
	// the epics CTE before the close-window cutoff in the SELECT. Build both the
	// statement and its arguments together above so those positions cannot drift.
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list initiatives: %w", err)
	}
	defer rows.Close()

	out := []Initiative{}
	for rows.Next() {
		var (
			initiative                                                                                                     Initiative
			due                                                                                                            sql.NullTime
			total, closed, closedInWindow, started, ready, blocked, deferScheduled, noReceipt, noAcceptance, noCloseReason sql.NullInt64
			optOutLabel                                                                                                    sql.NullString
			chained                                                                                                        sql.NullInt64
		)
		if err := rows.Scan(
			&initiative.ID,
			&initiative.Title,
			&initiative.Status,
			&initiative.Priority,
			&initiative.Assignee,
			&due,
			&initiative.Updated,
			&initiative.LastActivity,
			&total,
			&closed,
			&closedInWindow,
			&started,
			&ready,
			&blocked,
			&deferScheduled,
			&noReceipt,
			&noAcceptance,
			&noCloseReason,
			&optOutLabel,
			&chained,
		); err != nil {
			return nil, fmt.Errorf("scan initiative: %w", err)
		}

		initiative.Due = nullTime(due)
		initiative.Updated = utc(initiative.Updated)
		initiative.LastActivity = utc(initiative.LastActivity)
		initiative.Subtree = InitiativeRollup{
			Total:          int(total.Int64),
			Closed:         int(closed.Int64),
			Started:        int(started.Int64),
			Ready:          int(ready.Int64),
			DepBlocked:     int(blocked.Int64),
			DeferScheduled: int(deferScheduled.Int64),
			Chained:        int(chained.Int64),
		}
		initiative.NoReceipt = NoReceipt{
			Total:         int(noReceipt.Int64),
			NoAcceptance:  int(noAcceptance.Int64),
			NoCloseReason: int(noCloseReason.Int64),
		}
		initiative.CloseEligible = initiative.Status != StatusClosed &&
			initiative.Subtree.Total > 0 &&
			initiative.Subtree.Closed == initiative.Subtree.Total
		initiative.Health = ClassifyHealth(HealthInput{
			Total:          initiative.Subtree.Total,
			Closed:         initiative.Subtree.Closed,
			ClosedInWindow: int(closedInWindow.Int64),
			Due:            initiative.Due,
			Now:            now,
		})
		// The statement returns only the opt-out label itself (NULL when the
		// epic does not carry it) rather than the whole label set, so the
		// rollup does not fan out across a labels join. NeedsTarget still owns
		// which label means "deliberately not time-boxed".
		var labels []string
		if optOutLabel.Valid {
			labels = []string{optOutLabel.String}
		}
		initiative.NeedsTarget = NeedsTarget(TargetInput{
			Status: initiative.Status,
			Total:  initiative.Subtree.Total,
			Due:    initiative.Due,
			Labels: labels,
		})
		out = append(out, initiative)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list initiatives: %w", err)
	}
	return out, nil
}

// chainedTable lists the ids on either end of a blocks dependency whose other
// end is a visible issue. It ignores the other end's status on purpose:
// Chained describes the epic's shape, so a pair whose blocker has closed
// still counts, and so does a pair whose other end is pinned — the blockers
// graph draws that pinned neighbour, so the count must agree with the drill.
// A pinned descendant itself still leaves the rollup through the descendant
// join, not here. A derived table joined once rather than a correlated EXISTS,
// for the reason deferredByParentTable gives. UNION, not UNION ALL, so an id
// on several edges joins once and the descendant row does not fan out.
func chainedTable() string {
	return `(
  SELECT d.issue_id AS id
  FROM dependencies d
  JOIN issues blocker ON blocker.id = d.depends_on_issue_id
  WHERE d.type = 'blocks' AND ` + VisiblePredicate("blocker") + `
  UNION
  SELECT d.depends_on_issue_id AS id
  FROM dependencies d
  JOIN issues blocked ON blocked.id = d.issue_id
  WHERE d.type = 'blocks' AND ` + VisiblePredicate("blocked") + `
)`
}
