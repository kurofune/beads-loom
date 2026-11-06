package queries

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// listSelect is the single statement behind the backlog.
//
// Everything a card needs comes back in one round trip:
//   - ready       — LEFT JOIN the ready_issues view (never a per-row query)
//   - blocked_by  — grouped count of unresolved `blocks` deps on this issue
//   - blocks      — grouped count of issues depending on this one
//   - parent      — the parent-child dep where this issue is the child
//   - child stats — total/closed rollup for epics
//
// The grouped subselects are aggregated before the join, so no join multiplies
// rows. The format slots inject CTEs, the optional epic-scope join, filtering,
// and ordering.
//
// The child-stats subselect excludes status=pinned direct children the same way
// InitiativesFor excludes pinned descendants from its subtree — a pinned bead is
// a standing reference note bd classes with closed and the board hides outright
// (web/src/lib/board.test.ts), so counting one inflates the epic's denominator.
// Excluding it in only one of the two places is worse than excluding it in
// neither: the backlog row and the Initiatives view would then print different
// closed/total ratios for the same epic. Detail's twin subselect below carries
// the identical arm for the same reason.
const listSelect = `
WITH RECURSIVE %s
SELECT
  i.id, i.title, i.issue_type, i.status, i.priority,
  COALESCE(i.assignee, ''), i.estimated_minutes,
  i.created_at, i.updated_at, i.closed_at, i.due_at, i.defer_until,
  COALESCE(i.pinned, 0),
  (r.id IS NOT NULL) AS ready,
  %s AS not_ready,
  COALESCE(bb.n, 0) AS blocked_by,
  COALESCE(bl.n, 0) AS blocks,
  p.id, p.title,
  cs.total, cs.closed, cs.started,
  %s AS missing_acceptance,
  %s AS missing_close_reason
FROM issues i
%s
LEFT JOIN ready_issues r ON r.id = i.id
LEFT JOIN (SELECT DISTINCT issue_id FROM blocked_transitively) bt ON bt.issue_id = i.id
` + "LEFT JOIN " + deferredByParentTable + " dfp ON dfp.issue_id = i.id" + `
LEFT JOIN (
  SELECT d.issue_id AS id, COUNT(*) AS n
  FROM dependencies d
  JOIN issues b ON b.id = d.depends_on_issue_id
  WHERE d.type = 'blocks' AND b.status NOT IN ('closed', 'pinned')
  GROUP BY d.issue_id
) bb ON bb.id = i.id
LEFT JOIN (
  SELECT d.depends_on_issue_id AS id, COUNT(*) AS n
  FROM dependencies d
  WHERE d.type = 'blocks' AND d.depends_on_issue_id IS NOT NULL
  GROUP BY d.depends_on_issue_id
) bl ON bl.id = i.id
LEFT JOIN (
  SELECT d.issue_id AS id, MIN(d.depends_on_issue_id) AS parent_id
  FROM dependencies d
  WHERE d.type = 'parent-child' AND d.depends_on_issue_id IS NOT NULL
  GROUP BY d.issue_id
) pd ON pd.id = i.id
LEFT JOIN issues p ON p.id = pd.parent_id
LEFT JOIN (
  SELECT d.depends_on_issue_id AS id,
         COUNT(*) AS total,
         SUM(CASE WHEN c.status = 'closed' THEN 1 ELSE 0 END) AS closed,
         SUM(CASE WHEN %s THEN 1 ELSE 0 END) AS started
  FROM dependencies d
  JOIN issues c ON c.id = d.issue_id
  WHERE d.type = 'parent-child' AND c.status <> '%s' AND %s
  GROUP BY d.depends_on_issue_id
) cs ON cs.id = i.id
WHERE %s
ORDER BY %s
LIMIT ? OFFSET ?`

const countSelect = `%sSELECT COUNT(*) FROM issues i
%s
WHERE %s`

