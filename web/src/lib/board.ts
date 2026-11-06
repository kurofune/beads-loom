import type { Issue, IssueStatus } from '@/api/types'
import { statusLabel } from './beads'
// Column order is the shared issue-ordering rule, not a board-local one: the
// Backlog's status groups ask the same question and must get the same answer.
import { sortFor } from './ordering'

/**
 * Board layout maths — columns, lanes, and the flat order the keyboard walks.
 * Pure, so the column rules and the navigation model are testable without a DOM.
 */

/** Left-to-right flow of work, per the phase-2 work package. */
export const BOARD_COLUMNS: IssueStatus[] = ['open', 'in_progress', 'blocked', 'closed']

/**
 * The page the board asks for, rather than inheriting the server's.
 *
 * A request naming no limit gets `queries.DefaultLimit` = 200
 * (internal/queries/filter.go), and anything above `queries.MaxLimit` = 1000 is
 * silently clamped to it. Asking for exactly MaxLimit is therefore the largest
 * page the server will honour verbatim — enough that no ordinary project
 * truncates at all. It moves the cliff five-fold; it does not remove it, which
 * is what `boardScope` below is for.
 */
export const BOARD_PAGE_SIZE = 1000

/**
 * What the board is holding, against what the project actually contains.
 *
 * A kanban board cannot paginate — 6,030 cards is not an artifact anyone reads
 * — so past its page size the board is a sample, and every count it computes
 * (column totals, lane totals, "N ready") describes that sample. The server
 * hands back the honest total alongside the page; comparing the two is the
 * whole of the decision, and it has to be made in one place because three
 * different labels depend on it.
 */
export interface BoardScope {
  /** Rows the board actually has in hand. */
  fetched: number
  /** Rows the server says match the same request. */
  total: number
  /** True when rows exist that the board never received. */
  truncated: boolean
}

export function boardScope(fetched: number, total: number): BoardScope {
  return { fetched, total, truncated: total > fetched }
}

/**
 * How a count derived from the board's rows is allowed to be written.
 *
 * On a complete board a count is a fact about the project and reads as a bare
 * number, exactly as it always has. On a truncated one it is a fact about an
 * arbitrary first page, and the label carries that with it — a manager reading
 * "43" on a 6,030-issue project is reading a confidently wrong number.
 */
export function countLabel(n: number, scope: BoardScope): string {
  return scope.truncated ? `${n} shown` : String(n)
}

/**
 * Whether the Open column may print its "N ready" figure.
 *
 * Unlike a column count, readiness scoped to a sample answers a question
 * nobody asked: "how much startable work is in the first thousand rows sorted
 * by priority" is not a number anyone can act on. So it is withheld outright
 * while truncated rather than relabelled.
 */
export function showsReady(ready: number, scope: BoardScope): boolean {
  return ready > 0 && !scope.truncated
}

/**
 * The sample stated plainly, both halves named, for the truncation notice.
 *
 * "Loaded", not "showing": the quick filter and the hierarchy modes routinely
 * put fewer cards than this on screen, and a notice about dishonest numbers
 * cannot itself claim to be showing a thousand cards when it is showing three.
 * What the number does say is exactly true in every state — this is the pool
 * the filters, the columns and the lanes are all working from.
 */
export function sampleSummary(scope: BoardScope): string {
  const n = (v: number) => v.toLocaleString('en-US')
  return `Loaded ${n(scope.fetched)} of ${n(scope.total)} issues`
}

/**
 * Statuses the board refuses a column even when the project uses them.
 *
 * Neither of these flows. `deferred` is parked work, so a permanent column
 * spends board width on cards nobody is moving. `pinned` is a standing
 * reference note — bd's own readiness query classes it with `closed`, since a
 * pinned blocker does not block — and a project keeps one or two of them
 * forever. Both stay first-class in Backlog; this hides a column, not the work.
 */
export const HIDDEN_STATUSES: IssueStatus[] = ['deferred', 'pinned']

export type SwimlaneMode = 'none' | 'epic' | 'phase'

