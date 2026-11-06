import { describe, expect, it } from 'vitest'
import type { Issue, IssueStatus, Priority } from '@/api/types'
import {
  BOARD_COLUMNS,
  boardOrder,
  boardScope,
  buildLanes,
  countLabel,
  filterByHierarchy,
  headsLane,
  laneShowsColumns,
  locate,
  phaseOf,
  sampleSummary,
  showsReady,
  stepColumn,
  stepRow,
  visibleStatuses,
} from './board'

let n = 0
const mk = (over: Partial<Issue> = {}): Issue =>
  ({
    id: `i${n++}`,
    title: 't',
    type: 'task',
    status: 'open' as IssueStatus,
    priority: 2 as Priority,
    assignee: '',
    labels: [],
    estimate: 0,
    created: '2026-08-01T00:00:00Z',
    updated: '2026-08-01T00:00:00Z',
    closed: null,
    due: null,
    deferUntil: null,
    ready: false,
    pinned: false,
    depCounts: { blockedBy: 0, blocks: 0 },
    parent: null,
    childStats: null,
    ...over,
  }) as Issue

describe('boardScope', () => {
  it('is not truncated when the page holds every row the server matched', () => {
    expect(boardScope(43, 43).truncated).toBe(false)
  })

  it('is truncated when the server matched more rows than the page returned', () => {
    expect(boardScope(1000, 6030)).toEqual({ fetched: 1000, total: 6030, truncated: true })
  })

  // useIssues falls back to issues.length when the payload carries no total,
  // and a stale total can lag a shrinking page. Neither is evidence of missing
  // rows, and claiming truncation with nothing withheld would train the reader
  // to ignore the notice.
  it('is not truncated when the reported total is at or below the rows in hand', () => {
    expect(boardScope(200, 0).truncated).toBe(false)
    expect(boardScope(200, 199).truncated).toBe(false)
  })
})

describe('countLabel', () => {
  it('is a bare number when the board holds the whole project', () => {
    expect(countLabel(43, boardScope(43, 43))).toBe('43')
  })

  // The guard the bead is about: a count computed from a truncated page is a
  // count of the sample, and the label has to say so on its own.
  it('says the number describes the sample when the fetch was truncated', () => {
    expect(countLabel(43, boardScope(1000, 6030))).toBe('43 shown')
  })
})

describe('showsReady', () => {
  it('shows a non-zero ready figure on a complete board', () => {
    expect(showsReady(12, boardScope(43, 43))).toBe(true)
  })

  it('withholds it on a complete board when nothing is ready', () => {
    expect(showsReady(0, boardScope(43, 43))).toBe(false)
  })

  // "12 ready" is a claim about the project's startable work. Scoped to an
  // arbitrary first page it answers a question nobody asked, so it is withheld
  // rather than relabelled.
  it('withholds it entirely while the board is truncated', () => {
    expect(showsReady(12, boardScope(1000, 6030))).toBe(false)
  })
})

describe('sampleSummary', () => {
  it('names both halves so the reader can see how much is missing', () => {
    expect(sampleSummary(boardScope(1000, 6030))).toBe('Loaded 1,000 of 6,030 issues')
  })
})

describe('filterByHierarchy', () => {
  const standalone = mk({ id: 'standalone', parent: null, childStats: null })
  const epic = mk({ id: 'epic', parent: null, childStats: { total: 2, closed: 0, started: 0 } })
  const child = mk({
    id: 'child',
    parent: { id: 'epic', title: 'Epic' },
    childStats: null,
  })
  const issues = [standalone, epic, child]

  it('returns the input unchanged in all mode', () => {
    expect(filterByHierarchy(issues, 'all')).toBe(issues)
  })

  it('keeps only issues with no parent in top-level mode', () => {
    expect(filterByHierarchy(issues, 'top').map((i) => i.id)).toEqual(['standalone', 'epic'])
  })

  it('keeps only issues with no children in leaves mode', () => {
    expect(filterByHierarchy(issues, 'leaf').map((i) => i.id)).toEqual([
      'standalone',
      'child',
    ])
  })

  it('includes standalone issues in both top-level and leaves modes', () => {
    expect(filterByHierarchy([standalone], 'top')).toEqual([standalone])
    expect(filterByHierarchy([standalone], 'leaf')).toEqual([standalone])
  })

  it('shows a mid-level issue only in all mode', () => {
    const mid = mk({
      id: 'mid',
      parent: { id: 'grandparent', title: 'Grandparent' },
      childStats: { total: 1, closed: 0, started: 0 },
    })

    expect(filterByHierarchy([mid], 'all')).toEqual([mid])
    expect(filterByHierarchy([mid], 'top')).toEqual([])
    expect(filterByHierarchy([mid], 'leaf')).toEqual([])
  })

  it('leaves issues unchanged for an unknown runtime mode', () => {
    expect(filterByHierarchy(issues, 'banana' as never)).toBe(issues)
  })
})

