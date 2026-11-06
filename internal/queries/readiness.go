package queries

// Why an open issue is not ready. Surfaced per-issue as Issue.NotReady so the
// UI can say "deferred until …" instead of a red "blocked by ?" chip.
const (
	// NotReadyDeps means an unresolved `blocks` dependency is holding the issue,
	// directly or through a blocked ancestor.
	NotReadyDeps = "deps"
	// NotReadyDeferred means the issue is only unready because it — or a parent
	// — is defer-scheduled into the future.
	NotReadyDeferred = "deferred"
)

// blockedTransitiveCTE reproduces the `ready_issues` view's own blocking logic.
//
// It is copied from the view's DDL rather than approximated, because the two
// must agree exactly: readiness comes from the view, and this is what explains
// *why* something is missing from it. Direct `blocks` deps propagate down
// parent-child edges, which is why a plain "has an unresolved blocks dep" test
// is not enough.
const blockedTransitiveCTE = `
blocked_directly AS (
  SELECT DISTINCT d.issue_id
  FROM dependencies d
  WHERE d.type = 'blocks'
    AND EXISTS (
      SELECT 1 FROM issues blocker
      WHERE blocker.id = d.depends_on_issue_id
        AND blocker.status NOT IN ('closed', 'pinned')
    )
),
blocked_transitively AS (
  SELECT issue_id, 0 AS depth FROM blocked_directly
  UNION ALL
  SELECT d.issue_id, bt.depth + 1
  FROM blocked_transitively bt
  JOIN dependencies d ON d.depends_on_issue_id = bt.issue_id
  WHERE d.type = 'parent-child' AND bt.depth < 50
)`

// deferredByParentTable lists the ids of issues whose parent is
// defer-scheduled. It is a derived table joined once (deferParentJoin) rather
// than a correlated EXISTS, because Dolt runs the EXISTS form per row: 18s
// against a 4,000-issue backlog, where the join costs under half a second.
const deferredByParentTable = `(
  SELECT DISTINCT dp.issue_id
  FROM dependencies dp
  JOIN issues par ON par.id = dp.depends_on_issue_id
  WHERE dp.type = 'parent-child'
    AND par.defer_until IS NOT NULL
    AND par.defer_until > UTC_TIMESTAMP()
)`

// deferParentJoin is the LEFT JOIN that deferScheduledExpr and notReadyExpr
// read through deferParentAlias: a row there means a parent is
// defer-scheduled.
func deferParentJoin(alias, deferParentAlias string) string {
	return "LEFT JOIN " + deferredByParentTable + " " + deferParentAlias +
		" ON " + deferParentAlias + ".issue_id = " + alias + ".id"
}

// deferScheduledExpr is true when an issue is held back purely by
// scheduling: its own defer_until is in the future, or a parent's is. The
// statement must carry deferParentJoin(alias, deferParentAlias).
//
// Verified live against bd 1.1.2: `bd update <id> --defer <date>` moves the
// issue's own status to `deferred`, so the *direct* case cannot appear among
// open issues. The parent case can and does — a child of a deferred epic stays
// `open`, drops out of ready_issues, and has zero blocking deps. That child is
// what this expression exists to stop counting as dependency-blocked.
func deferScheduledExpr(alias, deferParentAlias string) string {
	return `(
  (` + alias + `.defer_until IS NOT NULL AND ` + alias + `.defer_until > UTC_TIMESTAMP())
  OR ` + deferParentAlias + `.issue_id IS NOT NULL
)`
}

// notReadyExpr renders a SQL expression yielding 'deps', 'deferred' or NULL.
// The statement must carry deferParentJoin(alias, deferParentAlias).
//
// Precedence is deliberate: an issue that is both dep-blocked and
// defer-scheduled reports "deps", because unblocking it is the action that
// matters and the schedule would resolve itself.
func notReadyExpr(alias, readyAlias, blockedAlias, deferParentAlias string) string {
	return `CASE
    WHEN ` + readyAlias + `.id IS NOT NULL THEN NULL
    WHEN ` + alias + `.status <> '` + StatusOpen + `' THEN NULL
    WHEN ` + blockedAlias + `.issue_id IS NOT NULL THEN '` + NotReadyDeps + `'
    WHEN ` + deferScheduledExpr(alias, deferParentAlias) + ` THEN '` + NotReadyDeferred + `'
    ELSE NULL
  END`
}
