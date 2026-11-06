import type { Issue, IssueStatus, Priority } from '@/api/types'
import { STATUS_ORDER, priorityLabel, statusLabel } from './beads'
// Group order is the shared issue-ordering rule, not a Backlog-local one: the
// board's columns ask the same question and must get the same answer.
import { byWorkOrder, sortFor } from './ordering'

/**
 * Backlog grouping.
 *
 * `phase-*` swimlanes are the obvious next dimension, so this is a mode table
 * rather than an if/else: adding one means adding an entry to GROUP_MODES and a
 * branch in groupIssues, with no change to the view.
 */
export type GroupMode = 'status' | 'priority' | 'epic' | 'none'

export const GROUP_MODES: { id: GroupMode; label: string }[] = [
  { id: 'status', label: 'Status' },
  { id: 'priority', label: 'Priority' },
  { id: 'epic', label: 'Epic' },
  // None is the "off" switch, not a dimension, so it stays last however many
  // dimensions land above it.
  { id: 'none', label: 'None' },
]

/** Key of the group holding everything with no epic ancestor. */
export const NO_EPIC_KEY = 'no-epic'

export interface IssueGroup {
  key: string
  label: string
  issues: Issue[]
  /** Set when the group *is* a status, so its header can carry the colour. */
  status?: IssueStatus
  priority?: Priority
  /** Set when the group *is* an epic, so its header can carry the id beside the
   *  title — an epic is named by both, and two epics can share a title. */
  epicId?: string
  /** Issues in this group that can actually be picked up right now. */
  readyCount: number
}

/**
 * Within a group, ordering is the group's own argument, so `finalize` takes the
 * comparator rather than owning one: a status group defers to `sortFor` (which
 * makes the Closed group read as a log, newest close first — the same rule the
 * board's Closed column follows), the priority and none modes are not asking
 * "what did I finish last" and stay on work order, and an epic group pins its
 * own epic above work order. Every one of those comparators ends in an id
 * tiebreak, so a group's order is total and never falls through to whatever
 * `Array.prototype.sort` did with equal keys.
 */
const finalize = (
  key: string,
  label: string,
  issues: Issue[],
  extra: Partial<IssueGroup> = {},
  compare: (a: Issue, b: Issue) => number = byWorkOrder,
): IssueGroup => ({
  key,
  label,
  issues: [...issues].sort(compare),
  readyCount: issues.filter((i) => i.ready).length,
  ...extra,
})

/**
 * An epic group's own epic leads it whatever its priority or readiness: it is
 * the group's subject, and sorted as one more bead it lands mid-list under its
 * own children. Every other row — and every row of a group whose epic the
 * filters removed — keeps work order.
 */
const epicFirst =
  (epicId: string) =>
  (a: Issue, b: Issue): number =>
    Number(b.id === epicId) - Number(a.id === epicId) || byWorkOrder(a, b)

/**
 * Each issue's nearest epic ancestor, or null when it has none.
 *
 * The walk follows the `parent` ref the list query already returns
 * (`internal/queries/issues.go`), and it can only see the page in hand: a
 * ParentRef carries an id and a title but no type, so an ancestor that is not
 * among the fetched rows cannot be shown to be an epic. Such a chain is broken
 * rather than epic-less-so-far, and the issue lands in the No epic group — the
 * alternative is inventing a group from a parent that might be a plain feature,
 * titled from a ref that might name one.
 *
 * `within` is the whole fetched page, which is deliberately NOT the same list as
 * the rows being grouped: the Backlog groups what survived the quick filter, the
 * stale filter and the hierarchy mode, and "Leaves" excludes every epic by
 * definition. Reading ancestry from the narrowed rows would drop the whole view
 * into No epic the moment a manager typed a letter.
 *
 * An epic is its own epic, so a nested epic gets its own group rather than
 * folding into the one above it — the same reading `epicIndex` in ./graph.ts
 * and the Initiatives rollup (`internal/queries/initiatives.go`) already take.
 * The whole walked path is memoized, not just the issue asked for, which is
 * what keeps this linear on a deep tree; the `seen` set makes a parent cycle
 * terminate instead of hanging the render.
 */
function epicAncestors(issues: Issue[], within: Issue[]): Map<string, Issue | null> {
  const byId = new Map(within.map((i) => [i.id, i]))
  for (const issue of issues) if (!byId.has(issue.id)) byId.set(issue.id, issue)
  const index = new Map<string, Issue | null>()

  for (const issue of issues) {
    const path: string[] = []
    const seen = new Set<string>()
    let at: Issue | undefined = issue
    let found: Issue | null = null

    while (at && !seen.has(at.id)) {
      seen.add(at.id)
      path.push(at.id)
      const cached = index.get(at.id)
      if (cached !== undefined) {
        found = cached
        break
      }
      if (at.type === 'epic') {
        found = at
        break
      }
      at = at.parent ? byId.get(at.parent.id) : undefined
    }

    for (const id of path) if (!index.has(id)) index.set(id, found)
  }
  return index
}

