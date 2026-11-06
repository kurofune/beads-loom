import type { Issue, Project } from '@/api/types'
import type { RecentIssue } from './recent'
import { rank, type Searchable } from './fuzzy'

/**
 * The command palette's action registry.
 *
 * An open list rather than a switch: phase 5 adds saved views, `bd query` and
 * the graph, and should be able to do that by appending entries instead of
 * editing the palette component. Every action is data — an id, how to find it,
 * and what to run.
 */
export type ActionGroup = 'Projects' | 'Recent' | 'Issues' | 'Views' | 'Actions'

export interface PaletteAction {
  id: string
  group: ActionGroup
  label: string
  /** Right-aligned context: a bead id, a project name, a shortcut. */
  hint?: string
  /** A short status word rendered as a dim tag, e.g. `closed`. */
  meta?: string
  /** Extra text the fuzzy matcher should consider, e.g. an id or a type. */
  keywords?: string
  /** Weighted higher for things the user names directly (ids, project names). */
  weight?: number
  /**
   * The server already matched this row against the live query, so the local
   * fuzzy pass may reorder it but must never drop it. See `searchActions`.
   */
  preMatched?: boolean
  run: () => void
}

export interface PaletteContext {
  projects: Project[]
  currentProject: string
  /** Which view the user is on, so project jumps preserve it. */
  currentView: string
  goProject: (project: string) => void
  /** A core view, `now`, or a view a mounted integration contributes. */
  goView: (view: string) => void
  /** Saved views for the current project, applied by name. */
  savedViews?: { id: string; name: string; hint: string }[]
  applySavedView?: (id: string) => void
  /** The current project's mounted integration commands (`paletteCommands`). */
  commands?: PaletteAction[]
  openCreate: () => void
  toggleTheme: () => void
  openHelp: () => void
  openSettings?: () => void
}

export function buildActions(ctx: PaletteContext): PaletteAction[] {
  const actions: PaletteAction[] = []

  for (const project of ctx.projects) {
    if (project.name === ctx.currentProject) continue
    actions.push({
      id: `project:${project.name}`,
      group: 'Projects',
      label: project.name,
      hint: project.reachable ? `${project.health?.open ?? 0} open` : 'unreachable',
      keywords: `go switch ${project.db}`,
      // Project names are short and typed deliberately; they should beat a
      // coincidental substring match inside a long issue title.
      weight: 1.4,
      run: () => ctx.goProject(project.name),
    })
  }

  actions.push(
    {
      id: 'view:initiatives',
      group: 'Views',
      label: 'Go to initiatives',
      hint: 'g i',
      keywords: 'epics progress rollup subtree',
      run: () => ctx.goView('initiatives'),
    },
    {
      id: 'view:backlog',
      group: 'Views',
      label: 'Go to backlog',
      hint: 'g l',
      keywords: 'list table',
      run: () => ctx.goView('backlog'),
    },
    {
      id: 'view:board',
      group: 'Views',
      label: 'Go to board',
      hint: 'g b',
      keywords: 'kanban columns',
      run: () => ctx.goView('board'),
    },
    {
      id: 'view:graph',
      group: 'Views',
      label: 'Go to the dependency graph',
      hint: 'g d',
      keywords: 'dag dependencies blockers frontier',
      run: () => ctx.goView('graph'),
    },
    {
      id: 'view:now',
      group: 'Views',
      label: 'Go to Now',
      hint: 'g n',
      keywords: 'agents working activity across projects',
      run: () => ctx.goView('now'),
    },
    {
      id: 'view:sweep',
      group: 'Views',
      label: 'Go to reconciliation sweep',
      keywords: 'git tracker reconcile findings audit divergence',
      run: () => ctx.goView('sweep'),
    },
    {
      id: 'action:create',
      group: 'Actions',
      label: 'New issue',
      hint: 'c',
      keywords: 'create add bead',
      run: ctx.openCreate,
    },
    {
      id: 'action:theme',
      group: 'Actions',
      label: 'Switch theme',
      hint: 't',
      keywords: 'dark light appearance',
      run: ctx.toggleTheme,
    },
    {
      id: 'action:settings',
      group: 'Actions',
      label: 'Settings',
      keywords: 'theme preferences default project appearance',
      run: () => ctx.openSettings?.(),
    },
    {
      id: 'action:help',
      group: 'Actions',
      label: 'Keyboard shortcuts',
      hint: '?',
      keywords: 'keys cheat sheet',
      run: ctx.openHelp,
    },
  )


  actions.push(...(ctx.commands ?? []))

  for (const view of ctx.savedViews ?? []) {
    actions.push({
      id: `view:saved:${view.id}`,
      group: 'Views',
      label: view.name,
      hint: view.hint,
      keywords: 'saved view filter',
      weight: 1.2,
      run: () => ctx.applySavedView?.(view.id),
    })
  }

  return actions
}

/**
 * The issue half of the registry: rows that come and go with the typed query.
 *
 * Split from `buildActions` because the two have different lifetimes and
 * different sources of truth. The static actions are derived from what the
 * shell already knows; these come from a server search scoped to the current
 * project, so the palette can reach a bead wherever it sorts (loom-nlm). The
 * old shape — handing `buildActions` a page of issues — meant "Jump to…" could
 * only ever find whatever happened to be in the first 200 rows.
 */
export interface IssueActionContext {
  /** Server search results for the live query, in server rank order. */
  issues: Issue[]
  /** Recently opened issues, most recent first, with the title from the visit. */
  recent: RecentIssue[]
  goIssue: (id: string) => void
}

