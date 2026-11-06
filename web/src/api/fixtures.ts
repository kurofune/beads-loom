/**
 * Dev fixtures — only loaded when VITE_MOCK=1.
 *
 * Shapes follow the wire contract (internal/httpapi and ./types.ts), and
 * the content mirrors this repo's own seeded beads so the mock exercises the
 * cases the real data has: epics with children, a dependency-blocked bead that
 * is still stored as `open`, `needs-operator` / `review-follow-up` labels, an
 * unreachable project, and a project with no issues at all.
 */
import type {
  BulkPatchRequest,
  BulkResult,
  CreateIssueRequest,
  DiscoveryStatus,
  Health,
  Initiative,
  InitiativeHealth,
  InitiativeUpdate,
  InitiativeUpdateSummary,
  Issue,
  IssueDetail,
  IssueFilter,
  IssueList,
  IssuePatch,
  Priority,
  Project,
  ProjectSchema,
  Run,
  Stats,
} from './types'
import { BUILTIN_SCHEMA } from './types'
// The started bucket uses the same status set as the rest of the UI.
import { isActive } from '@/lib/beads'
import { NO_TARGET_LABEL } from '@/lib/target-nudge'
import type { LoomClient } from './client'

const LATENCY_MS = 300
// Anchored to the wall clock so relative timestamps in the mock always read
// as the recent past, whenever the fixture set is loaded.
const now = Date.now()

/** Minutes ago → RFC3339, so relative timestamps look alive in the mock. */
const ago = (minutes: number) => new Date(now - minutes * 60_000).toISOString()

function fixtureInitiativeHealth(total: number, closed: number, due: string | null): InitiativeHealth {
  if (total === 0) return { state: 'no-scope', explanation: 'No descendants — nothing to burn down' }
  if (!due) return { state: 'no-target', explanation: 'No due date — no target to assess' }
  if (closed === total) return { state: 'on-track', explanation: `All ${total} descendants closed` }
  const remaining = total - closed
  const days = Math.max(0, Math.ceil((Date.parse(due) - now) / 86_400_000))
  return {
    state: 'at-risk',
    explanation: `closed ${closed} in 14d, ${remaining} remain, due in ${days}d — required rate fixture (at-risk)`,
  }
}

type Seed = Partial<Issue> & Pick<Issue, 'id' | 'title' | 'status' | 'priority'>

function issue(seed: Seed): Issue {
  return {
    type: 'task',
    assignee: '',
    labels: [],
    estimate: 0,
    created: ago(60 * 24 * 3),
    updated: ago(60 * 6),
    closed: null,
    due: null,
    deferUntil: null,
    ready: seed.status === 'open',
    missingAcceptance: false,
    missingCloseReason: false,
    pinned: false,
    depCounts: { blockedBy: 0, blocks: 0 },
    parent: null,
    childStats: null,
    ...seed,
  }
}

const FOUNDATIONS = { id: 'loom-3ft', title: '[Foundations] Config, server registry, read-only backlog' }
const BOARD = { id: 'loom-yqh', title: '[Board] Kanban with bd-backed writes' }
const RUNS = { id: 'loom-0zj', title: '[Runs] Djinn run monitoring' }