// ListIssues returns one page of the backlog plus the total matching the
// filter. Labels are hydrated by a second query rather than GROUP_CONCAT,
// which silently truncates past group_concat_max_len.
func ListIssues(ctx context.Context, db *sql.DB, f Filter) (*IssueList, error) {
	ctes := blockedTransitiveCTE
	scopeJoin := ""
	var scopeArgs []any
	if f.Epic != "" {
		ctes = subtreeDefinition + ",\n" + blockedTransitiveCTE
		scopeJoin = "JOIN (SELECT DISTINCT id FROM subtree WHERE depth > 0 AND id <> root) epic_scope ON epic_scope.id = i.id"
		scopeArgs = []any{f.Epic}
	}

	where, args := BuildWhere(f)
	limit, offset := f.Page()

	countPrefix := ""
	if f.Epic != "" {
		countPrefix = "WITH RECURSIVE " + subtreeDefinition + "\n"
	}
	countArgs := append(append([]any{}, scopeArgs...), args...)
	var total int
	if err := db.QueryRowContext(ctx, fmt.Sprintf(countSelect, countPrefix, scopeJoin, where), countArgs...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count issues: %w", err)
	}
	if f.Epic != "" && total == 0 {
		return &IssueList{Issues: []Issue{}, Total: 0}, nil
	}

	// Slot order tracks the statement, not the reading order: ctes, not_ready,
	// the two receipt classifiers, the epic-scope join, the child-stats started
	// SUM, the child-stats pinned exclusion, the child-stats visibility filter,
	// the WHERE clause, then ORDER BY.
	query := fmt.Sprintf(listSelect,
		ctes,
		notReadyExpr("i", "r", "bt", "dfp"),
		missingAcceptanceReceiptPredicate("i"),
		missingCloseReasonReceiptPredicate("i"),
		scopeJoin,
		startedPredicate("c"),
		StatusPinned,
		VisiblePredicate("c"), where, OrderBy(f.Sort))
	pageArgs := append(append(append([]any{}, scopeArgs...), args...), limit, offset)

	rows, err := db.QueryContext(ctx, query, pageArgs...)
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	defer rows.Close()

	issues := make([]Issue, 0, limit)
	for rows.Next() {
		var (
			iss                       Issue
			estimate                  sql.NullInt64
			closed, due, deferUntil   sql.NullTime
			pinned                    int
			ready                     bool
			notReady                  sql.NullString
			parentID, parentTitle     sql.NullString
			childTotal, childClosed   sql.NullInt64
			childStarted              sql.NullInt64
			missingAcceptance         sql.NullBool
			missingCloseReason        sql.NullBool
			blockedByCnt, blocksCount int
		)
		if err := rows.Scan(
			&iss.ID, &iss.Title, &iss.Type, &iss.Status, &iss.Priority,
			&iss.Assignee, &estimate,
			&iss.Created, &iss.Updated, &closed, &due, &deferUntil,
			&pinned, &ready, &notReady, &blockedByCnt, &blocksCount,
			&parentID, &parentTitle, &childTotal, &childClosed, &childStarted,
			&missingAcceptance, &missingCloseReason,
		); err != nil {
			return nil, fmt.Errorf("scan issue: %w", err)
		}

		iss.Created = utc(iss.Created)
		iss.Updated = utc(iss.Updated)
		iss.Closed = nullTime(closed)
		iss.Due = nullTime(due)
		iss.DeferUntil = nullTime(deferUntil)
		iss.Estimate = nullInt(estimate)
		iss.Pinned = pinned != 0
		iss.Ready = ready
		iss.MissingAcceptance = missingAcceptance.Valid && missingAcceptance.Bool
		iss.MissingCloseReason = missingCloseReason.Valid && missingCloseReason.Bool
		if notReady.Valid && notReady.String != "" {
			v := notReady.String
			iss.NotReady = &v
		}
		iss.DepCounts = DepCounts{BlockedBy: blockedByCnt, Blocks: blocksCount}
		iss.Labels = []string{}
		if parentID.Valid {
			iss.Parent = &ParentRef{ID: parentID.String, Title: parentTitle.String}
		}
		if childTotal.Valid && childTotal.Int64 > 0 {
			iss.ChildStats = &ChildStats{
				Total:   int(childTotal.Int64),
				Closed:  int(childClosed.Int64),
				Started: int(childStarted.Int64),
			}
		}
		issues = append(issues, iss)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}

	if err := hydrateLabels(ctx, db, issues); err != nil {
		return nil, err
	}
	return &IssueList{Issues: issues, Total: total}, nil
}