export function issueActions(ctx: IssueActionContext): PaletteAction[] {
  const actions: PaletteAction[] = []

  // Recency first, and in its own group, so an empty query offers "where I
  // just was" instead of nothing. These need no search: the title was recorded
  // when the issue was opened.
  //
  // A bead can be both, though, and then the loop below drops its Issues row.
  // Whatever that row would have carried has to move onto the survivor, or
  // deduping a bead quietly costs it what the Issues row had: `preMatched`,
  // without which the local matcher can drop a bead the server matched, and
  // the `closed` tag, without which the row lies about the bead's status.
  const recent = new Set(ctx.recent.map((r) => r.id))
  const hits = new Map(ctx.issues.map((i) => [i.id, i]))
  for (const entry of ctx.recent) {
    const hit = hits.get(entry.id)
    actions.push({
      id: `recent:${entry.id}`,
      group: 'Recent',
      label: entry.title || entry.id,
      hint: entry.id,
      meta: hit?.status === 'closed' ? 'closed' : undefined,
      keywords: `${entry.id} recent`,
      weight: 1.15,
      // Only when the server actually returned it for this query: `preMatched`
      // is a claim about the live query, not about having once visited.
      preMatched: Boolean(hit),
      run: () => ctx.goIssue(entry.id),
    })
  }

  for (const issue of ctx.issues) {
    // A bead that is both recent and a search hit is one bead, not two rows.
    if (recent.has(issue.id)) continue
    actions.push({
      id: `issue:${issue.id}`,
      group: 'Issues',
      label: issue.title,
      hint: issue.id,
      // Search reaches closed work deliberately — you look a bead up by id
      // whatever its status — so the row has to say which it is.
      meta: issue.status === 'closed' ? 'closed' : undefined,
      keywords: `${issue.id} ${issue.type} ${issue.status}`,
      preMatched: true,
      run: () => ctx.goIssue(issue.id),
    })
  }

  return actions
}

/**
 * Rank actions for a query.
 *
 * An empty query is not a search — it is the menu you get for pressing Cmd+K,
 * so it shows the navigational actions rather than an arbitrary slice of the
 * backlog.
 *
 * The local matcher is a *subsequence* matcher and the server's is a per-word
 * substring matcher, so the two disagree in both directions: "board sample"
 * matches "Sample of the board" on the server and not here. Where they
 * disagree the server wins, because it saw the whole project and this list saw
 * one response — a `preMatched` row that fails the fuzzy pass is appended
 * rather than dropped (loom-nlm).
 */
export function searchActions(query: string, actions: PaletteAction[], limit = 24): PaletteAction[] {
  // An empty query is the menu you get for pressing Cmd+K, not a search over
  // every bead — but "where I just was" belongs in that menu.
  if (!query.trim()) return actions.filter((a) => a.group !== 'Issues').slice(0, limit)

  const searchable: Searchable<PaletteAction>[] = actions.map((action) => ({
    item: action,
    fields: [
      { text: action.label, weight: action.weight ?? 1 },
      // Ids and keywords match well but should not outrank the visible label.
      { text: action.hint ?? '', weight: (action.weight ?? 1) * 0.95 },
      { text: action.keywords ?? '', weight: 0.5 },
    ].filter((f) => f.text),
  }))

  const ranked = rank(query, searchable, limit).map((r) => r.item)
  if (ranked.length >= limit) return ranked

  const shown = new Set(ranked.map((a) => a.id))
  const unranked = actions.filter((a) => a.preMatched && !shown.has(a.id))
  return [...ranked, ...unranked].slice(0, limit)
}

/**
 * Below this a query is not a search, it is a keystroke: one letter matches
 * thousands of beads and ranks them meaninglessly. Lives here rather than in
 * the api layer because both the hook that gates the request and the footer
 * that explains the gate have to agree on the number.
 */
export const MIN_SEARCH_LENGTH = 2

export interface ScopeState {
  /** What the user has typed, live. */
  query: string
  project: string
  /** Issue rows actually drawn in the list. */
  shownIssues: number
  /** Issues in the project that match, per the server. */
  total: number
  /** Whether a search was actually issued (query long enough, project known). */
  active: boolean
  searching: boolean
  failed: boolean
}

/**
 * One line of footer prose about the issue search.
 *
 * This is where loom-nlm's honesty requirement lands. The board could hang
 * "showing 200 of 6,030" in its header; a palette has no header, and the row
 * list is the whole surface. So the footer carries it: how many matches were
 * drawn, out of how many exist. The number the user must be able to trust is
 * `total` — it comes from a COUNT over the project, not from the length of a
 * page — and when it exceeds what is on screen, the line says so instead of
 * letting the list imply it is complete.
 */
export function describeScope(s: ScopeState): string {
  const where = s.project || 'this project'
  if (s.failed) return 'Issue search failed — no issue results'
  if (!s.query.trim()) return `Type to search every issue in ${where}`
  if (!s.active) return `Type ${MIN_SEARCH_LENGTH} characters to search issues`
  if (s.searching) return `Searching every issue in ${where}…`
  if (s.total === 0) return `No issue in ${where} matches`
  if (s.shownIssues < s.total) return `Showing ${s.shownIssues} of ${s.total} matching issues`
  return `${s.total} matching issue${s.total === 1 ? '' : 's'}`
}

/** Group ordering in the rendered list. */
export const GROUP_ORDER: ActionGroup[] = ['Projects', 'Recent', 'Views', 'Actions', 'Issues']

export function groupActions(actions: PaletteAction[]): [ActionGroup, PaletteAction[]][] {
  const groups = new Map<ActionGroup, PaletteAction[]>()
  for (const action of actions) {
    const list = groups.get(action.group) ?? []
    list.push(action)
    groups.set(action.group, list)
  }
  return GROUP_ORDER.filter((g) => groups.has(g)).map((g) => [g, groups.get(g)!])
}