const LOOM_ISSUES: Issue[] = [
  issue({
    id: 'loom-323',
    title: 'Status dropdown loses selection on refetch',
    type: 'bug',
    status: 'in_progress',
    priority: 0,
    assignee: 'nate',
    labels: ['layer-ui', 'review-follow-up'],
    estimate: 45,
    updated: ago(12),
    ready: false,
    depCounts: { blockedBy: 0, blocks: 1 },
  }),
  issue({
    id: 'loom-3ft',
    title: '[Foundations] Config, server registry, read-only backlog',
    type: 'epic',
    status: 'open',
    priority: 1,
    labels: ['phase-1'],
    updated: ago(60 * 20),
    depCounts: { blockedBy: 0, blocks: 1 },
    childStats: { total: 5, closed: 2, started: 1 },
  }),
  issue({
    id: 'loom-3ft.1',
    title: 'Config loader with dolt server registry',
    type: 'feature',
    status: 'closed',
    priority: 1,
    assignee: 'djinn-worker-2',
    labels: ['phase-1', 'layer-api'],
    closed: ago(60 * 30),
    updated: ago(60 * 30),
    ready: false,
    parent: FOUNDATIONS,
    depCounts: { blockedBy: 0, blocks: 2 },
    missingAcceptance: true,
  }),
  issue({
    id: 'loom-3ft.2',
    title: 'Dolt connection pool per configured server',
    type: 'feature',
    status: 'closed',
    priority: 1,
    assignee: 'djinn-worker-2',
    labels: ['phase-1', 'layer-api'],
    closed: ago(60 * 26),
    updated: ago(60 * 26),
    ready: false,
    parent: FOUNDATIONS,
    depCounts: { blockedBy: 1, blocks: 1 },
    missingCloseReason: true,
  }),
  issue({
    id: 'loom-3ft.3',
    title: 'Backlog table view with status grouping',
    type: 'feature',
    status: 'in_progress',
    priority: 1,
    assignee: 'djinn-worker-1',
    labels: ['phase-1', 'layer-ui'],
    estimate: 240,
    updated: ago(4),
    ready: false,
    parent: FOUNDATIONS,
    depCounts: { blockedBy: 0, blocks: 1 },
  }),
  issue({
    id: 'loom-3ft.4',
    title: 'Filter bar: status, type, priority, label, free text',
    type: 'feature',
    status: 'open',
    priority: 2,
    labels: ['phase-1', 'layer-ui'],
    estimate: 120,
    updated: ago(60 * 19),
    // Stored status is `open`, but a `blocks` dep is unmet — the row must
    // amber as "blocked by 1", and `ready` is the only thing that says so.
    ready: false,
    parent: FOUNDATIONS,
    depCounts: { blockedBy: 1, blocks: 0 },
  }),
  issue({
    id: 'loom-3ft.5',
    title: 'Saved views persisted per project',
    type: 'feature',
    status: 'open',
    priority: 2,
    labels: ['phase-1', 'layer-ui'],
    updated: ago(60 * 40),
    ready: true,
    parent: FOUNDATIONS,
  }),
  issue({
    id: 'loom-yqh',
    title: '[Board] Kanban with bd-backed writes',
    type: 'epic',
    status: 'open',
    priority: 1,
    labels: ['phase-2'],
    updated: ago(60 * 44),
    ready: false,
    depCounts: { blockedBy: 1, blocks: 1 },
    childStats: { total: 5, closed: 0, started: 0 },
  }),
  issue({
    id: 'loom-yqh.1',
    title: 'bd exec write layer with typed verbs',
    type: 'feature',
    status: 'open',
    priority: 1,
    labels: ['phase-2', 'layer-api'],
    estimate: 180,
    updated: ago(60 * 44),
    ready: false,
    parent: BOARD,
    depCounts: { blockedBy: 2, blocks: 3 },
  }),
  issue({
    id: 'loom-yqh.2',
    title: 'Kanban board with status columns and WIP counts',
    type: 'feature',
    status: 'open',
    priority: 1,
    labels: ['phase-2', 'layer-ui'],
    updated: ago(60 * 44),
    ready: false,
    parent: BOARD,
    depCounts: { blockedBy: 2, blocks: 1 },
  }),
  issue({
    id: 'loom-yqh.3',
    title: 'Drag-and-drop status transitions via bd',
    type: 'feature',
    status: 'open',
    priority: 1,
    labels: ['phase-2', 'layer-ui'],
    updated: ago(60 * 45),
    ready: false,
    parent: BOARD,
    depCounts: { blockedBy: 1, blocks: 0 },
  }),
  issue({
    id: 'loom-yqh.4',
    title: 'Operator smoke-test of destructive write paths',
    type: 'chore',
    status: 'open',
    priority: 2,
    // The loud one: summoner cannot finish this unattended.
    labels: ['phase-2', 'needs-operator'],
    updated: ago(60 * 45),
    ready: false,
    parent: BOARD,
    depCounts: { blockedBy: 3, blocks: 0 },
  }),
  issue({
    id: 'loom-yqh.5',
    title: 'Bulk-edit toolbar for multi-select',
    type: 'feature',
    // Manually parked by a human — distinct from dependency-blocked above.
    status: 'blocked',
    priority: 2,
    labels: ['phase-2', 'layer-ui'],
    updated: ago(60 * 8),
    ready: false,
    parent: BOARD,
    depCounts: { blockedBy: 1, blocks: 0 },
  }),
  issue({
    id: 'loom-0zj',
    title: '[Runs] Djinn run monitoring',
    type: 'epic',
    status: 'open',
    priority: 2,
    labels: ['phase-4'],
    updated: ago(60 * 50),
    ready: false,
    depCounts: { blockedBy: 1, blocks: 0 },
    childStats: { total: 3, closed: 0, started: 0 },
  }),
  issue({
    id: 'loom-0zj.1',
    title: 'SSE channel with dolt head-hash polling',
    type: 'feature',
    status: 'open',
    priority: 1,
    labels: ['phase-4', 'layer-api'],
    estimate: 150,
    updated: ago(60 * 50),
    ready: false,
    parent: RUNS,
    depCounts: { blockedBy: 1, blocks: 2 },
  }),
  issue({
    id: 'loom-0zj.2',
    title: 'Historical cost analytics dashboard',
    type: 'feature',
    status: 'deferred',
    priority: 3,
    labels: ['phase-4'],
    deferUntil: '2026-09-15T00:00:00Z',
    updated: ago(60 * 72),
    ready: false,
    parent: RUNS,
  }),
  issue({
    id: 'loom-0zj.3',
    title: 'Rotate and cap summoner log ingestion buffers',
    type: 'chore',
    status: 'open',
    priority: 4,
    labels: ['phase-4', 'layer-api'],
    updated: ago(60 * 80),
    ready: false,
    parent: RUNS,
    depCounts: { blockedBy: 1, blocks: 0 },
  }),
  issue({
    id: 'loom-rs5',
    title: 'Spike: dolt_diff tables for issue history rendering',
    type: 'spike',
    status: 'open',
    priority: 2,
    labels: ['layer-api'],
    estimate: 90,
    updated: ago(60 * 14),
    ready: true,
  }),
  issue({
    id: 'loom-y1y',
    title: 'Pool: add exponential backoff on reconnect',
    type: 'task',
    status: 'open',
    priority: 3,
    labels: ['layer-api', 'review-follow-up'],
    updated: ago(60 * 33),
    ready: true,
    depCounts: { blockedBy: 0, blocks: 0 },
  }),
  issue({
    id: 'loom-8kq',
    title: 'Decide: virtualize the backlog or cap the page size',
    type: 'decision',
    status: 'pinned',
    priority: 2,
    labels: ['layer-ui'],
    pinned: true,
    updated: ago(60 * 2),
    ready: false,
  }),
  issue({
    id: 'loom-w4d',
    title: 'Theme tokens fail AA on the deferred status chip',
    type: 'bug',
    status: 'closed',
    priority: 2,
    assignee: 'nate',
    labels: ['layer-ui'],
    closed: ago(60 * 9),
    updated: ago(60 * 9),
    ready: false,
    closeReason: 'fixed',
  } as Seed),
  issue({
    id: 'loom-m2p',
    title: 'bd version check at boot warns instead of dying',
    type: 'task',
    status: 'open',
    priority: 3,
    labels: ['layer-api'],
    updated: ago(60 * 55),
    ready: true,
  }),
  issue({
    id: 'loom-q7t',
    title: 'Hook: mirror AGENTS.md edits into CLAUDE.md',
    type: 'chore',
    status: 'hooked',
    priority: 4,
    updated: ago(60 * 100),
    ready: false,
  }),
  issue({
    id: 'loom-v1c',
    title: 'Registry: treat a missing dolt-server.port as degraded, not fatal',
    type: 'feature',
    status: 'closed',
    priority: 1,
    assignee: 'djinn-worker-2',
    labels: ['phase-1', 'layer-api'],
    closed: ago(60 * 34),
    updated: ago(60 * 34),
    ready: false,
  }),
]

