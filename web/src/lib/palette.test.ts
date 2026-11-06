import { describe, expect, it, vi } from 'vitest'
import type { Issue, Project } from '@/api/types'
import { commandActions, mountIntegrations } from '@/integrations'
import { fuzzyMatch, rank } from './fuzzy'
import {
  buildActions,
  describeScope,
  groupActions,
  issueActions,
  searchActions,
  type PaletteContext,
  type ScopeState,
} from './palette'

describe('fuzzyMatch', () => {
  it('matches a subsequence and reports where', () => {
    const result = fuzzyMatch('kwc', 'kanban with counts')
    expect(result).not.toBeNull()
    expect(result!.matches).toHaveLength(3)
  })

  it('rejects a query that is not a subsequence', () => {
    expect(fuzzyMatch('zzz', 'kanban board')).toBeNull()
    expect(fuzzyMatch('nak', 'kanban')).toBeNull() // right letters, wrong order
  })

  it('is case-insensitive', () => {
    expect(fuzzyMatch('KANBAN', 'kanban board')).not.toBeNull()
  })

  it('treats an empty query as a match with no score', () => {
    expect(fuzzyMatch('', 'anything')).toEqual({ score: 0, matches: [] })
  })

  it('ranks an exact match above a prefix above a scattered match', () => {
    const exact = fuzzyMatch('loom-3ft', 'loom-3ft')!.score
    const prefix = fuzzyMatch('loom-3ft', 'loom-3ft.4')!.score
    const scattered = fuzzyMatch('loom-3ft', 'l o o m - 3 f t elsewhere')!.score
    expect(exact).toBeGreaterThan(prefix)
    expect(prefix).toBeGreaterThan(scattered)
  })

  it('rewards word starts, so "bw" finds "Board writes"', () => {
    const boundary = fuzzyMatch('bw', 'board writes')!.score
    const mid = fuzzyMatch('bw', 'abcdbwxyz')!.score
    expect(boundary).toBeGreaterThan(mid)
  })

  it('rewards consecutive characters', () => {
    expect(fuzzyMatch('kan', 'kanban')!.score).toBeGreaterThan(
      fuzzyMatch('kan', 'k a n')!.score,
    )
  })

  it('prefers the shorter of two otherwise equal haystacks', () => {
    expect(fuzzyMatch('3ft', 'loom-3ft')!.score).toBeGreaterThan(
      fuzzyMatch('3ft', 'loom-3ft.4')!.score,
    )
  })
})

describe('rank', () => {
  const items = [
    { item: 'a', fields: [{ text: 'kanban board', weight: 1 }] },
    { item: 'b', fields: [{ text: 'backlog table', weight: 1 }] },
    { item: 'c', fields: [{ text: 'kanban', weight: 1 }] },
  ]

  it('drops non-matches and orders by score', () => {
    expect(rank('kanban', items).map((r) => r.item)).toEqual(['c', 'a'])
  })

  it('honours field weights, so an id can outrank a title', () => {
    const weighted = [
      { item: 'title', fields: [{ text: 'loom project', weight: 1 }] },
      { item: 'id', fields: [{ text: 'loom', weight: 1.4 }] },
    ]
    expect(rank('loom', weighted)[0]!.item).toBe('id')
  })

  it('is stable for equal scores, so the list does not jitter as you type', () => {
    const same = [
      { item: 'first', fields: [{ text: 'same', weight: 1 }] },
      { item: 'second', fields: [{ text: 'same', weight: 1 }] },
    ]
    expect(rank('same', same).map((r) => r.item)).toEqual(['first', 'second'])
  })

  it('respects the limit', () => {
    expect(rank('a', items, 1)).toHaveLength(1)
  })
})

/* --- the registry --------------------------------------------------------- */

const project = (name: string, over: Partial<Project> = {}): Project =>
  ({
    name,
    db: name,
    workdir: `/tmp/${name}`,
    reachable: true,
    reason: '',
    integrations: {},
    health: { ready: 1, inProgress: 0, blocked: 0, open: 4, total: 9 },
    ...over,
  }) as Project