export const SWIMLANE_MODES: { id: SwimlaneMode; label: string }[] = [
  { id: 'none', label: 'None' },
  { id: 'epic', label: 'Epic' },
  { id: 'phase', label: 'Phase' },
]

/**
 * Which altitude the board shows, Linear-style: everything, containers only,
 * or leaves only. `top` and `leaf` overlap on purpose — a standalone issue
 * with no parent and no children is both a top-level item and a leaf, and
 * belongs in either view. A mid-level issue with both a parent and children
 * appears only in `all`.
 */
export type HierarchyMode = 'all' | 'top' | 'leaf'

export const HIERARCHY_MODES: { id: HierarchyMode; label: string }[] = [
  { id: 'all', label: 'All' },
  { id: 'top', label: 'Top-level' },
  { id: 'leaf', label: 'Leaves' },
]

/** Must run BEFORE visibleStatuses/buildLanes so column counts and the
 *  "N ready" figure describe what is actually on screen. */
export function filterByHierarchy(issues: Issue[], mode: HierarchyMode): Issue[] {
  switch (mode) {
    case 'top':
      return issues.filter((i) => i.parent === null)
    case 'leaf':
      return issues.filter((i) => i.childStats === null)
    default:
      return issues
  }
}

export interface BoardColumn {
  status: IssueStatus
  label: string
  issues: Issue[]
  /** Open issues here that can actually be started. */
  ready: number
}

export interface Swimlane {
  key: string
  title: string
  /** Shown after the title — the epic id, or nothing for the catch-all lane. */
  subtitle?: string
  columns: BoardColumn[]
  count: number
}

/** Empty epic lanes keep their identifying header without spending space on columns. */
export function laneShowsColumns(mode: SwimlaneMode, lane: Pick<Swimlane, 'count'>): boolean {
  return mode !== 'epic' || lane.count > 0
}

/** Which statuses get a column: the fixed flow, plus any other status in use. */
export function visibleStatuses(issues: Issue[]): IssueStatus[] {
  const used = new Set(issues.map((i) => i.status))
  // Anything else a project actually uses — bd's rarer statuses (`hooked`) and
  // project-defined ones alike — still needs somewhere to live, unless we have
  // deliberately withheld a column from it.
  const extra = [...used].filter(
    (s) => !BOARD_COLUMNS.includes(s) && !HIDDEN_STATUSES.includes(s),
  )
  return [...BOARD_COLUMNS, ...extra.sort()]
}

function toColumns(issues: Issue[], statuses: IssueStatus[]): BoardColumn[] {
  const buckets = new Map<string, Issue[]>(statuses.map((s) => [s, []]))
  for (const issue of issues) {
    const bucket = buckets.get(issue.status)
    if (bucket) bucket.push(issue)
  }
  return statuses.map((status) => {
    const list = (buckets.get(status) ?? []).sort(sortFor(status))
    return {
      status,
      label: statusLabel(status),
      issues: list,
      ready: list.filter((i) => i.ready).length,
    }
  })
}

/** The `phase-*` label, which ARCHITECTURE.md names as the grouping dimension. */
export function phaseOf(issue: Issue): string | null {
  return issue.labels.find((l) => l.startsWith('phase-')) ?? null
}

/** In epic-lane mode a bead that heads a lane is drawn as that lane's header
 *  and nothing else: the container must not compete with its contents for
 *  board space. Type `epic` counts even with no children — otherwise a
 *  childless epic falls into the "No epic" catch-all it is the opposite of. */
export function headsLane(issue: Issue): boolean {
  return issue.type === 'epic' || issue.childStats !== null
}