const DJINN_ISSUES: Issue[] = [
  issue({
    id: 'djinn-a1x',
    title: 'Summoner: honour --max-cost across a session, not per bead',
    type: 'feature',
    status: 'in_progress',
    priority: 1,
    assignee: 'nate',
    labels: ['needs-operator'],
    updated: ago(38),
    ready: false,
    depCounts: { blockedBy: 0, blocks: 2 },
  }),
  issue({
    id: 'djinn-b7k',
    title: 'Evidence YAML: emit per-criterion file:line spans',
    type: 'feature',
    status: 'open',
    priority: 2,
    updated: ago(60 * 5),
    ready: true,
  }),
  issue({
    id: 'djinn-c3m',
    title: 'Worker worktrees leak on SIGKILL',
    type: 'bug',
    status: 'open',
    priority: 0,
    labels: ['review-follow-up'],
    updated: ago(60 * 2),
    ready: true,
  }),
]

/** Derived, not hand-written — otherwise the sidebar counts drift away from
 *  the rows the mock actually serves and the fixture stops being a useful
 *  rehearsal of the real thing. */
function healthOf(issues: Issue[]): Project['health'] {
  return {
    ready: issues.filter((i) => i.ready).length,
    inProgress: issues.filter((i) => i.status === 'in_progress').length,
    blocked: issues.filter((i) => i.status === 'blocked').length,
    open: issues.filter((i) => i.status !== 'closed').length,
    total: issues.length,
  }
}

const PROJECTS: Project[] = [
  {
    name: 'loom',
    db: 'loom',
    workdir: '~/code/loom',
    source: 'config',
    reachable: true,
    reason: '',
    writable: true,
    writeReason: '',
    integrations: {},
    health: healthOf(LOOM_ISSUES),
  },
  {
    name: 'djinn',
    db: 'djinn',
    workdir: '~/code/djinn',
    source: 'config',
    reachable: true,
    reason: '',
    writable: true,
    writeReason: '',
    integrations: { djinn: { capabilities: ['runs', 'evidence', 'dispatch'] } },
    health: healthOf(DJINN_ISSUES),
  },
  {
    name: 'Project-Pheonix',
    db: 'Project_Pheonix',
    workdir: '~/code/Project-Pheonix',
    source: 'config',
    reachable: false,
    reason: 'dial tcp 127.0.0.1:3309: connect: connection refused',
    writable: true,
    writeReason: '',
    integrations: {
      djinn: {
        capabilities: ['runs', 'evidence'],
        reasons: { dispatch: 'the summoner binary was not found on PATH; install it to dispatch from Loom' },
      },
    },
    health: { ready: 0, inProgress: 0, blocked: 0, open: 0, total: 0 },
  },
]

/**
 * VITE_MOCK_ROWS pads the loom backlog to N rows so the virtualized table can
 * be exercised at the 500-row bar from the work package without hand-writing
 * 500 fixtures.
 */
function padded(base: Issue[], target: number): Issue[] {
  if (target <= base.length) return base
  const statuses = ['open', 'in_progress', 'blocked', 'deferred', 'closed', 'pinned'] as const
  const types = ['task', 'bug', 'feature', 'chore', 'spike'] as const
  const out = [...base]
  for (let i = base.length; i < target; i++) {
    const status = statuses[i % statuses.length]!
    out.push(
      issue({
        id: `loom-gen${i.toString(36).padStart(3, '0')}`,
        title: `Generated backlog row ${i} — virtualization load sample`,
        type: types[i % types.length]!,
        status,
        priority: (i % 5) as Priority,
        labels: i % 3 === 0 ? ['phase-3'] : [],
        updated: ago(i * 7),
        ready: status === 'open' && i % 4 !== 0,
        closed: status === 'closed' ? ago(i * 9) : null,
        depCounts: { blockedBy: i % 4 === 0 ? 1 : 0, blocks: 0 },
      }),
    )
  }
  return out
}

const MOCK_ROWS = Number(import.meta.env.VITE_MOCK_ROWS ?? 0) || 0
const BY_PROJECT: Record<string, Issue[]> = {
  loom: padded(LOOM_ISSUES, MOCK_ROWS),
  djinn: DJINN_ISSUES,
  'Project-Pheonix': [],
}

const MARKDOWN_BLURB: Record<string, Partial<IssueDetail>> = {
  'loom-3ft': {
    description: [
      'Foundations for the Loom management GUI: config loading, dolt server registry,',
      'and the read-only backlog view.',
      '',
      '**Diagnostic Question:** Can a manager see the entire backlog of a project,',
      'grouped and filterable, within 2 seconds of opening the app?',
      '',
      "**Reference Standard:** Linear's issue list — density, keyboard navigation,",
      'instant filtering.',
    ].join('\n'),
    acceptanceCriteria: 'All phase-1 children closed\nmake dev serves the backlog view against the seeded test db',
    notes: 'Phase gate: phase-2 work must not start until this closes.',
  },
  'loom-3ft.3': {
    description:
      'The dense Linear-style backlog table, grouped by status, with `j/k` row navigation. First user-visible surface.',
    acceptanceCriteria: [
      'Rows grouped by status with collapsible group headers',
      'j/k moves selection, Enter opens detail panel',
      'Priority renders as P0-P4 badge with per-priority color token',
      'Remains scrollable and readable with 500 seeded issues',
      'Empty project shows a designed empty state, not a blank table',
    ].join('\n'),
    design: 'web/src/views/Backlog.tsx; TanStack Query for fetch; virtualized list.',
    notes: 'Context: parent epic loom-3ft carries the shared brief.',
  },
  'loom-3ft.4': {
    description:
      'Filter bar across the top of the backlog: status, type, priority, label, and free text.\n\nFilters compose (AND across dimensions, OR within one) and are reflected in the URL so a filtered view is shareable.',
    acceptanceCriteria: [
      'Each dimension is a popover with keyboard selection',
      'Active filters render as removable chips',
      'Filter state round-trips through the URL',
    ].join('\n'),
    design: 'Query params map 1:1 onto the /issues filter contract.',
  },
  'loom-yqh.4': {
    description:
      'Manually exercise every destructive write path (close, reopen, bulk status change, dependency removal) against a scratch project before the board ships.',
    acceptanceCriteria: [
      'Every write verb exercised once against a throwaway db',
      'Each failure mode produces a legible error toast, not a silent no-op',
    ].join('\n'),
    notes: '**Cannot run unattended** — a human has to confirm the destructive prompts.',
  },
}