/**
 * `page` is the unfiltered set the rows came from, and only the `epic` mode
 * reads it: an epic that the quick filter or the hierarchy mode removed is still
 * the epic its surviving children belong to. It defaults to the rows themselves,
 * so every existing caller keeps its meaning.
 */
export function groupIssues(
  issues: Issue[],
  mode: GroupMode,
  page: Issue[] = issues,
): IssueGroup[] {
  if (mode === 'none') {
    return issues.length ? [finalize('all', 'All issues', issues)] : []
  }

  if (mode === 'priority') {
    const buckets = new Map<number, Issue[]>()
    for (const issue of issues) {
      const list = buckets.get(issue.priority) ?? []
      list.push(issue)
      buckets.set(issue.priority, list)
    }
    return [...buckets.entries()]
      .sort(([a], [b]) => a - b)
      .map(([priority, list]) =>
        finalize(`p${priority}`, priorityLabel(priority), list, {
          priority: priority as Priority,
        }),
      )
  }

  if (mode === 'epic') {
    const ancestors = epicAncestors(issues, page)
    const buckets = new Map<string, Issue[]>()
    const epics = new Map<string, Issue>()
    const orphans: Issue[] = []

    for (const issue of issues) {
      const epic = ancestors.get(issue.id) ?? null
      if (!epic) {
        orphans.push(issue)
        continue
      }
      epics.set(epic.id, epic)
      const list = buckets.get(epic.id) ?? []
      list.push(issue)
      buckets.set(epic.id, list)
    }

    // Biggest epic first — with no fixed order to fall back on (unlike statuses
    // and priorities), size is the only ranking the data supplies. The id
    // tiebreak keeps a tie from depending on sort stability, exactly as the
    // within-group comparators do.
    const groups = [...buckets.entries()]
      .sort(([aId, a], [bId, b]) => b.length - a.length || aId.localeCompare(bId))
      .map(([id, list]) =>
        finalize(
          `epic:${id}`,
          epics.get(id)!.title || id,
          list,
          { epicId: id },
          epicFirst(id),
        ),
      )

    // Everything with no epic is a group, never a silent drop: on a 207-issue game backlog
    // it is 35 issues — larger than any single epic, which is exactly why it is
    // appended after the count-descending sort rather than ranked by it. Leading
    // the Backlog with "No epic" would bury the epics the mode was chosen to see.
    if (orphans.length) groups.push(finalize(NO_EPIC_KEY, 'No epic', orphans))
    return groups
  }

  // Status. Known statuses keep the fixed operational order from
  // ARCHITECTURE.md (what's moving → what's stuck → what's available →
  // what's parked); anything a project defined itself is appended
  // alphabetically rather than dropped.
  const buckets = new Map<string, Issue[]>()
  for (const issue of issues) {
    const list = buckets.get(issue.status) ?? []
    list.push(issue)
    buckets.set(issue.status, list)
  }

  const known = STATUS_ORDER.filter((s) => buckets.has(s))
  const custom = [...buckets.keys()].filter((s) => !STATUS_ORDER.includes(s)).sort()

  return [...known, ...custom].map((status) =>
    finalize(status, statusLabel(status), buckets.get(status)!, { status }, sortFor(status)),
  )
}

/**
 * Client-side quick filter over the fetched page.
 *
 * Deliberately not a server round trip: at phase-1 page sizes the whole
 * backlog is already in memory, and filtering locally keeps typing at 60fps.
 * The server-side filter bar is its own bead (loom-3ft.4).
 */
export function quickFilter(issues: Issue[], query: string): Issue[] {
  const q = query.trim().toLowerCase()
  if (!q) return issues
  const terms = q.split(/\s+/)
  return issues.filter((issue) => {
    const haystack = [
      issue.id,
      issue.title,
      issue.assignee,
      issue.type,
      issue.status,
      `p${issue.priority}`,
      ...issue.labels,
    ]
      .join(' ')
      .toLowerCase()
    return terms.every((t) => haystack.includes(t))
  })
}

/* --- flattened rows, for virtualization --------------------------------- */

export type BacklogRow =
  | { kind: 'group'; key: string; group: IssueGroup; index: number }
  | { kind: 'issue'; key: string; issue: Issue; groupKey: string }

/**
 * Groups → one flat list, because the virtualizer needs a single index space.
 * Collapsed groups keep their header and drop their rows.
 */
export function toRows(groups: IssueGroup[], collapsed: ReadonlySet<string>): BacklogRow[] {
  const rows: BacklogRow[] = []
  groups.forEach((group, index) => {
    rows.push({ kind: 'group', key: `g:${group.key}`, group, index })
    if (collapsed.has(group.key)) return
    for (const issue of group.issues) {
      rows.push({ kind: 'issue', key: `i:${issue.id}`, issue, groupKey: group.key })
    }
  })
  return rows
}