export function buildLanes(
  allIssues: Issue[],
  mode: SwimlaneMode,
  statuses: IssueStatus[],
): Swimlane[] {
  // An issue whose status has no column cannot be rendered, so it must not be
  // counted either — a lane header promising cards the lane does not show is
  // how "where did that bead go?" starts.
  const issues = allIssues.filter((i) => statuses.includes(i.status))

  if (mode === 'none') {
    return [{ key: 'all', title: 'All', columns: toColumns(issues, statuses), count: issues.length }]
  }

  const groups = new Map<string, { title: string; subtitle?: string; issues: Issue[] }>()
  const key = (issue: Issue) =>
    mode === 'epic'
      ? (headsLane(issue) ? issue.id : (issue.parent?.id ?? ''))
      : (phaseOf(issue) ?? '')

  for (const issue of issues) {
    const k = key(issue)
    if (!groups.has(k)) {
      groups.set(k, {
        title:
          k === ''
            ? mode === 'epic'
              ? 'No epic'
              : 'No phase'
            : mode === 'epic'
              ? // The epic card itself may create the lane before any child does.
                (issue.id === k ? issue.title : (issue.parent?.title ?? k))
              : k,
        ...(mode === 'epic' && k !== '' ? { subtitle: k } : {}),
        issues: [],
      })
    }
    groups.get(k)!.issues.push(issue)
  }

  return (
    [...groups.entries()]
      // Named lanes first, alphabetically; the catch-all lane sinks to the
      // bottom because "no epic" is where you look last, not first.
      .sort(([a], [b]) => (a === '' ? 1 : b === '' ? -1 : a.localeCompare(b)))
      .map(([k, group]) => {
        const cards =
          mode === 'epic' && k !== ''
            ? group.issues.filter((issue) => issue.id !== k)
            : group.issues
        return {
          key: k || '__none__',
          title: group.title,
          ...(group.subtitle ? { subtitle: group.subtitle } : {}),
          columns: toColumns(cards, statuses),
          count: cards.length,
        }
      })
  )
}

/**
 * Keyboard order across the board: down a column, then on to the next column.
 * Column-major matches how the eye reads a kanban board, and it makes `j`/`k`
 * mean "next card in this column" rather than an unpredictable zig-zag.
 */
export function boardOrder(lanes: Swimlane[]): string[] {
  const out: string[] = []
  for (const lane of lanes) {
    for (const column of lane.columns) {
      for (const issue of column.issues) out.push(issue.id)
    }
  }
  return out
}

/** Where a card sits, so arrow keys can move by column while keeping depth. */
export interface CardAddress {
  lane: number
  column: number
  row: number
}

export function locate(lanes: Swimlane[], id: string): CardAddress | null {
  for (let lane = 0; lane < lanes.length; lane++) {
    const columns = lanes[lane]!.columns
    for (let column = 0; column < columns.length; column++) {
      const row = columns[column]!.issues.findIndex((i) => i.id === id)
      if (row >= 0) return { lane, column, row }
    }
  }
  return null
}

const at = (lanes: Swimlane[], a: CardAddress): string | undefined =>
  lanes[a.lane]?.columns[a.column]?.issues[a.row]?.id

/** Move within a column (`j`/`k`), clamping at the ends. */
export function stepRow(lanes: Swimlane[], from: string | null, delta: number): string | null {
  const order = boardOrder(lanes)
  if (order.length === 0) return null
  if (!from) return delta > 0 ? order[0]! : order[order.length - 1]!

  const a = locate(lanes, from)
  if (!a) return order[0]!
  const column = lanes[a.lane]!.columns[a.column]!
  const row = a.row + delta
  if (row >= 0 && row < column.issues.length) return at(lanes, { ...a, row }) ?? from
  return from
}

/**
 * Move between columns (arrows), keeping the row depth where possible and
 * skipping empty columns so a run of empty statuses doesn't swallow the press.
 */
export function stepColumn(lanes: Swimlane[], from: string | null, delta: number): string | null {
  const order = boardOrder(lanes)
  if (order.length === 0) return null
  if (!from) return order[0]!

  const a = locate(lanes, from)
  if (!a) return order[0]!
  const columns = lanes[a.lane]!.columns
  for (let c = a.column + delta; c >= 0 && c < columns.length; c += delta) {
    const target = columns[c]!
    if (target.issues.length === 0) continue
    const row = Math.min(a.row, target.issues.length - 1)
    return target.issues[row]!.id
  }
  return from
}