const issue = (id: string, title: string, over: Partial<Issue> = {}): Issue =>
  ({
    id,
    title,
    type: 'feature',
    status: 'open',
    priority: 2,
    assignee: '',
    labels: [],
    estimate: 0,
    created: '',
    updated: '',
    closed: null,
    due: null,
    deferUntil: null,
    ready: true,
    missingAcceptance: false,
    missingCloseReason: false,
    pinned: false,
    depCounts: { blockedBy: 0, blocks: 0 },
    parent: null,
    childStats: null,
    ...over,
  }) as Issue

const ctx = (over: Partial<PaletteContext> = {}): PaletteContext => ({
  projects: [project('loom'), project('djinn'), project('Project-Pheonix')],
  currentProject: 'loom',
  currentView: 'backlog',
  goProject: vi.fn(),
  goView: vi.fn(),
  openCreate: vi.fn(),
  toggleTheme: vi.fn(),
  openHelp: vi.fn(),
  ...over,
})

/** The two rows the palette's server search returns in most tests below. */
const results = [
  issue('loom-yqh.2', 'Kanban board with status columns and WIP counts'),
  issue('loom-3ft.4', 'Filter bar: status, type, priority, label, free text'),
]

const allActions = (over: Partial<PaletteContext> = {}, issues = results) => [
  ...buildActions(ctx(over)),
  ...issueActions({ issues, recent: [], goIssue: vi.fn() }),
]

describe('buildActions', () => {
  it('offers the other projects but not the one you are already in', () => {
    const names = buildActions(ctx())
      .filter((a) => a.group === 'Projects')
      .map((a) => a.label)
    expect(names).toEqual(['djinn', 'Project-Pheonix'])
  })

  it('marks an unreachable project instead of hiding it', () => {
    const actions = buildActions(
      ctx({ projects: [project('loom'), project('ghost', { reachable: false, health: null })] }),
    )
    expect(actions.find((a) => a.label === 'ghost')?.hint).toBe('unreachable')
  })

  it('carries no issues of its own — the corpus is the server, not this list', () => {
    // loom-nlm: the registry used to be handed a page of issues, which meant
    // the palette could only ever find whatever happened to be on page one.
    expect(buildActions(ctx()).filter((a) => a.group === 'Issues')).toHaveLength(0)
  })
})