function detail(project: string, id: string): IssueDetail | undefined {
  const base = (BY_PROJECT[project] ?? []).find((i) => i.id === id)
  if (!base) return undefined
  const all = BY_PROJECT[project] ?? []
  const ref = (i: Issue) => ({
    id: i.id,
    title: i.title,
    status: i.status,
    priority: i.priority,
    ready: i.ready,
  })
  const children = all.filter((i) => i.parent?.id === id)
  const parent = base.parent ? all.find((i) => i.id === base.parent!.id) : undefined

  // Synthesize plausible blocked-by / blocks lists from depCounts so the typed
  // dependency sections in the slide-over have something to render.
  const others = all.filter((i) => i.id !== id)
  const blockedBy = others.slice(0, base.depCounts.blockedBy).map(ref)
  const blocks = others.slice(-base.depCounts.blocks || others.length).map(ref).slice(0, base.depCounts.blocks)

  return {
    ...base,
    health: base.type === 'epic'
      ? fixtureInitiativeHealth(children.length, children.filter((child) => child.status === 'closed').length, base.due)
      : null,
    description: '',
    acceptanceCriteria: '',
    design: '',
    notes: '',
    closeReason: base.status === 'closed' ? 'fixed' : '',
    owner: 'Nate Gross',
    ...MARKDOWN_BLURB[id],
    deps: {
      blocks,
      blockedBy,
      children: children.map(ref),
      discoveredFrom: id === 'loom-y1y' ? [ref(all.find((i) => i.id === 'loom-3ft.2')!)] : [],
      parent: parent ? ref(parent) : null,
    },
    comments:
      id === 'loom-3ft.3'
        ? [
            {
              id: 'c1',
              author: 'nate',
              text: 'Group header counts should show *ready* / total, not just total.',
              created: ago(60 * 6),
            },
            {
              id: 'c2',
              author: 'djinn-worker-1',
              text: 'Ack — folded into the group header. Ready count uses the `ready` field, not status.',
              created: ago(50),
            },
          ]
        : [],
    events: [
      { type: 'created', actor: 'nate', oldValue: '', newValue: '', comment: '', created: base.created },
      ...(base.status === 'in_progress'
        ? [
            {
              type: 'status_changed',
              actor: 'djinn-worker-1',
              oldValue: 'open',
              newValue: 'in_progress',
              comment: '',
              created: ago(60 * 12),
            },
            {
              type: 'assignee_changed',
              actor: 'summoner',
              oldValue: '',
              newValue: base.assignee,
              comment: '',
              created: ago(60 * 12),
            },
          ]
        : []),
      ...(base.closed
        ? [
            {
              type: 'closed',
              actor: 'djinn-worker-2',
              oldValue: 'in_progress',
              newValue: 'closed',
              comment: 'fixed',
              created: base.closed,
            },
          ]
        : []),
    ],
  }
}

const delay = <T,>(value: T): Promise<T> =>
  new Promise((resolve) => setTimeout(() => resolve(value), LATENCY_MS))

let discoveryStatus: DiscoveryStatus = {
  enabled: false,
  roots: [],
  depth: 3,
  exclude: [],
  intervalSeconds: 300,
  lastScanAt: null,
  hidden: [],
}

function statsFor(issues: Issue[]): Stats {
  const byStatus: Record<string, number> = {}
  for (const i of issues) byStatus[i.status] = (byStatus[i.status] ?? 0) + 1
  const weekAgo = now - 7 * 24 * 60 * 60_000
  return {
    byStatus,
    ready: issues.filter((i) => i.ready).length,
    depBlocked: issues.filter((i) => i.status === 'open' && !i.ready).length,
    closedLast7d: issues.filter((i) => i.closed && Date.parse(i.closed) > weekAgo).length,
  }
}

/* --- weekly initiative updates ---------------------------------------------
 *
 * A year of history is the scale the sheet must stay readable at, so the
 * fixture generates it rather than hand-listing three rows. Every week that is
 * not the quiet one or the broken one carries cited bullets, because an
 * uncited claim is what publish refuses.
 */

/** Walks ISO weeks backwards from `from`, crossing the year boundary. */
export function weeksBack(from: string, count: number): string[] {
  const [year, week] = from.split('-W').map(Number) as [number, number]
  const out: string[] = []
  let y = year
  let w = week
  for (let i = 0; i < count; i++) {
    out.push(`${y}-W${String(w).padStart(2, '0')}`)
    w -= 1
    if (w === 0) {
      y -= 1
      // 52 weeks for every year in this fixture's range; the point is the
      // rollover, not a perfect ISO calendar.
      w = 52
    }
  }
  return out
}

/** The run the mock history cites and the mock run list answers to. */
const MOCK_RUN_ID = 'run-1785600000000000000'

/**
 * A year of updates for one epic, newest first.
 *
 * Index 0 is the current draft. One week is quiet, one week is a stored value
 * that will not decode, and one week's draft carries an uncited claim so the
 * publish refusal has something to refuse.
 */