describe('visibleStatuses', () => {
  it('always shows the flow columns', () => {
    expect(visibleStatuses([])).toEqual(BOARD_COLUMNS)
  })

  it('promotes a rare status to a column only when the project uses it', () => {
    expect(visibleStatuses([mk({ status: 'hooked' })])).toContain('hooked')
    expect(visibleStatuses([mk({ status: 'open' })])).not.toContain('hooked')
  })

  it('gives a project-custom status somewhere to live', () => {
    expect(visibleStatuses([mk({ status: 'awaiting_review' })])).toContain('awaiting_review')
  })

  it('withholds a column from a hidden status even when the project uses it', () => {
    // `deferred` is parked work and `pinned` is a standing reference note that
    // bd itself classes with `closed` — neither flows, and both would otherwise
    // fall through the custom-status branch and grow a column at the far right.
    expect(visibleStatuses([mk({ status: 'deferred' })])).toEqual(BOARD_COLUMNS)
    expect(visibleStatuses([mk({ status: 'pinned' })])).toEqual(BOARD_COLUMNS)
  })
})

describe('buildLanes', () => {
  it('puts each issue in its status column, sorted by work order', () => {
    const lanes = buildLanes(
      [
        mk({ id: 'low', status: 'open', priority: 3 }),
        mk({ id: 'hot', status: 'open', priority: 0 }),
        mk({ id: 'wip', status: 'in_progress' }),
      ],
      'none',
      BOARD_COLUMNS,
    )
    const [open, inProgress] = lanes[0]!.columns
    expect(open!.issues.map((i) => i.id)).toEqual(['hot', 'low'])
    expect(inProgress!.issues.map((i) => i.id)).toEqual(['wip'])
  })

  it('counts ready separately from open — the number the column header shows', () => {
    const lanes = buildLanes(
      [mk({ status: 'open', ready: true }), mk({ status: 'open', ready: false })],
      'none',
      BOARD_COLUMNS,
    )
    expect(lanes[0]!.columns[0]!.issues).toHaveLength(2)
    expect(lanes[0]!.columns[0]!.ready).toBe(1)
  })

  it('drops an issue whose status has no column, from the columns and the count', () => {
    // A lane count that includes cards you cannot see is a lie about the lane.
    const hidden = [mk({ id: 'parked', status: 'deferred' }), mk({ id: 'note', status: 'pinned' })]
    const lanes = buildLanes(
      [mk({ id: 'shown', status: 'open' }), ...hidden],
      'none',
      visibleStatuses(hidden),
    )
    expect(boardOrder(lanes)).toEqual(['shown'])
    expect(lanes[0]!.count).toBe(1)
  })

  it('groups by epic and sinks the catch-all lane to the bottom', () => {
    const lanes = buildLanes(
      [
        mk({ parent: { id: 'loom-b', title: 'B epic' } }),
        mk({ parent: null }),
        mk({ parent: { id: 'loom-a', title: 'A epic' } }),
      ],
      'epic',
      BOARD_COLUMNS,
    )
    expect(lanes.map((l) => l.title)).toEqual(['A epic', 'B epic', 'No epic'])
  })

  it('gives an epic card its own lane while true strays remain in No epic', () => {
    const lanes = buildLanes(
      [
        mk({
          id: 'epic',
          title: 'Epic title',
          parent: null,
          childStats: { total: 2, closed: 0, started: 0 },
        }),
        mk({ id: 'stray', parent: null, childStats: null }),
      ],
      'epic',
      BOARD_COLUMNS,
    )

    expect(lanes.map(({ key, title, subtitle }) => ({ key, title, subtitle }))).toEqual([
      { key: 'epic', title: 'Epic title', subtitle: 'epic' },
      { key: '__none__', title: 'No epic', subtitle: undefined },
    ])
    expect(lanes.flatMap((lane) => lane.columns.flatMap((column) => column.issues)))
      .not.toContainEqual(expect.objectContaining({ id: 'epic' }))
  })

  it('keeps a childless epic as an empty named lane without a No epic catch-all', () => {
    const epic = mk({ id: 'epic', title: 'Empty epic', type: 'epic', childStats: null })

    expect(headsLane(epic)).toBe(true)
    const lanes = buildLanes([epic], 'epic', BOARD_COLUMNS)
    expect(lanes.map(({ key, title, count }) => ({ key, title, count }))).toEqual([
      { key: 'epic', title: 'Empty epic', count: 0 },
    ])
    expect(boardOrder(lanes)).toEqual([])
  })

  it('gives every childless epic a collapsed zero-count lane without a No epic catch-all', () => {
    const lanes = buildLanes(
      [
        mk({ id: 'epic-a', title: 'Epic A', type: 'epic', childStats: null }),
        mk({ id: 'epic-b', title: 'Epic B', type: 'epic', childStats: null }),
      ],
      'epic',
      BOARD_COLUMNS,
    )

    expect(lanes.map(({ key, title, count }) => ({ key, title, count }))).toEqual([
      { key: 'epic-a', title: 'Epic A', count: 0 },
      { key: 'epic-b', title: 'Epic B', count: 0 },
    ])
    expect(lanes.some((lane) => lane.key === '__none__')).toBe(false)
    expect(lanes.every((lane) => !laneShowsColumns('epic', lane))).toBe(true)
  })

  it('uses the epic title whether the epic card or a child is encountered first', () => {
    const epic = mk({
      id: 'epic',
      title: 'Epic title',
      parent: null,
      childStats: { total: 1, closed: 0, started: 0 },
    })
    const child = mk({ id: 'child', parent: { id: 'epic', title: 'Epic title' } })

    expect(buildLanes([epic, child], 'epic', BOARD_COLUMNS)[0]!.title).toBe('Epic title')
    expect(buildLanes([child, epic], 'epic', BOARD_COLUMNS)[0]!.title).toBe('Epic title')
  })

  it('sorts the rendered cards normally and counts only those cards', () => {
    const lanes = buildLanes(
      [
        mk({
          id: 'epic',
          title: 'Epic title',
          status: 'open',
          priority: 4,
          parent: null,
          childStats: { total: 4, closed: 0, started: 0 },
        }),
        mk({
          id: 'hot-child',
          status: 'open',
          priority: 0,
          parent: { id: 'epic', title: 'Epic title' },
        }),
        mk({
          id: 'older-child',
          status: 'in_progress',
          priority: 2,
          created: '2026-08-01T00:00:00Z',
          parent: { id: 'epic', title: 'Epic title' },
        }),
        mk({
          id: 'newer-child',
          status: 'in_progress',
          priority: 2,
          created: '2026-08-02T00:00:00Z',
          parent: { id: 'epic', title: 'Epic title' },
        }),
        mk({ id: 'low-stray', status: 'open', priority: 3 }),
        mk({ id: 'hot-stray', status: 'open', priority: 0 }),
      ],
      'epic',
      BOARD_COLUMNS,
    )
    const epicLane = lanes.find((lane) => lane.key === 'epic')!
    const noEpicLane = lanes.find((lane) => lane.key === '__none__')!

    expect(epicLane.columns[0]!.issues.map((i) => i.id)).toEqual(['hot-child'])
    expect(epicLane.columns[1]!.issues.map((i) => i.id)).toEqual([
      'older-child',
      'newer-child',
    ])
    expect(epicLane.count).toBe(3)
    expect(noEpicLane.columns[0]!.issues.map((i) => i.id)).toEqual([
      'hot-stray',
      'low-stray',
    ])
  })

  it('excludes epic owners from keyboard walks only in epic-lane mode', () => {
    const issues = [
      mk({
        id: 'epic',
        type: 'epic',
        priority: 4,
        parent: null,
        childStats: { total: 2, closed: 0, started: 0 },
      }),
      mk({
        id: 'child-a',
        priority: 0,
        parent: { id: 'epic', title: 'Epic title' },
      }),
      mk({
        id: 'child-b',
        status: 'in_progress',
        priority: 1,
        parent: { id: 'epic', title: 'Epic title' },
      }),
    ]
    const lanes = buildLanes(issues, 'epic', BOARD_COLUMNS)

    expect(boardOrder(lanes)).toEqual(['child-a', 'child-b'])
    expect(stepRow(lanes, null, 1)).toBe('child-a')
    expect(stepColumn(lanes, 'child-a', 1)).toBe('child-b')
    expect(boardOrder(buildLanes(issues, 'none', BOARD_COLUMNS))).toContain('epic')
    expect(boardOrder(buildLanes(issues, 'phase', BOARD_COLUMNS))).toContain('epic')
  })

  it('keeps a non-epic parent task as an empty lane owner', () => {
    const lanes = buildLanes(
      [
        mk({
          id: 'grandparent',
          title: 'Grandparent',
          priority: 4,
          parent: null,
          childStats: { total: 1, closed: 0, started: 0 },
        }),
        mk({
          id: 'mid',
          title: 'Mid-level',
          priority: 0,
          parent: { id: 'grandparent', title: 'Grandparent' },
          childStats: { total: 1, closed: 0, started: 0 },
        }),
        mk({
          id: 'leaf',
          parent: { id: 'mid', title: 'Mid-level' },
          childStats: null,
        }),
      ],
      'epic',
      BOARD_COLUMNS,
    )
    const grandparentLane = lanes.find((lane) => lane.key === 'grandparent')!
    const midLane = lanes.find((lane) => lane.key === 'mid')!

    expect(grandparentLane.count).toBe(0)
    expect(grandparentLane.columns.flatMap((column) => column.issues)).toEqual([])
    expect(midLane.columns[0]!.issues.map((i) => i.id)).toEqual(['leaf'])
  })

  it('makes a nested epic its own header instead of a card in its ancestor lane', () => {
    const lanes = buildLanes(
      [
        mk({
          id: 'ancestor',
          title: 'Ancestor',
          type: 'epic',
          childStats: { total: 1, closed: 0, started: 0 },
        }),
        mk({
          id: 'nested',
          title: 'Nested',
          type: 'epic',
          parent: { id: 'ancestor', title: 'Ancestor' },
          childStats: { total: 1, closed: 0, started: 0 },
        }),
        mk({ id: 'leaf', parent: { id: 'nested', title: 'Nested' } }),
      ],
      'epic',
      BOARD_COLUMNS,
    )

    expect(lanes.find((lane) => lane.key === 'ancestor')?.count).toBe(0)
    expect(lanes.find((lane) => lane.key === 'nested')?.columns[0]?.issues.map((i) => i.id))
      .toEqual(['leaf'])
    expect(boardOrder(lanes)).not.toContain('nested')
    expect(lanes.map((lane) => lane.key)).not.toContain('__none__')
  })

  it('retains an empty epic lane when every child status is filtered out', () => {
    const lanes = buildLanes(
      [
        mk({
          id: 'epic',
          type: 'epic',
          childStats: { total: 1, closed: 1, started: 0 },
        }),
        mk({
          id: 'closed-child',
          status: 'closed',
          parent: { id: 'epic', title: 'Epic' },
        }),
      ],
      'epic',
      ['open'],
    )

    expect(lanes.map(({ key, count }) => ({ key, count }))).toEqual([{ key: 'epic', count: 0 }])
  })

  it('orders the Closed column by close time, newest first, with no priority term', () => {
    // Closed is a log, and logs read chronologically: what you finished last is
    // what you want to see first, however urgent the older work once was.
    const lanes = buildLanes(
      [
        mk({
          id: 'urgent-last-week',
          status: 'closed',
          priority: 0,
          closed: '2026-08-09T00:00:00Z',
        }),
        mk({ id: 'minor-today', status: 'closed', priority: 3, closed: '2026-08-16T00:00:00Z' }),
        mk({
          id: 'important-yesterday',
          status: 'closed',
          priority: 1,
          closed: '2026-08-15T00:00:00Z',
        }),
      ],
      'none',
      BOARD_COLUMNS,
    )

    expect(lanes[0]!.columns[3]!.issues.map((i) => i.id)).toEqual([
      'minor-today',
      'important-yesterday',
      'urgent-last-week',
    ])
  })

  it('reads the closed timestamp, not updated, for Closed order', () => {
    // A note, a label, or a reopen-then-reclose moves `updated` for reasons that
    // have nothing to do with finishing. Ordering on it would relitigate the log.
    const lanes = buildLanes(
      [
        mk({
          id: 'finished-first',
          status: 'closed',
          closed: '2026-08-10T00:00:00Z',
          updated: '2026-08-16T00:00:00Z',
        }),
        mk({
          id: 'finished-second',
          status: 'closed',
          closed: '2026-08-14T00:00:00Z',
          updated: '2026-08-14T00:00:00Z',
        }),
      ],
      'none',
      BOARD_COLUMNS,
    )

    expect(lanes[0]!.columns[3]!.issues.map((i) => i.id)).toEqual([
      'finished-second',
      'finished-first',
    ])
  })

  it('sinks a closed card with no close timestamp to the bottom of Closed', () => {
    // `closed` is nullable on the wire, so data drift can hand the board a card
    // with no place in the chronology. It has to land somewhere defined, and
    // the top — where the newest finish belongs — is the one wrong answer.
    const lanes = buildLanes(
      [
        mk({ id: 'no-timestamp', status: 'closed', priority: 0, closed: null }),
        mk({ id: 'older', status: 'closed', priority: 3, closed: '2026-08-01T00:00:00Z' }),
        mk({ id: 'newer', status: 'closed', priority: 3, closed: '2026-08-15T00:00:00Z' }),
      ],
      'none',
      BOARD_COLUMNS,
    )

    expect(lanes[0]!.columns[3]!.issues.map((i) => i.id)).toEqual([
      'newer',
      'older',
      'no-timestamp',
    ])
  })

  it('keeps priority-first, then oldest-created, on every column that is not Closed', () => {
    // The ids run counter to the expected order on purpose: alphabetical ids
    // would let a comparator that ignores priority and age entirely — the
    // Closed one, say — pass this by falling through to the id tiebreak.
    const perColumn = (status: IssueStatus) => [
      mk({ id: `${status}-a-cold`, status, priority: 3, created: '2026-07-01T00:00:00Z' }),
      mk({ id: `${status}-b-young`, status, priority: 0, created: '2026-08-09T00:00:00Z' }),
      mk({ id: `${status}-c-old`, status, priority: 0, created: '2026-08-01T00:00:00Z' }),
    ]
    const custom = perColumn('awaiting_review' as IssueStatus)
    const lanes = buildLanes(
      [...perColumn('open'), ...perColumn('in_progress'), ...perColumn('blocked'), ...custom],
      'none',
      visibleStatuses(custom),
    )

    expect(lanes[0]!.columns.map((c) => c.issues.map((i) => i.id))).toEqual([
      ['open-c-old', 'open-b-young', 'open-a-cold'],
      ['in_progress-c-old', 'in_progress-b-young', 'in_progress-a-cold'],
      ['blocked-c-old', 'blocked-b-young', 'blocked-a-cold'],
      [],
      ['awaiting_review-c-old', 'awaiting_review-b-young', 'awaiting_review-a-cold'],
    ])
  })

  it('breaks every remaining tie on issue id, so column order never depends on sort stability', () => {
    // Without a total order the rendered order is whatever Array.prototype.sort
    // happened to do with equal keys — so feed each comparator a full tie and
    // assert the id decides it, from input order that is already reversed.
    const tied = (over: Partial<Issue>) =>
      mk({ priority: 2, updated: '2026-08-01T00:00:00Z', ...over })
    const custom = tied({ id: 'c-b', status: 'awaiting_review' })
    const lanes = buildLanes(
      [
        tied({ id: 'o-b', status: 'open' }),
        tied({ id: 'o-a', status: 'open' }),
        tied({ id: 'w-b', status: 'in_progress' }),
        tied({ id: 'w-a', status: 'in_progress' }),
        tied({ id: 'b-b', status: 'blocked' }),
        tied({ id: 'b-a', status: 'blocked' }),
        tied({ id: 'x-b', status: 'closed', closed: '2026-08-05T00:00:00Z' }),
        tied({ id: 'x-a', status: 'closed', closed: '2026-08-05T00:00:00Z' }),
        tied({ id: 'n-b', status: 'closed', closed: null }),
        tied({ id: 'n-a', status: 'closed', closed: null }),
        custom,
        tied({ id: 'c-a', status: 'awaiting_review' }),
      ],
      'none',
      visibleStatuses([custom]),
    )

    expect(lanes[0]!.columns.map((c) => c.issues.map((i) => i.id))).toEqual([
      ['o-a', 'o-b'],
      ['w-a', 'w-b'],
      ['b-a', 'b-b'],
      ['x-a', 'x-b', 'n-a', 'n-b'],
      ['c-a', 'c-b'],
    ])
  })

  it('groups by the phase-* label', () => {
    const lanes = buildLanes(
      [mk({ labels: ['phase-2', 'layer-ui'] }), mk({ labels: ['layer-ui'] })],
      'phase',
      BOARD_COLUMNS,
    )
    expect(lanes.map((l) => l.title)).toEqual(['phase-2', 'No phase'])
  })

  it('reads the phase label, ignoring other labels', () => {
    expect(phaseOf(mk({ labels: ['layer-ui', 'phase-3'] }))).toBe('phase-3')
    expect(phaseOf(mk({ labels: ['layer-ui'] }))).toBeNull()
  })
})