describe('issueActions', () => {
  it('turns each search result into a jump, labelled by title and hinted by id', () => {
    const actions = issueActions({ issues: results, recent: [], goIssue: vi.fn() })
    expect(actions.map((a) => a.hint)).toEqual(['loom-yqh.2', 'loom-3ft.4'])
    expect(actions[0]!.label).toBe('Kanban board with status columns and WIP counts')
    expect(actions.every((a) => a.group === 'Issues')).toBe(true)
  })

  it('navigates to the issue it was built for', () => {
    const goIssue = vi.fn()
    issueActions({ issues: results, recent: [], goIssue })[1]!.run()
    expect(goIssue).toHaveBeenCalledWith('loom-3ft.4')
  })

  it('marks a closed result rather than offering it as if it were live', () => {
    // Search reaches closed work on purpose — a manager looking up a bead by
    // id usually wants it whatever its status — so the row has to say so.
    const [action] = issueActions({
      issues: [issue('loom-old', 'Shipped last month', { status: 'closed' })],
      recent: [],
      goIssue: vi.fn(),
    })
    expect(action!.meta).toBe('closed')
    expect(issueActions({ issues: results, recent: [], goIssue: vi.fn() })[0]!.meta).toBeUndefined()
  })

  it('builds Recent rows from the stored title, with no corpus lookup', () => {
    const actions = issueActions({
      issues: [],
      recent: [{ id: 'scale-030.200.9', title: 'A bead far below row 200' }],
      goIssue: vi.fn(),
    })
    expect(actions).toHaveLength(1)
    expect(actions[0]!.group).toBe('Recent')
    expect(actions[0]!.label).toBe('A bead far below row 200')
    expect(actions[0]!.hint).toBe('scale-030.200.9')
  })

  it('falls back to the id when an older recent entry has no title', () => {
    const [action] = issueActions({
      issues: [],
      recent: [{ id: 'loom-old', title: '' }],
      goIssue: vi.fn(),
    })
    expect(action!.label).toBe('loom-old')
  })

  it('lists a bead that is both recent and a search hit exactly once', () => {
    const actions = issueActions({
      issues: results,
      recent: [{ id: 'loom-3ft.4', title: 'Filter bar' }],
      goIssue: vi.fn(),
    })
    expect(actions.filter((a) => a.hint === 'loom-3ft.4')).toHaveLength(1)
    expect(actions.find((a) => a.hint === 'loom-3ft.4')!.group).toBe('Recent')
  })

  it('carries the server match onto the Recent row it kept in the dedup', () => {
    // The dedup drops the Issues row, so whatever that row carried has to
    // move onto the survivor — otherwise deduping a bead silently strips its
    // `preMatched` protection and the local matcher can drop it entirely.
    const actions = issueActions({
      issues: [issue('loom-old', 'Shipped last month', { status: 'closed' })],
      recent: [{ id: 'loom-old', title: 'Shipped last month' }],
      goIssue: vi.fn(),
    })
    expect(actions).toHaveLength(1)
    expect(actions[0]!.group).toBe('Recent')
    expect(actions[0]!.preMatched).toBe(true)
    expect(actions[0]!.meta).toBe('closed')
  })

  it('does not mark a recent bead the search did not return', () => {
    // `preMatched` means "the server matched this against the live query".
    // A recent bead the server did not return has no such claim behind it.
    const [action] = issueActions({
      issues: results,
      recent: [{ id: 'loom-elsewhere', title: 'Nowhere in this search' }],
      goIssue: vi.fn(),
    })
    expect(action!.preMatched).toBeFalsy()
    expect(action!.meta).toBeUndefined()
  })
})

describe('searchActions', () => {
  const actions = allActions()

  it('shows navigation, not the backlog, for an empty query', () => {
    // Cmd+K with no query is a menu, not a search over every bead.
    const empty = searchActions('', actions)
    expect(empty.some((a) => a.group === 'Issues')).toBe(false)
    expect(empty.some((a) => a.group === 'Projects')).toBe(true)
  })

  it('finds a project by name — the "go djinn" case from the work package', () => {
    expect(searchActions('djinn', actions)[0]!.label).toBe('djinn')
  })

  it('finds an issue by bead id', () => {
    expect(searchActions('yqh.2', actions)[0]!.hint).toBe('loom-yqh.2')
  })

  it('finds an issue by words from its title', () => {
    expect(searchActions('kanban wip', actions)[0]!.hint).toBe('loom-yqh.2')
  })

  it('ranks a project name above an issue that merely contains the letters', () => {
    const withDecoy = allActions({}, [issue('loom-x', 'Deal with the adjoining index')])
    expect(searchActions('djinn', withDecoy)[0]!.group).toBe('Projects')
  })

  it('finds actions by keyword rather than only by label', () => {
    expect(searchActions('kanban', actions).some((a) => a.id === 'view:board')).toBe(true)
  })

  it('returns nothing for a query that matches nothing', () => {
    // With no server results in hand there is genuinely nothing to offer —
    // the server, not the local matcher, decides whether an issue matched.
    expect(searchActions('qqqqzz', allActions({}, []))).toEqual([])
  })

  it('runs the action it was built with', () => {
    const goProject = vi.fn()
    const found = searchActions('djinn', buildActions(ctx({ goProject })))[0]!
    found.run()
    expect(goProject).toHaveBeenCalledWith('djinn')
  })

  it('keeps every server result rather than re-filtering the search away', () => {
    // The server already decided these rows match; the fuzzy pass only orders
    // them. A result the client silently drops is the old bug in miniature —
    // and the words here match the title out of order, which subsequence
    // matching alone would reject.
    const deep = issue('scale-030.200.9', 'Sample of the board, rendered deep')
    const found = searchActions('board sample', allActions({}, [deep]))
    expect(found.map((a) => a.hint)).toContain('scale-030.200.9')
  })

  it('keeps a server result the dedup moved into Recent', () => {
    // Same silent drop as above, one step further along: having also visited
    // the bead must not cost it the protection the Issues row would have had.
    const deep = issue('scale-030.200.9', 'Sample of the board, rendered deep')
    const found = searchActions('board sample', [
      ...buildActions(ctx()),
      ...issueActions({
        issues: [deep],
        recent: [{ id: 'scale-030.200.9', title: 'Sample of the board, rendered deep' }],
        goIssue: vi.fn(),
      }),
    ])
    expect(found.map((a) => a.hint)).toContain('scale-030.200.9')
  })
})