export function mockUpdateHistory(epic: string, count = 52): InitiativeUpdate[] {
  return weeksBack('2026-W32', count).map((week, index) => {
    const base = { epic, week, schema: 1, generatedAt: `2026-08-08T0${index % 10}:00:00Z` }
    if (index === 3) {
      return { ...base, status: 'draft', body: '', bodySha: '', parseError: 'unexpected end of JSON input' }
    }
    if (index === 2) {
      return {
        ...base,
        status: 'published',
        body: `# ${epic} — ${week}\n\nQuiet week for ${epic}: no beads closed, started, or discovered.\n\n## Reconciliation vs previous week\n- ${epic}.1 remains in flight (${epic}.1)\n`,
        bodySha: `sha-${week}`,
        parseError: '',
      }
    }
    const uncited = index === 1 ? `- We tidied up a few things along the way\n` : ''
    return {
      ...base,
      status: index === 0 || index === 1 ? 'draft' : 'published',
      body:
        `# ${epic} — ${week}\n\n## Closed\n- ${epic}.1 — reconciliation sweep — shipped in b64f334, verified by ${MOCK_RUN_ID} (${epic}.1)\n${uncited}` +
        `\n## Started\n- ${epic}.2 — weekly updates (${epic}.2)\n` +
        `\n## Discovered\n- loom-9dx — verify the apply path — discovered from ${epic}.1\n` +
        `\n## Reconciliation vs previous week\n- ${epic}.1 — listed in flight; closed this week (${epic}.1, commit b64f334).\n`,
      bodySha: `sha-${week}`,
      parseError: '',
    }
  })
}

const mockUpdates: Record<string, InitiativeUpdate[]> = {}

function historyFor(epic: string): InitiativeUpdate[] {
  mockUpdates[epic] ??= mockUpdateHistory(epic)
  return mockUpdates[epic]
}

