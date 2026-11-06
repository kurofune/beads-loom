import type { Issue, IssueStatus } from '@/api/types'

/**
 * How a list of issues is ordered, for every surface that shows one.
 *
 * Board columns and Backlog groups are the same question asked twice, so the
 * answer lives once: two surfaces disagreeing about what Closed order means is
 * worse than either rule alone — the same backlog would read chronologically on
 * the board and by priority in the list.
 */

/** Last resort for every comparator: without it, rows that tie on every
 *  meaningful key are ordered by whatever `Array.prototype.sort` did with them,
 *  so the order is only as reproducible as the sort is stable. */
export const byId = (a: Issue, b: Issue) => a.id.localeCompare(b.id)

/**
 * How close an issue is to being worked, 0 (already moving) to 4 (not live).
 *
 * Reads status, `ready` and `notReady` and nothing else — readiness is the
 * `ready_issues` view's call, never inferred from dependency counts or dates.
 * Terminal statuses are caught first: the server sends a closed or pinned row
 * with `ready: false` and no reason, which is exactly the legacy blocked
 * reading, so checking readiness first would sort finished work as stuck work.
 */
export type ReadinessTier = 0 | 1 | 2 | 3 | 4

export function readinessTier(issue: Issue): ReadinessTier {
  const { status, ready, notReady } = issue
  if (status === 'closed' || status === 'pinned') return 4
  if (status === 'in_progress' || status === 'hooked') return 0
  if (ready) return 1
  if (status === 'deferred' || notReady === 'deferred') return 3
  if (status === 'blocked' || notReady === 'deps' || notReady == null) return 2
  return 4
}

/**
 * Within a tier, the dependency key that says which bead to take first:
 * workable beads (tiers 0-1) lead with the one that unblocks the most, blocked
 * beads (tier 2) with the one nearest to free. Deferred and other work has no
 * dependency argument to make, so it ties here and falls through to age.
 */
function dependencyRank(issue: Issue, tier: ReadinessTier): number {
  if (tier <= 1) return -issue.depCounts.blocks
  if (tier === 2) return issue.depCounts.blockedBy
  return 0
}

/**
 * Live work answers "what do I do next": urgency leads, then how close the
 * bead is to being workable, then its dependency leverage, then age — the
 * oldest waiting bead first. `updated` is deliberately absent: a note or a
 * label moves it, and "last touched" says nothing about what can be worked.
 */
export const byWorkOrder = (a: Issue, b: Issue) => {
  const ta = readinessTier(a)
  const tb = readinessTier(b)
  return (
    a.priority - b.priority ||
    ta - tb ||
    dependencyRank(a, ta) - dependencyRank(b, tb) ||
    Date.parse(a.created) - Date.parse(b.created) ||
    byId(a, b)
  )
}

/** Milliseconds, or null when the issue carries no usable finish time. */
function closedAt(issue: Issue): number | null {
  if (!issue.closed) return null
  const t = Date.parse(issue.closed)
  return Number.isNaN(t) ? null : t
}

/**
 * Closed is a log, and logs read chronologically — on finished work there is no
 * "next", so priority has nothing left to say. Order on `closed`, never
 * `updated`: a note, a label, or a reopen-then-reclose moves `updated` for
 * reasons unrelated to finishing. An issue with no usable finish time has no
 * place in the chronology at all, so it sinks rather than leading the list.
 */
export const byCloseTimeDescending = (a: Issue, b: Issue) => {
  const x = closedAt(a)
  const y = closedAt(b)
  if (x === null || y === null) {
    if (x !== y) return x === null ? 1 : -1
  } else if (x !== y) {
    return y - x
  }
  return byId(a, b)
}

/** Which ordering a status-bucketed list argues for. Every status but `closed`
 *  is live work — a board column and a Backlog status group alike. */
export function sortFor(status: IssueStatus): (a: Issue, b: Issue) => number {
  return status === 'closed' ? byCloseTimeDescending : byWorkOrder
}