describe('groupActions', () => {
  it('orders groups navigation-first and omits empty ones', () => {
    const groups = groupActions(searchActions('', buildActions(ctx())))
    expect(groups.map(([g]) => g)).toEqual(['Projects', 'Views', 'Actions'])
  })
})

/* --- palette v2 (phase 5) ------------------------------------------------- */

describe('palette v2', () => {
  const withV2 = (over: Partial<PaletteContext> = {}, recentTitle = 'Filter bar') => [
    ...buildActions(
      ctx({
        savedViews: [{ id: 'v1', name: 'My P0s', hint: 'filter “p0”' }],
        applySavedView: vi.fn(),
        ...over,
      }),
    ),
    ...issueActions({
      issues: results,
      recent: [{ id: 'loom-3ft.4', title: recentTitle }],
      goIssue: vi.fn(),
    }),
  ]

  it('offers the graph, sweep and Now views', () => {
    const ids = withV2().map((a) => a.id)
    expect(ids).toContain('view:graph')
    expect(ids).toContain('view:sweep')
    expect(ids).toContain('view:now')
  })

  it('lists saved views, and applies one by id', () => {
    const applySavedView = vi.fn()
    const action = withV2({ applySavedView }).find((a) => a.id === 'view:saved:v1')!
    expect(action.label).toBe('My P0s')
    action.run()
    expect(applySavedView).toHaveBeenCalledWith('v1')
  })

  it('puts recent issues in their own group, ahead of the full list', () => {
    const actions = withV2()
    const recent = actions.filter((a) => a.group === 'Recent')
    expect(recent.map((a) => a.hint)).toEqual(['loom-3ft.4'])
    // …and does not also list it under Issues, which would be a duplicate row.
    expect(actions.filter((a) => a.hint === 'loom-3ft.4')).toHaveLength(1)
  })

  it('shows recent issues for an empty query — "where I just was" is the menu', () => {
    const empty = searchActions('', withV2())
    expect(empty.some((a) => a.group === 'Recent')).toBe(true)
    expect(empty.some((a) => a.group === 'Issues')).toBe(false)
  })

  it('still offers a recent bead the current search did not return', () => {
    // Pre-loom-nlm this row was dropped whenever the bead fell outside the
    // fetched page, which on a 6,030-issue project was almost always.
    const actions = [
      ...buildActions(ctx()),
      ...issueActions({
        issues: [],
        recent: [{ id: 'loom-vanished', title: 'Nowhere in this page' }],
        goIssue: vi.fn(),
      }),
    ]
    expect(actions.filter((a) => a.group === 'Recent')).toHaveLength(1)
  })

  it('orders groups navigation-first with Recent near the top', () => {
    const groups = groupActions(searchActions('', withV2())).map(([g]) => g)
    expect(groups.indexOf('Recent')).toBeLessThan(groups.indexOf('Actions'))
    expect(groups.indexOf('Projects')).toBeLessThan(groups.indexOf('Recent'))
  })

  it('still finds an issue by id when searching', () => {
    expect(searchActions('yqh.2', withV2())[0]!.hint).toBe('loom-yqh.2')
  })
})