export const mockClient: LoomClient = {
  health: () => delay<Health>({ status: 'ok', bdVersion: '1.1.2 (mock)' }),

  projects: () => delay(PROJECTS),

  discovery: () => delay(discoveryStatus),
  discoveryHide: (workdir) => {
    const project = PROJECTS.find((candidate) => candidate.workdir === workdir)
    if (project && !discoveryStatus.hidden.some((hidden) => hidden.workdir === workdir)) {
      discoveryStatus = {
        ...discoveryStatus,
        hidden: [...discoveryStatus.hidden, { workdir, name: project.name }],
      }
    }
    return delay(discoveryStatus)
  },
  discoveryUnhide: (workdir) => {
    discoveryStatus = {
      ...discoveryStatus,
      hidden: discoveryStatus.hidden.filter((hidden) => hidden.workdir !== workdir),
    }
    return delay(discoveryStatus)
  },
  discoveryRescan: () => delay({ added: [], removed: [] }),

  issues: (project: string, filter: IssueFilter = {}): Promise<IssueList> => {
    const all = BY_PROJECT[project]
    if (!all) return Promise.reject(new Error(`unknown project ${project}`))
    const wanted = filter.status?.split(',').filter(Boolean)
    const matching = wanted?.length
      ? all.filter((i) => wanted.includes(i.status))
      : // Matches the backend default: closed is excluded unless asked for.
        all.filter((i) => filter.includeClosed || i.status !== 'closed')
    // `total` counts what matches, and the page is cut from it — the backend's
    // countSelect ignores LIMIT/OFFSET but not the WHERE clause. A mock that
    // reported `all.length` against a closed-excluding page would show the
    // Backlog a permanent load-more button for rows it had already been given.
    const offset = filter.offset ?? 0
    const issues = matching.slice(offset, offset + (filter.limit ?? matching.length))
    return delay({ issues, total: matching.length })
  },

  issue: (project: string, id: string) => {
    const found = detail(project, id)
    return found ? delay(found) : Promise.reject(new Error(`issue ${id} not found`))
  },

  stats: (project: string) => delay(statsFor(BY_PROJECT[project] ?? [])),

  schema: () =>
    delay<ProjectSchema>({
      statuses: [...BUILTIN_SCHEMA.statuses, { name: 'in_review', category: 'active' }],
      types: [...BUILTIN_SCHEMA.types, 'rfc'],
    }),

  initiatives: (project: string, includeClosed: boolean) => {
    const all = BY_PROJECT[project] ?? []
    const children = new Map<string, Issue[]>()
    for (const candidate of all) {
      if (!candidate.parent) continue
      children.set(candidate.parent.id, [...(children.get(candidate.parent.id) ?? []), candidate])
    }
    const initiatives: Initiative[] = all
      .filter((candidate) => candidate.type === 'epic' && (includeClosed || candidate.status !== 'closed'))
      .map((epic) => {
        const descendants: Issue[] = []
        const seen = new Set([epic.id])
        const queue = [...(children.get(epic.id) ?? [])]
        for (let index = 0; index < queue.length; index++) {
          const child = queue[index]!
          if (seen.has(child.id)) continue
          seen.add(child.id)
          descendants.push(child)
          queue.push(...(children.get(child.id) ?? []))
        }
        const lastActivity = descendants.reduce(
          (latest, child) => Date.parse(child.updated) > Date.parse(latest) ? child.updated : latest,
          epic.updated,
        )
        const closed = descendants.filter((child) => child.status === 'closed').length
        const missingAcceptance = descendants.filter((child) => child.status === 'closed' && child.missingAcceptance).length
        const missingCloseReason = descendants.filter((child) => child.status === 'closed' && child.missingCloseReason).length
        const noReceipt = descendants.filter((child) => child.status === 'closed' && (child.missingAcceptance || child.missingCloseReason)).length
        return {
          id: epic.id,
          title: epic.title,
          status: epic.status,
          priority: epic.priority,
          assignee: epic.assignee,
          due: epic.due,
          updated: epic.updated,
          lastActivity,
          subtree: {
            total: descendants.length,
            closed,
            started: descendants.filter((child) => isActive(child.status)).length,
            ready: descendants.filter((child) => child.ready).length,
            depBlocked: descendants.filter((child) => child.notReady === 'deps').length,
            deferScheduled: descendants.filter((child) => child.notReady === 'deferred').length,
          },
          closeEligible:
            epic.status !== 'closed' && descendants.length > 0 && closed === descendants.length,
          health: fixtureInitiativeHealth(
            descendants.length,
            closed,
            epic.due,
          ),
          // Mirrors queries.NeedsTarget: open, has descendants, no date, and no
          // explicit opt-out label.
          needsTarget:
            epic.status !== 'closed' &&
            descendants.length > 0 &&
            !epic.due &&
            !epic.labels.includes(NO_TARGET_LABEL),
          noReceipt: {
            total: noReceipt,
            noAcceptance: missingAcceptance,
            noCloseReason: missingCloseReason,
          },
        }
      })
    return delay({ initiatives })
  },

  /* --- writes ------------------------------------------------------------
   * The mock mutates its in-memory store rather than no-op'ing, so drag and
   * drop, bulk edits and quick create are all exercisable with the backend
   * down. It also *rejects* the same things bd does (closing without a reason,
   * an unknown status), because the error paths are the ones most likely to be
   * wrong and least likely to be tested by hand.
   * --------------------------------------------------------------------- */

  patchIssue: (project, id, patch) => mutate(project, id, (issue) => applyMockPatch(issue, patch)),

  bulkPatch: (project, body: BulkPatchRequest): Promise<BulkResult> => {
    const ids = body.ids.filter(Boolean)
    if (ids.length === 0) return Promise.reject(new Error('ids must contain at least one issue id'))
    for (const id of ids) {
      const issue = find(project, id)
      if (issue) Object.assign(issue, applyMockPatch(issue, body.patch))
    }
    return delay({ updated: ids.length, ids })
  },

  bulkClose: (project, body): Promise<BulkResult> => {
    if (!body.reason.trim()) return Promise.reject(new Error('reason is required'))
    const ids = body.ids.filter(Boolean)
    for (const id of ids) {
      const issue = find(project, id)
      if (issue) Object.assign(issue, { status: 'closed', ready: false, closed: new Date().toISOString() })
    }
    return delay({ updated: ids.length, ids })
  },

  closeIssue: (project, id, reason) => {
    if (!reason.trim()) return Promise.reject(new Error('reason is required to close an issue'))
    return mutate(project, id, (issue) => ({
      ...issue,
      status: 'closed',
      ready: false,
      closed: new Date().toISOString(),
      closeReason: reason,
    }))
  },

  reopenIssue: (project, id) =>
    mutate(project, id, (issue) => ({ ...issue, status: 'open', ready: true, closed: null })),

  addComment: (project, id, text) => {
    if (!text.trim()) return Promise.reject(new Error('text is required'))
    mockComments[id] = [
      ...(mockComments[id] ?? []),
      { id: `c-${Object.keys(mockComments).length + 1}`, author: 'nate', text, created: new Date().toISOString() },
    ]
    return mutate(project, id, (issue) => issue)
  },

  createIssue: (project, body: CreateIssueRequest) => {
    if (!body.title.trim()) return Promise.reject(new Error('title is required'))
    const list = BY_PROJECT[project]
    if (!list) return Promise.reject(new Error(`unknown project ${project}`))
    const id = `${project}-new${(list.length + 1).toString(36)}`
    const parent = body.parent ? list.find((i) => i.id === body.parent) : undefined
    const created = issue({
      id,
      title: body.title,
      type: body.type ?? 'task',
      status: 'open',
      priority: (body.priority ?? 2) as Priority,
      labels: body.labels ?? [],
      estimate: body.estimate ?? 0,
      created: new Date().toISOString(),
      updated: new Date().toISOString(),
      ready: true,
      parent: parent ? { id: parent.id, title: parent.title } : null,
    })
    list.unshift(created)
    mockProse[id] = {
      description: body.description ?? '',
      acceptanceCriteria: body.acceptance ?? '',
      design: body.design ?? '',
      notes: body.notes ?? '',
    }
    return mutate(project, id, (i) => i)
  },

  addDep: () => delay(undefined),
  removeDep: () => delay(undefined),

  doltStatus: (project) =>
    delay(
      project === 'djinn'
        ? {
            dirty: true,
            ahead: 2,
            behind: 1,
            remote: 'origin',
            branch: 'main',
            lastCommit: {
              hash: 'lel6f6e6q0pi4ka4s1bdqv7at1vpe43u',
              message: 'bd: create djinn-aa5oc',
              when: ago(60 * 26),
            },
          }
        : {
            // This repo has no remote — ahead/behind are absent, not zero.
            dirty: true,
            remote: null,
            branch: 'main',
            lastCommit: {
              hash: 'kejl512jdnsr931eh483aoafffrjddu3',
              message: 'bd: update loom-0uh',
              when: ago(41),
            },
          },
    ),

  doltSync: (_project, verb) =>
    delay({ ok: true, output: `Dolt ${verb} complete.\nEverything up-to-date.\n` }),

  /* --- runs ---------------------------------------------------------------
   * Shapes copied from live /runs responses (internal/runs/model.go),
   * including the awkward ones the UI has to survive: an external run (no argv,
   * no Loom-side console capture), null cost caps, and a CRASHED verdict.
   * --------------------------------------------------------------------- */

  now: () =>
    delay({
      entries: LOOM_ISSUES.filter((i) => i.status === 'in_progress').map((issue, n) => ({
        project: 'loom',
        issue: {
          id: issue.id,
          title: issue.title,
          status: issue.status,
          priority: issue.priority,
          ready: issue.ready,
          assignee: issue.assignee,
          startedAt: ago(60 * (2 + n * 9)),
        },
        inStatusSince: ago(60 * (2 + n * 9)),
        stale: n === 1,
      })),
      skipped: ['Project-Pheonix'],
    }),

  runs: (project) =>
    delay({ runs: project === 'loom' ? [] : [MOCK_RUN, MOCK_EXTERNAL_RUN] }),

  run: (project, runId) => {
    const found = [MOCK_RUN, MOCK_EXTERNAL_RUN].find((r) => r.id === runId)
    if (!found || project === 'loom') return Promise.reject(new Error(`no run ${runId}`))
    return delay({
      ...found,
      activeSlots:
        found.state === 'running'
          ? [
              {
                bead: found.beads[0]!.id,
                spawnedAt: found.beads[0]!.spawnedAt,
                currentStep: {
                  loop: 'impl-loop',
                  step: 'impl',
                  iter: 2,
                  procedure: 'implement',
                  mtime: ago(1),
                },
              },
            ]
          : [],
      ...(found.source === 'loom' ? { logPath: '.djinn/summoner-logs/console-mock.log' } : {}),
    })
  },

  runLog: (_project, _runId, offset = 0) => {
    const text = MOCK_LOG.slice(offset)
    return delay({ text, offset: MOCK_LOG.length, eof: true })
  },

  forensics: (_project, bead) =>
    delay({
      bead,
      claimSha: '4f2a1c9e8b7d6a5f4e3c2b1a0987654321fedcba',
      timeline: [
        { loop: 'ac-quality-loop', step: 'ac-quality', iter: 1, procedure: 'ac-quality-review', mtime: ago(40) },
        { loop: 'impl-loop', step: 'impl', iter: 1, procedure: 'implement', mtime: ago(32) },
        { loop: 'impl-loop', step: 'impl', iter: 2, procedure: 'implement', mtime: ago(24) },
        { loop: 'review-impl-loop', step: 'review-impl', iter: 1, procedure: 'review-impl', mtime: ago(18) },
        { loop: 'review-tests-loop', step: 'review-tests', iter: 1, procedure: 'review-tests', mtime: ago(12) },
        { loop: 'verify-loop', step: 'verify', iter: 1, procedure: 'verify-impl', mtime: ago(6) },
      ],
      verdicts: [
        { loop: 'verify-loop', iter: 1, procedure: 'verify-impl', json: { verdict: 'pass', notes: 'all criteria met' } },
        { loop: 'review-impl-loop', iter: 1, procedure: 'review-impl', text: 'LGTM with one nit filed.' },
      ],
    }),

  summonPreview: (project, body) => {
    const all = BY_PROJECT[project] ?? []
    const asked = body.beads?.length ? body.beads : all.filter((i) => i.ready).map((i) => i.id)
    const known = asked.filter((id) => all.some((i) => i.id === id))
    return delay({
      ordered: known.slice(0, 4),
      warnings: asked
        .filter((id) => !known.includes(id))
        .map((bead) => ({ bead, reason: 'not in the ready set (unknown or not ready); skipping' })),
      // Mirrors the backend, which merges summoner's answer with a SQL check
      // for the needs-operator label.
      needsOperator: known.filter((id) =>
        all.find((i) => i.id === id)?.labels.includes('needs-operator'),
      ),
      preflight: Boolean(body.preflight),
    })
  },

  summon: (project, _body) =>
    delay({ runId: MOCK_RUN.id, run: { ...MOCK_RUN, project } }),

  stopRun: () => delay(undefined),

  // The mock has no evidence records; the section stays absent, as it does for
  // a bead no worker has touched.
  evidence: () => delay({ evidence: null }),
  spend: (project) => delay({ last7dUsd: project === 'loom' ? null : 4.12 }),

  graph: (project, opts = {}) => {
    const all = BY_PROJECT[project] ?? []
    const scoped = opts.epic
      ? all.filter((i) => i.id === opts.epic || i.parent?.id === opts.epic)
      : all
    const nodes = scoped
      .filter((i) => opts.includeClosed || i.status !== 'closed')
      .map((i) => ({
        id: i.id,
        title: i.title,
        status: i.status,
        priority: i.priority,
        type: i.type,
        ready: i.ready,
        labels: i.labels,
      }))
    const ids = new Set(nodes.map((n) => n.id))
    const edges: { from: string; to: string; type: string }[] = []
    for (const issue of scoped) {
      // parent-child: the child points at the parent, as in the dependencies table.
      if (issue.parent && ids.has(issue.parent.id) && ids.has(issue.id)) {
        edges.push({ from: issue.id, to: issue.parent.id, type: 'parent-child' })
      }
    }
    // A few synthetic blocks/discovered-from edges so every edge style renders.
    const order = nodes.map((n) => n.id)
    for (let i = 0; i + 1 < order.length && i < 6; i += 2) {
      edges.push({ from: order[i + 1]!, to: order[i]!, type: 'blocks' })
    }
    if (order.length > 3) edges.push({ from: order[3]!, to: order[0]!, type: 'discovered-from' })
    return delay({ nodes, edges, ...(opts.epic ? { epic: opts.epic } : {}) })
  },

  query: (project, q) => {
    if (q.includes('((')) return Promise.reject(new Error('parse error near "(("'))
    const all = BY_PROJECT[project] ?? []
    const issues = all.filter((i) => `${i.id} ${i.title} ${i.status}`.toLowerCase().includes(q.toLowerCase().replace(/^status=/, '')))
    return delay({ issues, total: issues.length, query: q })
  },

  stale: (project, days = 14) => {
    const all = BY_PROJECT[project] ?? []
    const issues = all
      .filter((i) => i.status === 'open' || i.status === 'in_progress')
      .slice(0, 3)
      .map((i, n) => ({ ...i, staleDays: days + 7 * (3 - n) }))
    return delay({ issues, days })
  },

  sweep: () =>
    delay({
      sweptAt: new Date().toISOString(),
      branch: 'main',
      window: 500,
      commitsScanned: 0,
      findings: [],
      partial: [],
      counts: {},
    }),
  initiativeUpdateSummaries: (project: string) => {
    const epics = (BY_PROJECT[project] ?? []).filter((issue) => issue.type === 'epic')
    const updates: InitiativeUpdateSummary[] = epics.slice(0, 2).map((epic) => {
      const history = historyFor(epic.id)
      return {
        epic: epic.id,
        total: history.length,
        draftCount: history.filter((u) => u.status === 'draft').length,
        latestWeek: history[0]?.week ?? '',
        latestStatus: history[0]?.status ?? '',
      }
    })
    return delay({ updates })
  },
  initiativeUpdates: (_project: string, epic: string) => {
    const updates = historyFor(epic)
    return delay({
      updates,
      knownIssueIds: [`${epic}.1`, `${epic}.2`, 'loom-9dx'],
      knownRunIds: [MOCK_RUN_ID],
    })
  },
  publishUpdate: (_project: string, epic: string, week: string) => {
    const history = historyFor(epic)
    const found = history.find((u) => u.week === week)
    if (found) found.status = 'published'
    return delay({ published: true, epic, week })
  },

  applyFinding: (_project, fingerprint) => delay({ applied: true, fingerprint }),
  dismissFinding: (_project, fingerprint) => delay({ dismissed: true, fingerprint }),
  undismissFinding: (_project, fingerprint) => delay({ dismissed: false, fingerprint }),
}