// hydrateLabels fills Labels on every issue with one query.
func hydrateLabels(ctx context.Context, db *sql.DB, issues []Issue) error {
	if len(issues) == 0 {
		return nil
	}
	ids := make([]any, 0, len(issues))
	index := make(map[string]*Issue, len(issues))
	for i := range issues {
		ids = append(ids, issues[i].ID)
		index[issues[i].ID] = &issues[i]
	}

	q := "SELECT issue_id, label FROM labels WHERE issue_id IN (" +
		placeholders(len(ids)) + ") ORDER BY issue_id, label"
	rows, err := db.QueryContext(ctx, q, ids...)
	if err != nil {
		return fmt.Errorf("load labels: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, label string
		if err := rows.Scan(&id, &label); err != nil {
			return fmt.Errorf("scan label: %w", err)
		}
		if iss, ok := index[id]; ok {
			iss.Labels = append(iss.Labels, label)
		}
	}
	return rows.Err()
}

// detailSelect pulls one issue with its prose fields and readiness flag.
const detailSelect = `
WITH RECURSIVE %s
SELECT
  i.id, i.title, i.issue_type, i.status, i.priority,
  COALESCE(i.assignee, ''), i.estimated_minutes,
  i.created_at, i.updated_at, i.closed_at, i.due_at, i.defer_until,
  COALESCE(i.pinned, 0),
  (r.id IS NOT NULL) AS ready,
  %s AS not_ready,
  COALESCE(bb.n, 0), COALESCE(bl.n, 0),
  p.id, p.title,
  cs.total, cs.closed, cs.started,
  i.description, i.acceptance_criteria, i.design, i.notes,
  COALESCE(i.close_reason, ''), COALESCE(i.owner, '')
FROM issues i
LEFT JOIN ready_issues r ON r.id = i.id
LEFT JOIN (SELECT DISTINCT issue_id FROM blocked_transitively) bt ON bt.issue_id = i.id
` + "LEFT JOIN " + deferredByParentTable + " dfp ON dfp.issue_id = i.id" + `
LEFT JOIN (
  SELECT d.issue_id AS id, COUNT(*) AS n
  FROM dependencies d
  JOIN issues b ON b.id = d.depends_on_issue_id
  WHERE d.type = 'blocks' AND b.status NOT IN ('closed', 'pinned')
  GROUP BY d.issue_id
) bb ON bb.id = i.id
LEFT JOIN (
  SELECT d.depends_on_issue_id AS id, COUNT(*) AS n
  FROM dependencies d
  WHERE d.type = 'blocks' AND d.depends_on_issue_id IS NOT NULL
  GROUP BY d.depends_on_issue_id
) bl ON bl.id = i.id
LEFT JOIN (
  SELECT d.issue_id AS id, MIN(d.depends_on_issue_id) AS parent_id
  FROM dependencies d
  WHERE d.type = 'parent-child' AND d.depends_on_issue_id IS NOT NULL
  GROUP BY d.issue_id
) pd ON pd.id = i.id
LEFT JOIN issues p ON p.id = pd.parent_id
LEFT JOIN (
  SELECT d.depends_on_issue_id AS id,
         COUNT(*) AS total,
         SUM(CASE WHEN c.status = 'closed' THEN 1 ELSE 0 END) AS closed,
         SUM(CASE WHEN %s THEN 1 ELSE 0 END) AS started
  FROM dependencies d
  JOIN issues c ON c.id = d.issue_id
  WHERE d.type = 'parent-child' AND c.status <> '%s' AND %s
  GROUP BY d.depends_on_issue_id
) cs ON cs.id = i.id
WHERE i.id = ?`

// depsSelect returns every related issue in one statement, tagged by kind.
//
// Direction reminder: dependencies.issue_id depends on depends_on_issue_id, so
// for parent-child the CHILD is issue_id and the PARENT is depends_on_issue_id.
//
// Infra beads (gate/agent/role/message/event, ephemeral, template) are excluded
// from every kind — including blockedBy.
//
// That choice needs stating, because the alternative is defensible: an infra
// gate bead genuinely does hold readiness, so hiding it hides a real cause. It
// is excluded anyway for two reasons. First, consistency: the epic children
// rollup (childStats) already filters, so keeping them here made an epic's
// child count and child list disagree — the reviewer's N6 point. Second,
// naming: a gate bead's title is machine coordination text that means nothing
// to a manager, so listing it explains less than the readiness flag already
// does. depCounts.blockedBy is unfiltered, so the count still reflects reality
// and a discrepancy between count and list is the signal that infra is
// involved.
//
// The issue's own rows live in %[1]s (`dependencies` for an issue,
// `wisp_dependencies` for a wisp); rows that point at it live in
// `dependencies`, keyed by %[2]s (`depends_on_issue_id` for an issue,
// `depends_on_wisp_id` for a wisp).
const depsSelect = `
SELECT kind, t.id, t.title, t.status, t.priority, (r.id IS NOT NULL) AS ready
FROM (
  SELECT 'blockedBy' AS kind, d.depends_on_issue_id AS target
  FROM %[1]s d WHERE d.issue_id = ? AND d.type = 'blocks'
  UNION ALL
  SELECT 'blocks', d.issue_id
  FROM dependencies d WHERE d.%[2]s = ? AND d.type = 'blocks'
  UNION ALL
  SELECT 'children', d.issue_id
  FROM dependencies d WHERE d.%[2]s = ? AND d.type = 'parent-child'
  UNION ALL
  SELECT 'parent', d.depends_on_issue_id
  FROM %[1]s d WHERE d.issue_id = ? AND d.type = 'parent-child'
  UNION ALL
  SELECT 'discoveredFrom', d.depends_on_issue_id
  FROM %[1]s d WHERE d.issue_id = ? AND d.type = 'discovered-from'
) rel
JOIN issues t ON t.id = rel.target
LEFT JOIN ready_issues r ON r.id = t.id
WHERE %[3]s
ORDER BY kind, t.priority ASC, t.id ASC`

// ErrNotFound is returned by GetIssue when no such issue exists.
var ErrNotFound = fmt.Errorf("issue not found")

// GetIssue returns one issue with prose, typed dependency lists, comments and
// history. Non-epics use five round trips; epics use a sixth for the shared
// transitive initiative rollup and health classification.
func GetIssue(ctx context.Context, db *sql.DB, id string) (*IssueDetail, error) {
	var (
		d                        IssueDetail
		estimate                 sql.NullInt64
		closed, due, deferUntil  sql.NullTime
		pinned                   int
		ready                    bool
		notReady                 sql.NullString
		blockedByCnt, blocksCnt  int
		parentID, parentTitle    sql.NullString
		childTotal, childClosed  sql.NullInt64
		childStarted             sql.NullInt64
		desc, accept, design, nt sql.NullString
	)

	row := db.QueryRowContext(ctx, fmt.Sprintf(detailSelect,
		blockedTransitiveCTE, notReadyExpr("i", "r", "bt", "dfp"),
		startedPredicate("c"), StatusPinned, VisiblePredicate("c")), id)
	err := row.Scan(
		&d.ID, &d.Title, &d.Type, &d.Status, &d.Priority,
		&d.Assignee, &estimate,
		&d.Created, &d.Updated, &closed, &due, &deferUntil,
		&pinned, &ready, &notReady, &blockedByCnt, &blocksCnt,
		&parentID, &parentTitle, &childTotal, &childClosed, &childStarted,
		&desc, &accept, &design, &nt,
		&d.CloseReason, &d.Owner,
	)
	if err == sql.ErrNoRows {
		return getWisp(ctx, db, id)
	}
	if err != nil {
		return nil, fmt.Errorf("get issue %s: %w", id, err)
	}

	d.Created = utc(d.Created)
	d.Updated = utc(d.Updated)
	d.Closed = nullTime(closed)
	d.Due = nullTime(due)
	d.DeferUntil = nullTime(deferUntil)
	d.Estimate = nullInt(estimate)
	d.Pinned = pinned != 0
	d.Ready = ready
	if notReady.Valid && notReady.String != "" {
		v := notReady.String
		d.NotReady = &v
	}
	d.DepCounts = DepCounts{BlockedBy: blockedByCnt, Blocks: blocksCnt}
	d.Description = desc.String
	d.AcceptanceCriteria = accept.String
	d.Design = design.String
	d.Notes = nt.String
	d.Labels = []string{}
	if parentID.Valid {
		d.Parent = &ParentRef{ID: parentID.String, Title: parentTitle.String}
	}
	if childTotal.Valid && childTotal.Int64 > 0 {
		d.ChildStats = &ChildStats{
			Total:   int(childTotal.Int64),
			Closed:  int(childClosed.Int64),
			Started: int(childStarted.Int64),
		}
	}
	if d.Type == "epic" {
		initiative, err := InitiativeFor(ctx, db, id)
		if errors.Is(err, ErrNotFound) {
			// Health is an enrichment, so its absence is not the detail's absence;
			// real query failures still fail the request below.
		} else if err != nil {
			return nil, fmt.Errorf("get initiative health for %s: %w", id, err)
		} else {
			d.Health = &initiative.Health
		}
	}

	if d.Deps, err = getDeps(ctx, db, "dependencies", "depends_on_issue_id", id); err != nil {
		return nil, err
	}

	labelRows, err := db.QueryContext(ctx, "SELECT label FROM labels WHERE issue_id = ? ORDER BY label", id)
	if err != nil {
		return nil, fmt.Errorf("get labels for %s: %w", id, err)
	}
	for labelRows.Next() {
		var l string
		if err := labelRows.Scan(&l); err != nil {
			labelRows.Close()
			return nil, fmt.Errorf("scan label for %s: %w", id, err)
		}
		d.Labels = append(d.Labels, l)
	}
	labelRows.Close()
	if err := labelRows.Err(); err != nil {
		return nil, fmt.Errorf("get labels for %s: %w", id, err)
	}

	if d.Comments, err = getComments(ctx, db, "comments", id); err != nil {
		return nil, err
	}
	if d.Events, err = getEvents(ctx, db, "events", id); err != nil {
		return nil, err
	}
	return &d, nil
}

// wispSelect reads one row of bd's `wisps` table, which has the issues
// table's columns. bd 1.3.2 stores every create of an ephemeral type there
// (`message` always, whatever --storage-class says), so a custom type the
// project configured reads back from here, not from `issues`.
//
// A wisp's own links live in `wisp_dependencies`; an issue's link to a wisp
// lives in `dependencies` under depends_on_wisp_id. The counts, parent and
// child rollup follow detailSelect's rules over those rows.
const wispSelect = `
SELECT
  w.id, w.title, w.issue_type, w.status, w.priority,
  COALESCE(w.assignee, ''), w.estimated_minutes,
  w.created_at, w.updated_at, w.closed_at, w.due_at, w.defer_until,
  COALESCE(w.pinned, 0),
  COALESCE(bb.n, 0), COALESCE(bl.n, 0),
  p.id, p.title,
  cs.total, cs.closed, cs.started,
  w.description, w.acceptance_criteria, w.design, w.notes,
  COALESCE(w.close_reason, ''), COALESCE(w.owner, '')
FROM wisps w
LEFT JOIN (
  SELECT d.issue_id AS id, COUNT(*) AS n
  FROM wisp_dependencies d
  JOIN issues b ON b.id = d.depends_on_issue_id
  WHERE d.type = 'blocks' AND b.status NOT IN ('closed', 'pinned')
  GROUP BY d.issue_id
) bb ON bb.id = w.id
LEFT JOIN (
  SELECT d.depends_on_wisp_id AS id, COUNT(*) AS n
  FROM dependencies d
  WHERE d.type = 'blocks' AND d.depends_on_wisp_id IS NOT NULL
  GROUP BY d.depends_on_wisp_id
) bl ON bl.id = w.id
LEFT JOIN (
  SELECT d.issue_id AS id, MIN(d.depends_on_issue_id) AS parent_id
  FROM wisp_dependencies d
  WHERE d.type = 'parent-child' AND d.depends_on_issue_id IS NOT NULL
  GROUP BY d.issue_id
) pd ON pd.id = w.id
LEFT JOIN issues p ON p.id = pd.parent_id
LEFT JOIN (
  SELECT d.depends_on_wisp_id AS id,
         COUNT(*) AS total,
         SUM(CASE WHEN c.status = 'closed' THEN 1 ELSE 0 END) AS closed,
         SUM(CASE WHEN %s THEN 1 ELSE 0 END) AS started
  FROM dependencies d
  JOIN issues c ON c.id = d.issue_id
  WHERE d.type = 'parent-child' AND c.status <> '%s' AND %s
  GROUP BY d.depends_on_wisp_id
) cs ON cs.id = w.id
WHERE w.id = ?`

// getWisp is GetIssue for an id that lives in `wisps`. A wisp is not in
// ready_issues, so it reads back as not ready.
func getWisp(ctx context.Context, db *sql.DB, id string) (*IssueDetail, error) {
	var (
		d                        IssueDetail
		estimate                 sql.NullInt64
		closed, due, deferUntil  sql.NullTime
		pinned                   int
		blockedByCnt, blocksCnt  int
		parentID, parentTitle    sql.NullString
		childTotal, childClosed  sql.NullInt64
		childStarted             sql.NullInt64
		desc, accept, design, nt sql.NullString
	)
	err := db.QueryRowContext(ctx, fmt.Sprintf(wispSelect,
		startedPredicate("c"), StatusPinned, VisiblePredicate("c")), id).Scan(
		&d.ID, &d.Title, &d.Type, &d.Status, &d.Priority,
		&d.Assignee, &estimate,
		&d.Created, &d.Updated, &closed, &due, &deferUntil,
		&pinned, &blockedByCnt, &blocksCnt,
		&parentID, &parentTitle, &childTotal, &childClosed, &childStarted,
		&desc, &accept, &design, &nt,
		&d.CloseReason, &d.Owner,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("get wisp %s: %w", id, err)
	}
	d.Created = utc(d.Created)
	d.Updated = utc(d.Updated)
	d.Closed = nullTime(closed)
	d.Due = nullTime(due)
	d.DeferUntil = nullTime(deferUntil)
	d.Estimate = nullInt(estimate)
	d.Pinned = pinned != 0
	d.DepCounts = DepCounts{BlockedBy: blockedByCnt, Blocks: blocksCnt}
	d.Description = desc.String
	d.AcceptanceCriteria = accept.String
	d.Design = design.String
	d.Notes = nt.String
	if parentID.Valid {
		d.Parent = &ParentRef{ID: parentID.String, Title: parentTitle.String}
	}
	if childTotal.Valid && childTotal.Int64 > 0 {
		d.ChildStats = &ChildStats{
			Total:   int(childTotal.Int64),
			Closed:  int(childClosed.Int64),
			Started: int(childStarted.Int64),
		}
	}
	if d.Deps, err = getDeps(ctx, db, "wisp_dependencies", "depends_on_wisp_id", id); err != nil {
		return nil, err
	}

	labelRows, err := db.QueryContext(ctx, "SELECT label FROM wisp_labels WHERE issue_id = ? ORDER BY label", id)
	if err != nil {
		return nil, fmt.Errorf("get labels for %s: %w", id, err)
	}
	defer labelRows.Close()
	d.Labels = []string{}
	for labelRows.Next() {
		var l string
		if err := labelRows.Scan(&l); err != nil {
			return nil, fmt.Errorf("scan label for %s: %w", id, err)
		}
		d.Labels = append(d.Labels, l)
	}
	if err := labelRows.Err(); err != nil {
		return nil, fmt.Errorf("get labels for %s: %w", id, err)
	}

	if d.Comments, err = getComments(ctx, db, "wisp_comments", id); err != nil {
		return nil, err
	}
	if d.Events, err = getEvents(ctx, db, "wisp_events", id); err != nil {
		return nil, err
	}
	return &d, nil
}

// getDeps reads depsSelect for one issue or wisp; ownTable and inColumn are
// depsSelect's %[1]s and %[2]s.
func getDeps(ctx context.Context, db *sql.DB, ownTable, inColumn, id string) (Deps, error) {
	deps := Deps{
		Blocks:         []IssueRef{},
		BlockedBy:      []IssueRef{},
		Children:       []IssueRef{},
		DiscoveredFrom: []IssueRef{},
	}
	rows, err := db.QueryContext(ctx, fmt.Sprintf(depsSelect, ownTable, inColumn, VisiblePredicate("t")), id, id, id, id, id)
	if err != nil {
		return deps, fmt.Errorf("get deps for %s: %w", id, err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var ref IssueRef
		if err := rows.Scan(&kind, &ref.ID, &ref.Title, &ref.Status, &ref.Priority, &ref.Ready); err != nil {
			return deps, fmt.Errorf("scan dep for %s: %w", id, err)
		}
		switch kind {
		case "blocks":
			deps.Blocks = append(deps.Blocks, ref)
		case "blockedBy":
			deps.BlockedBy = append(deps.BlockedBy, ref)
		case "children":
			deps.Children = append(deps.Children, ref)
		case "discoveredFrom":
			deps.DiscoveredFrom = append(deps.DiscoveredFrom, ref)
		case "parent":
			ref := ref
			deps.Parent = &ref
		}
	}
	if err := rows.Err(); err != nil {
		return deps, fmt.Errorf("get deps for %s: %w", id, err)
	}
	return deps, nil
}

func getComments(ctx context.Context, db *sql.DB, table, id string) ([]Comment, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT id, author, text, created_at FROM "+table+" WHERE issue_id = ? ORDER BY created_at ASC, id ASC", id)
	if err != nil {
		return nil, fmt.Errorf("get comments for %s: %w", id, err)
	}
	defer rows.Close()

	out := []Comment{}
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.Author, &c.Text, &c.Created); err != nil {
			return nil, fmt.Errorf("scan comment for %s: %w", id, err)
		}
		c.Created = utc(c.Created)
		out = append(out, c)
	}
	return out, rows.Err()
}

func getEvents(ctx context.Context, db *sql.DB, table, id string) ([]Event, error) {
	rows, err := db.QueryContext(ctx, `
SELECT event_type, actor, COALESCE(old_value, ''), COALESCE(new_value, ''), COALESCE(comment, ''), created_at
FROM `+table+` WHERE issue_id = ? ORDER BY created_at ASC, id ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("get events for %s: %w", id, err)
	}
	defer rows.Close()

	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Type, &e.Actor, &e.OldValue, &e.NewValue, &e.Comment, &e.Created); err != nil {
			return nil, fmt.Errorf("scan event for %s: %w", id, err)
		}
		e.Created = utc(e.Created)
		out = append(out, e)
	}
	return out, rows.Err()
}

// utc normalizes a driver time to UTC so JSON always renders with a Z suffix.
func utc(t time.Time) time.Time { return t.UTC() }

func nullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

func nullInt(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

// ParseCSV splits a comma-separated query parameter, dropping empty entries.
func ParseCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