/* --- the footer's honesty about the match set (loom-nlm) ------------------ */

describe('describeScope', () => {
  const state = (over: Partial<ScopeState> = {}): ScopeState => ({
    query: 'scale',
    project: 'scale',
    shownIssues: 3,
    total: 3,
    active: true,
    searching: false,
    failed: false,
    ...over,
  })

  it('says the search covers every issue before anything is typed', () => {
    expect(describeScope(state({ query: '', active: false, total: 0, shownIssues: 0 }))).toBe(
      'Type to search every issue in scale',
    )
  })

  it('says how many characters a search needs', () => {
    const text = describeScope(state({ query: 'l', active: false, total: 0, shownIssues: 0 }))
    expect(text).toBe('Type 2 characters to search issues')
  })

  it('names the true match count, not the number of rows drawn', () => {
    // The load-bearing line: a palette has no header to hang a truncation
    // notice on, so this footer is where "there are more" has to be said.
    expect(describeScope(state({ shownIssues: 24, total: 431 }))).toBe(
      'Showing 24 of 431 matching issues',
    )
  })

  it('states the count plainly when every match is on screen', () => {
    expect(describeScope(state({ shownIssues: 3, total: 3 }))).toBe('3 matching issues')
    expect(describeScope(state({ shownIssues: 1, total: 1 }))).toBe('1 matching issue')
  })

  it('says nothing matched rather than leaving the count blank', () => {
    expect(describeScope(state({ shownIssues: 0, total: 0 }))).toBe('No issue in scale matches')
  })

  it('reports a failed search instead of implying an empty project', () => {
    expect(describeScope(state({ failed: true, shownIssues: 0, total: 0 }))).toBe(
      'Issue search failed — no issue results',
    )
  })

  it('says it is still searching', () => {
    expect(describeScope(state({ searching: true }))).toBe('Searching every issue in scale…')
  })

  it('falls back to "this project" when none is named', () => {
    expect(describeScope(state({ project: '', query: '', active: false, total: 0 }))).toBe(
      'Type to search every issue in this project',
    )
  })
})

/* --- integration commands (loom-wvr) -------------------------------------- */

describe('palette commands come from the mounted integrations', () => {
  /** The palette for one project, its commands mounted exactly as the shell mounts them. */
  const paletteFor = (current: Project) => {
    const host = { currentProject: current.name, currentView: 'backlog', goView: vi.fn() }
    const actions = buildActions(
      ctx({
        projects: [current],
        currentProject: current.name,
        goView: host.goView,
        commands: commandActions(mountIntegrations(current).paletteCommands, host),
      }),
    )
    return { actions, labels: actions.map((a) => a.label), goView: host.goView }
  }

  it('lists no "Summon djinn" and no "Runs" command for a project without djinn', () => {
    const { labels } = paletteFor(project('plain', { integrations: {} }))
    expect(labels).not.toContain('Summon djinn')
    expect(labels.some((l) => /\bruns\b/i.test(l))).toBe(false)
  })

  it('lists both for a djinn project that can dispatch', () => {
    const { actions, labels, goView } = paletteFor(
      project('loom', { integrations: { djinn: { capabilities: ['runs', 'evidence', 'dispatch'] } } }),
    )
    expect(labels).toContain('Summon djinn')
    expect(labels).toContain('Go to runs')
    actions.find((a) => a.label === 'Go to runs')!.run()
    expect(goView).toHaveBeenCalledWith('runs')
  })

  it('offers runs but not summon when djinn cannot dispatch here', () => {
    const { labels } = paletteFor(
      project('loom', {
        integrations: { djinn: { capabilities: ['runs', 'evidence'], reasons: { dispatch: 'no summoner' } } },
      }),
    )
    expect(labels).toContain('Go to runs')
    expect(labels).not.toContain('Summon djinn')
  })
})