const MOCK_LOG = [
  'summoner: dispatching 3 bead(s) with 3 worker(s)',
  '✅ Done [ac-quality iter 1/3] (cost: $0.1204, ctx: 3%, dur=18.2s)',
  '✅ Done [implement iter 1/3] (cost: $1.8831, ctx: 11%, dur=4m02.7s)',
  '✅ Done [review-impl iter 1/3] (cost: $0.4419, ctx: 6%, dur=51.4s)',
  '✅ Done [verify-impl iter 2/3] (cost: $0.8135, ctx: 6%, dur=1m26.3s)',
  'summoner: worker for loom-3ft.5 exited COMPLETE (closed)',
].join('\n') + '\n'

const MOCK_RUN: Run = {
  id: MOCK_RUN_ID,
  project: 'djinn',
  source: 'loom',
  startedAt: ago(22),
  endedAt: null,
  state: 'running',
  args: ['--bead', 'djinn-b7k', '--max-workers', '3'],
  beads: [
    { id: 'djinn-b7k', spawnedAt: ago(22), exitedAt: null, retries: 0 },
    { id: 'djinn-c3m', spawnedAt: ago(20), exitedAt: ago(4), verdict: 'COMPLETE', finalStatus: 'closed', retries: 1 },
  ],
  costSoFar: 3.2589,
  caps: { maxCostUsdPerBead: 4, maxCostUsdPerSession: 12 },
}