describe('keyboard navigation', () => {
  const issues = [
    mk({ id: 'o1', status: 'open' }),
    mk({ id: 'o2', status: 'open' }),
    mk({ id: 'o3', status: 'open' }),
    mk({ id: 'w1', status: 'in_progress' }),
  ]
  const lanes = buildLanes(issues, 'none', BOARD_COLUMNS)

  it('walks column-major, the way the eye reads a board', () => {
    expect(boardOrder(lanes)).toEqual(['o1', 'o2', 'o3', 'w1'])
  })

  it('j and k move within a column and stop at the ends', () => {
    expect(stepRow(lanes, 'o1', 1)).toBe('o2')
    expect(stepRow(lanes, 'o3', 1)).toBe('o3')
    expect(stepRow(lanes, 'o1', -1)).toBe('o1')
  })

  it('j from nothing starts at the top; k from nothing starts at the bottom', () => {
    expect(stepRow(lanes, null, 1)).toBe('o1')
    expect(stepRow(lanes, null, -1)).toBe('w1')
  })

  it('arrows cross columns, keeping depth where the target is deep enough', () => {
    expect(stepColumn(lanes, 'o1', 1)).toBe('w1')
    // in_progress has one card, so row 2 clamps to row 0.
    expect(stepColumn(lanes, 'o3', 1)).toBe('w1')
    expect(stepColumn(lanes, 'w1', -1)).toBe('o1')
  })

  it('skips empty columns instead of swallowing the keypress', () => {
    // blocked sits between in_progress and closed and is empty.
    const withClosed = buildLanes(
      [...issues, mk({ id: 'c1', status: 'closed' })],
      'none',
      BOARD_COLUMNS,
    )
    expect(stepColumn(withClosed, 'w1', 1)).toBe('c1')
  })

  it('stays put at the edges of the board', () => {
    expect(stepColumn(lanes, 'o1', -1)).toBe('o1')
    expect(stepColumn(lanes, 'w1', 1)).toBe('w1')
  })

  it('locates a card by lane, column and row', () => {
    expect(locate(lanes, 'o2')).toEqual({ lane: 0, column: 0, row: 1 })
    expect(locate(lanes, 'nope')).toBeNull()
  })

  it('returns null rather than throwing on an empty board', () => {
    const none = buildLanes([], 'none', BOARD_COLUMNS)
    expect(stepRow(none, null, 1)).toBeNull()
    expect(stepColumn(none, null, 1)).toBeNull()
  })
})
