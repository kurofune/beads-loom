import { describe, expect, it } from 'vitest'
import type { Issue, IssueStatus, Priority } from '@/api/types'
import { GROUP_MODES, type GroupMode, groupIssues, quickFilter, toRows } from './grouping'

let seq = 0
const mk = (over: Partial<Issue> = {}): Issue =>
  ({
    id: `loom-${(seq++).toString(36)}`,
    title: 'title',
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

describe('groupIssues by status', () => {
  it('orders groups by operational urgency, not alphabetically', () => {
    const groups = groupIssues(
      [
        mk({ status: 'closed' }),
        mk({ status: 'open' }),
        mk({ status: 'in_progress' }),
        mk({ status: 'deferred' }),
        mk({ status: 'blocked' }),
      ],
      'status',
    )
    expect(groups.map((g) => g.key)).toEqual([
      'in_progress',
      'blocked',
      'open',
      'deferred',
      'closed',
    ])
  })

  it('appends project-custom statuses instead of dropping them', () => {
    const groups = groupIssues([mk({ status: 'awaiting_review' }), mk({ status: 'open' })], 'status')
    expect(groups.map((g) => g.key)).toEqual(['open', 'awaiting_review'])
    expect(groups[1]!.label).toBe('Awaiting review')
  })

  it('omits groups with no issues', () => {
    expect(groupIssues([mk({ status: 'open' })], 'status').map((g) => g.key)).toEqual(['open'])
  })

  it('counts ready issues from the ready flag, not the status', () => {
    const group = groupIssues(
      [mk({ status: 'open', ready: true }), mk({ status: 'open', ready: false })],
      'status',
    )[0]!
    expect(group.issues).toHaveLength(2)
    expect(group.readyCount).toBe(1)
  })

  it('sorts within a group by priority, then readiness — a fresh touch does not lift a blocked bead', () => {
    const groups = groupIssues(
      [
        mk({ id: 'c', priority: 3, notReady: 'deps', updated: '2026-08-09T00:00:00Z' }),
        mk({ id: 'a', priority: 0, updated: '2026-07-01T00:00:00Z' }),
        mk({ id: 'b', priority: 3, ready: true, updated: '2026-08-02T00:00:00Z' }),
      ],
      'status',
    )
    expect(groups[0]!.issues.map((i) => i.id)).toEqual(['a', 'b', 'c'])
  })
})

describe('order within a group', () => {
  const idsIn = (issues: Issue[], key: string, mode: GroupMode = 'status') =>
    groupIssues(issues, mode)
      .find((g) => g.key === key)!
      .issues.map((i) => i.id)

  it('orders the Closed group by close time, newest first, with no priority term', () => {
    // Closed is a log, and logs read chronologically: what you finished last is
    // what you want to see first, however urgent the older work once was.
    expect(
      idsIn(
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
        'closed',
      ),
    ).toEqual(['minor-today', 'important-yesterday', 'urgent-last-week'])
  })

  it('reads the closed timestamp, not updated, for Closed order', () => {
    // A note, a label, or a reopen-then-reclose moves `updated` for reasons that
    // have nothing to do with finishing. Ordering on it would relitigate the log.
    expect(
      idsIn(
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
        'closed',
      ),
    ).toEqual(['finished-second', 'finished-first'])
  })

  it('sinks a closed issue with no close timestamp to the bottom of the Closed group', () => {
    // `closed` is nullable on the wire, so data drift can hand the backlog a row
    // with no place in the chronology. It has to land somewhere defined, and the
    // top — where the newest finish belongs — is the one wrong answer.
    expect(
      idsIn(
        [
          mk({ id: 'no-timestamp', status: 'closed', priority: 0, closed: null }),
          mk({ id: 'older', status: 'closed', priority: 3, closed: '2026-08-01T00:00:00Z' }),
          mk({ id: 'newer', status: 'closed', priority: 3, closed: '2026-08-15T00:00:00Z' }),
        ],
        'closed',
      ),
    ).toEqual(['newer', 'older', 'no-timestamp'])
  })

  it('keeps priority-first, then oldest-created, on every status group that is not Closed', () => {
    // The ids run counter to the expected order on purpose: alphabetical ids
    // would let a comparator that ignores priority and age entirely — the
    // Closed one, say — pass this by falling through to the id tiebreak.
    const perStatus = (status: IssueStatus) => [
      mk({ id: `${status}-a-cold`, status, priority: 3, created: '2026-07-01T00:00:00Z' }),
      mk({ id: `${status}-b-young`, status, priority: 0, created: '2026-08-09T00:00:00Z' }),
      mk({ id: `${status}-c-old`, status, priority: 0, created: '2026-08-01T00:00:00Z' }),
    ]
    const issues = [
      ...perStatus('open'),
      ...perStatus('in_progress'),
      ...perStatus('blocked'),
      ...perStatus('deferred'),
      ...perStatus('awaiting_review' as IssueStatus),
    ]

    for (const status of ['open', 'in_progress', 'blocked', 'deferred', 'awaiting_review']) {
      expect(idsIn(issues, status)).toEqual([
        `${status}-c-old`,
        `${status}-b-young`,
        `${status}-a-cold`,
      ])
    }
  })

  it('keeps work order, not close time, for closed issues in the priority and none modes', () => {
    // Grouping by priority or not at all is a different question than "what did
    // I finish last" — the Closed rule belongs to the Closed *group*, not to the
    // closed *status*, so it must not leak into the other two modes.
    const issues = [
      mk({
        id: 'a-p2-older',
        status: 'closed',
        priority: 2,
        created: '2026-06-01T00:00:00Z',
        closed: '2026-08-01T00:00:00Z',
      }),
      mk({
        id: 'b-p2-newer',
        status: 'closed',
        priority: 2,
        created: '2026-07-15T00:00:00Z',
        closed: '2026-08-15T00:00:00Z',
      }),
      mk({
        id: 'c-p0-oldest-close',
        status: 'closed',
        priority: 0,
        updated: '2026-07-01T00:00:00Z',
        closed: '2026-07-01T00:00:00Z',
      }),
    ]

    expect(idsIn(issues, 'all', 'none')).toEqual([
      'c-p0-oldest-close',
      'a-p2-older',
      'b-p2-newer',
    ])
    expect(idsIn(issues, 'p2', 'priority')).toEqual(['a-p2-older', 'b-p2-newer'])
  })

  it('breaks every remaining tie on issue id, so group order never depends on sort stability', () => {
    // Without a total order the rendered order is whatever Array.prototype.sort
    // happened to do with equal keys — so feed each comparator a full tie and
    // assert the id decides it, from input order that is already reversed.
    const tied = (over: Partial<Issue>) =>
      mk({ priority: 2, updated: '2026-08-01T00:00:00Z', ...over })
    const issues = [
      tied({ id: 'o-b', status: 'open' }),
      tied({ id: 'o-a', status: 'open' }),
      tied({ id: 'x-b', status: 'closed', closed: '2026-08-05T00:00:00Z' }),
      tied({ id: 'x-a', status: 'closed', closed: '2026-08-05T00:00:00Z' }),
      tied({ id: 'n-b', status: 'closed', closed: null }),
      tied({ id: 'n-a', status: 'closed', closed: null }),
    ]

    expect(idsIn(issues, 'open')).toEqual(['o-a', 'o-b'])
    expect(idsIn(issues, 'closed')).toEqual(['x-a', 'x-b', 'n-a', 'n-b'])
    // Mixed-status groups split on readiness first (open work above closed),
    // then the id decides each tie.
    expect(idsIn(issues, 'p2', 'priority')).toEqual(['o-a', 'o-b', 'n-a', 'n-b', 'x-a', 'x-b'])
    expect(idsIn(issues, 'all', 'none')).toEqual(['o-a', 'o-b', 'n-a', 'n-b', 'x-a', 'x-b'])
  })
})

describe('groupIssues by priority and none', () => {
  it('orders priority groups P0 first', () => {
    const groups = groupIssues([mk({ priority: 4 }), mk({ priority: 0 }), mk({ priority: 2 })], 'priority')
    expect(groups.map((g) => g.label)).toEqual(['P0', 'P2', 'P4'])
  })

  it('collapses to a single group when grouping is off', () => {
    const groups = groupIssues([mk(), mk()], 'none')
    expect(groups).toHaveLength(1)
    expect(groups[0]!.issues).toHaveLength(2)
  })

  it('returns no groups at all for an empty backlog', () => {
    expect(groupIssues([], 'none')).toEqual([])
    expect(groupIssues([], 'status')).toEqual([])
  })
})

describe('groupIssues by epic', () => {
  const epic = (id: string, title: string, over: Partial<Issue> = {}): Issue =>
    mk({ id, title, type: 'epic', ...over })
  /** A child, wired the way the list query hands it over: a ParentRef, not a type. */
  const under = (parent: Issue | { id: string; title: string }, over: Partial<Issue> = {}): Issue =>
    mk({ parent: { id: parent.id, title: parent.title }, ...over })

  it('offers epic alongside Status and Priority in the mode table', () => {
    const epicMode = GROUP_MODES.find((m) => m.id === 'epic')
    expect(epicMode).toBeDefined()
    expect(epicMode!.label).toBe('Epic')
    // None stays last: it is the "off" option, not a dimension.
    expect(GROUP_MODES.at(-1)!.id).toBe('none')
  })

  it('returns one group per epic, ordered by member count descending', () => {
    const small = epic('loom-sml', 'Small epic')
    const big = epic('loom-big', 'Big epic')
    const groups = groupIssues(
      [
        under(small, { id: 's1' }),
        under(big, { id: 'b1' }),
        under(big, { id: 'b2' }),
        under(big, { id: 'b3' }),
        small,
        big,
      ],
      'epic',
    )
    expect(groups.map((g) => g.key)).toEqual(['epic:loom-big', 'epic:loom-sml'])
    expect(groups[0]!.issues.map((i) => i.id)).toEqual(['loom-big', 'b1', 'b2', 'b3'])
    expect(groups[1]!.issues.map((i) => i.id)).toEqual(['loom-sml', 's1'])
  })

  it('breaks a member-count tie on epic id, so the order never depends on sort stability', () => {
    const a = epic('loom-aaa', 'Alpha')
    const b = epic('loom-bbb', 'Bravo')
    const groups = groupIssues([under(b), b, under(a), a], 'epic')
    expect(groups.map((g) => g.key)).toEqual(['epic:loom-aaa', 'epic:loom-bbb'])
  })

  it('carries the epic id, its title and the ready count on the group header', () => {
    const found = epic('loom-3ft', 'Foundations')
    const group = groupIssues(
      [found, under(found, { ready: true }), under(found, { ready: false })],
      'epic',
    )[0]!
    expect(group.epicId).toBe('loom-3ft')
    expect(group.label).toBe('Foundations')
    expect(group.readyCount).toBe(1)
    expect(group.issues).toHaveLength(3)
  })

  it('keeps a trailing labelled group for issues with no epic ancestor, even when it is the largest', () => {
    // On a 207-issue game backlog that group holds 35 issues — more than any single epic
    // — so count-descending ordering would float it to the top, and a grouping
    // built only from the epics it found would drop those 35 rows outright.
    const found = epic('loom-3ft', 'Foundations')
    const groups = groupIssues(
      [
        found,
        under(found, { id: 'child' }),
        mk({ id: 'orphan-a' }),
        mk({ id: 'orphan-b' }),
        mk({ id: 'orphan-c' }),
      ],
      'epic',
    )
    expect(groups.map((g) => g.key)).toEqual(['epic:loom-3ft', 'no-epic'])
    const last = groups.at(-1)!
    expect(last.label).toBe('No epic')
    expect(last.epicId).toBeUndefined()
    expect(last.issues.map((i) => i.id)).toEqual(['orphan-a', 'orphan-b', 'orphan-c'])
  })

  it('puts every issue in exactly one group, epics included', () => {
    const found = epic('loom-3ft', 'Foundations')
    const issues = [found, under(found, { id: 'child' }), mk({ id: 'orphan' })]
    const placed = groupIssues(issues, 'epic').flatMap((g) => g.issues.map((i) => i.id))
    expect(placed.sort()).toEqual(['child', 'loom-3ft', 'orphan'])
  })

  it('gives a nested epic its own group instead of folding it into its parent epic', () => {
    // Matches how InitiativeRollup treats a nested epic: it is an initiative in
    // its own right, not a row inside the one above it.
    const outer = epic('loom-out', 'Outer')
    const inner = epic('loom-inn', 'Inner', { parent: { id: outer.id, title: outer.title } })
    const groups = groupIssues(
      [outer, inner, under(outer, { id: 'direct' }), under(inner, { id: 'nested-child' })],
      'epic',
    )
    expect(groups.map((g) => g.key).sort()).toEqual(['epic:loom-inn', 'epic:loom-out'])
    const byKey = new Map(groups.map((g) => [g.key, g]))
    expect(byKey.get('epic:loom-inn')!.issues.map((i) => i.id).sort()).toEqual([
      'loom-inn',
      'nested-child',
    ])
    expect(byKey.get('epic:loom-out')!.issues.map((i) => i.id).sort()).toEqual([
      'direct',
      'loom-out',
    ])
  })

  it('walks past a non-epic parent to the nearest epic ancestor', () => {
    const found = epic('loom-3ft', 'Foundations')
    const feature = under(found, { id: 'feature', type: 'feature' })
    const task = under(feature, { id: 'task' })
    const groups = groupIssues([found, feature, task], 'epic')
    expect(groups).toHaveLength(1)
    expect(groups[0]!.issues.map((i) => i.id).sort()).toEqual(['feature', 'loom-3ft', 'task'])
  })

  it('lands an issue whose parent chain is broken in the no-epic group', () => {
    // The page is the only thing grouping can see: a closed epic filtered out of
    // the fetched rows leaves its children pointing at an id that is not there.
    // Guessing that the missing parent was an epic would invent a group whose
    // title nothing in hand can supply.
    const groups = groupIssues(
      [under({ id: 'loom-gone', title: 'Filtered-out epic' }, { id: 'dangling' })],
      'epic',
    )
    expect(groups.map((g) => g.key)).toEqual(['no-epic'])
    expect(groups[0]!.issues.map((i) => i.id)).toEqual(['dangling'])
  })

  it('does not hang on a parent cycle, and lands the cycle in the no-epic group', () => {
    const a = mk({ id: 'cycle-a', parent: { id: 'cycle-b', title: 'B' } })
    const b = mk({ id: 'cycle-b', parent: { id: 'cycle-a', title: 'A' } })
    const groups = groupIssues([a, b], 'epic')
    expect(groups.map((g) => g.key)).toEqual(['no-epic'])
    expect(groups[0]!.issues.map((i) => i.id)).toEqual(['cycle-a', 'cycle-b'])
  })

  it('pins the group\'s own epic first, even below-priority and not ready', () => {
    // The epic is the group's header made concrete: sorting it like any other
    // bead lands it mid-list, under children it is the parent of.
    const found = epic('loom-3ft', 'Foundations', { priority: 3 })
    const groups = groupIssues(
      [under(found, { id: 'a-p0-ready', priority: 0, ready: true }), found],
      'epic',
    )
    expect(groups[0]!.issues.map((i) => i.id)).toEqual(['loom-3ft', 'a-p0-ready'])
  })

  it('orders a group whose epic is absent from the rows purely by work order', () => {
    // Same expectation as the priority and none modes: priority, then
    // readiness, then age, then the id tiebreak — including for closed issues,
    // whose chronological rule belongs to the Closed *group*, not the status.
    // The epic is on the page but filtered out of the rows, so its group exists
    // with nothing to pin.
    const found = epic('loom-3ft', 'Foundations')
    const rows = [
      under(found, { id: 'a-p2-blocked', priority: 2, notReady: 'deps' }),
      under(found, { id: 'b-p2-ready', priority: 2, ready: true }),
      under(found, {
        id: 'c-p0-closed',
        priority: 0,
        status: 'closed',
        updated: '2026-07-01T00:00:00Z',
        closed: '2026-07-01T00:00:00Z',
      }),
    ]
    const groups = groupIssues(rows, 'epic', [...rows, found])
    expect(groups.map((g) => g.key)).toEqual(['epic:loom-3ft'])
    expect(groups[0]!.issues.map((i) => i.id)).toEqual([
      'c-p0-closed',
      'b-p2-ready',
      'a-p2-blocked',
    ])
  })

  it('returns no groups at all for an empty backlog', () => {
    expect(groupIssues([], 'epic')).toEqual([])
  })

  it('resolves ancestry against the whole page, not just the rows being grouped', () => {
    // The Backlog groups what survived the quick filter, the stale filter and
    // the hierarchy mode — and "Leaves" removes every epic by definition. Reading
    // ancestry from the narrowed rows would collapse the entire view into No
    // epic the moment a manager typed a letter.
    const found = epic('loom-3ft', 'Foundations')
    const child = under(found, { id: 'child' })
    const groups = groupIssues([child], 'epic', [found, child])
    expect(groups.map((g) => g.key)).toEqual(['epic:loom-3ft'])
    expect(groups[0]!.label).toBe('Foundations')
    expect(groups[0]!.issues.map((i) => i.id)).toEqual(['child'])
  })

  it('groups only the rows it was given, never pulling the wider page back in', () => {
    const found = epic('loom-3ft', 'Foundations')
    const shown = under(found, { id: 'shown' })
    const hidden = under(found, { id: 'filtered-out' })
    const groups = groupIssues([shown], 'epic', [found, shown, hidden])
    expect(groups[0]!.issues.map((i) => i.id)).toEqual(['shown'])
    expect(groups[0]!.readyCount).toBe(0)
  })
})

describe('quickFilter', () => {
  const issues = [
    mk({ id: 'loom-3ft.4', title: 'Filter bar', labels: ['phase-1'], assignee: 'nate' }),
    mk({ id: 'loom-yqh.4', title: 'Operator smoke-test', labels: ['needs-operator'], priority: 1 }),
  ]

  it('returns everything for an empty query', () => {
    expect(quickFilter(issues, '')).toHaveLength(2)
    expect(quickFilter(issues, '   ')).toHaveLength(2)
  })

  it('matches id, title, labels, assignee and priority shorthand', () => {
    expect(quickFilter(issues, '3ft')).toHaveLength(1)
    expect(quickFilter(issues, 'smoke')).toHaveLength(1)
    expect(quickFilter(issues, 'needs-operator')).toHaveLength(1)
    expect(quickFilter(issues, 'nate')).toHaveLength(1)
    expect(quickFilter(issues, 'p1')).toHaveLength(1)
  })

  it('ANDs multiple terms and ignores case', () => {
    expect(quickFilter(issues, 'OPERATOR SMOKE')).toHaveLength(1)
    expect(quickFilter(issues, 'operator nate')).toHaveLength(0)
  })
})

describe('toRows', () => {
  it('interleaves one header per group with its issues', () => {
    const groups = groupIssues([mk({ status: 'open' }), mk({ status: 'in_progress' })], 'status')
    const rows = toRows(groups, new Set())
    expect(rows.map((r) => r.kind)).toEqual(['group', 'issue', 'group', 'issue'])
  })

  it('keeps a collapsed group header but drops its rows', () => {
    const groups = groupIssues([mk({ status: 'open' }), mk({ status: 'open' })], 'status')
    const rows = toRows(groups, new Set(['open']))
    expect(rows).toHaveLength(1)
    expect(rows[0]!.kind).toBe('group')
  })

  it('gives every row a stable unique key for the virtualizer', () => {
    const groups = groupIssues([mk({ status: 'open' }), mk({ status: 'in_progress' })], 'status')
    const keys = toRows(groups, new Set()).map((r) => r.key)
    expect(new Set(keys).size).toBe(keys.length)
  })
})