// An external run: started at a terminal, reconstructed from djinn's own logs.
// No argv, no Loom console capture, and null caps — the real config.json has
// neither key.
const MOCK_EXTERNAL_RUN: Run = {
  id: 'ext-20260624T074233Z',
  project: 'djinn',
  source: 'external',
  startedAt: ago(60 * 30),
  endedAt: ago(60 * 27),
  state: 'finished',
  beads: [
    { id: 'djinn-gggc', spawnedAt: ago(60 * 30), exitedAt: ago(60 * 29), verdict: 'COMPLETE', finalStatus: 'closed', retries: 0 },
    { id: 'djinn-9sxi', spawnedAt: ago(60 * 29), exitedAt: ago(60 * 28), verdict: 'FAILED', finalStatus: 'operator-escalation halt (loop_exhausted)', retries: 2 },
    { id: 'djinn-n758', spawnedAt: ago(60 * 28), exitedAt: ago(60 * 27), verdict: 'CRASHED', finalStatus: 'open', retries: 0 },
  ],
  costSoFar: 5.74,
  caps: { maxCostUsdPerBead: null, maxCostUsdPerSession: null },
  summary: {
    iterations: 1,
    closed: 1,
    failures: 2,
    totalCostUsd: 5.74,
    peakContext: 0,
    ran: '9m47.9s',
    ranSeconds: 587.9,
    halt: 'iter 1: loop impl-loop exhausted',
    gateFailures: [],
    sandbagging: '(no mutation data)',
    dispatched: [
      { bead: 'djinn-gggc', verdict: 'COMPLETE' },
      { bead: 'djinn-9sxi', verdict: 'FAILED' },
      { bead: 'djinn-n758', verdict: 'CRASHED' },
    ],
    filedFollowUps: 3,
  },
}

/* --- mock write helpers --------------------------------------------------- */

/** Prose and comments written through the mock, keyed by issue id. */
const mockProse: Record<string, Partial<IssueDetail>> = {}
const mockComments: Record<string, IssueDetail['comments']> = {}

const find = (project: string, id: string): Issue | undefined =>
  (BY_PROJECT[project] ?? []).find((i) => i.id === id)

/** Apply a patch to a fixture issue with the same field semantics as bd. */
function applyMockPatch(issue: Issue, patch: IssuePatch): Issue {
  const labels = new Set(issue.labels)
  for (const l of patch.labelsAdd ?? []) labels.add(l)
  for (const l of patch.labelsRemove ?? []) labels.delete(l)
  return {
    ...issue,
    ...(patch.status !== undefined ? { status: patch.status } : {}),
    ...(patch.priority !== undefined ? { priority: patch.priority } : {}),
    ...(patch.assignee !== undefined ? { assignee: patch.assignee } : {}),
    ...(patch.title !== undefined ? { title: patch.title } : {}),
    ...(patch.type !== undefined ? { type: patch.type } : {}),
    ...(patch.estimate !== undefined ? { estimate: patch.estimate } : {}),
    labels: [...labels],
    updated: new Date().toISOString(),
  }
}

/** Mutate the store in place, then answer with the full detail shape the real
 *  endpoints return (they re-read after every write). */
function mutate(
  project: string,
  id: string,
  fn: (issue: Issue) => Issue,
): Promise<IssueDetail> {
  const list = BY_PROJECT[project]
  if (!list) return Promise.reject(new Error(`unknown project ${project}`))
  const index = list.findIndex((i) => i.id === id)
  if (index < 0) return Promise.reject(new Error(`issue ${id} not found`))
  list[index] = fn(list[index]!)
  const found = detail(project, id)
  if (!found) return Promise.reject(new Error(`issue ${id} not found`))
  return delay({
    ...found,
    ...mockProse[id],
    comments: [...found.comments, ...(mockComments[id] ?? [])],
  })
}
