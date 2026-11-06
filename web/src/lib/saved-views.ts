import type { GroupMode } from './grouping'
import type { HierarchyMode } from './board'

/**
 * Saved views — a named backlog configuration, per project.
 *
 * Deliberately localStorage rather than the database. A saved view is a
 * *personal* lens on someone else's shared backlog: "my P0s", "the UI layer",
 * "what's stale". Writing those into the project's beads db would push one
 * person's habits onto everyone reading the same repository, and there is no
 * bd verb for it anyway.
 *
 * Keyed by project, because a view named "mine" means something different in
 * each one and the ids inside a filter are project-scoped.
 */
export interface SavedView {
  id: string
  name: string
  /** Free-text quick filter, or a raw `bd query` when `raw` is set. */
  query: string
  /** The query is a bd query expression rather than a substring filter. */
  raw: boolean
  group: GroupMode
  includeClosed: boolean
  staleOnly: boolean
  /** undefined on records saved before the toggle existed → treat as 'all'. */
  hierarchy?: HierarchyMode
  /** For ordering; also lets the UI show "saved 3d ago". */
  createdAt: string
}

export type SavedViewInput = Omit<SavedView, 'id' | 'createdAt'>

const KEY_PREFIX = 'loom.views.'
const MAX_PER_PROJECT = 40

const keyFor = (project: string) => `${KEY_PREFIX}${project}`

/**
 * Read a project's views.
 *
 * Every failure path returns `[]` rather than throwing: a corrupt entry, a
 * schema change, Safari private mode. A saved view is a convenience, and losing
 * one must never take the backlog down with it.
 */
export function loadViews(project: string): SavedView[] {
  if (!project) return []
  try {
    const raw = localStorage.getItem(keyFor(project))
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter(isSavedView)
  } catch {
    return []
  }
}

function isSavedView(value: unknown): value is SavedView {
  if (!value || typeof value !== 'object') return false
  const v = value as Record<string, unknown>
  return typeof v.id === 'string' && typeof v.name === 'string' && typeof v.query === 'string'
}

function persist(project: string, views: SavedView[]): SavedView[] {
  try {
    localStorage.setItem(keyFor(project), JSON.stringify(views))
  } catch {
    // Quota or private mode. The in-memory list still works for this session.
  }
  return views
}

/**
 * Save a view, replacing any existing one with the same name.
 *
 * Replacing rather than appending is the behaviour people expect from "save":
 * re-saving "my P0s" after tweaking a filter should update it, not leave two
 * entries with the same name and different contents.
 */
export function saveView(project: string, input: SavedViewInput, now = Date.now()): SavedView[] {
  const name = input.name.trim()
  if (!name) return loadViews(project)

  const view: SavedView = {
    ...input,
    name,
    id: `${now.toString(36)}-${name.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`,
    createdAt: new Date(now).toISOString(),
  }
  const rest = loadViews(project).filter((v) => v.name.toLowerCase() !== name.toLowerCase())
  return persist(project, [view, ...rest].slice(0, MAX_PER_PROJECT))
}

export function deleteView(project: string, id: string): SavedView[] {
  return persist(
    project,
    loadViews(project).filter((v) => v.id !== id),
  )
}

export function clearViews(project: string): SavedView[] {
  return persist(project, [])
}

/** Does the current backlog configuration match a saved view? */
export function matchesView(view: SavedView, current: SavedViewInput): boolean {
  return (
    view.query === current.query &&
    view.raw === current.raw &&
    view.group === current.group &&
    view.includeClosed === current.includeClosed &&
    view.staleOnly === current.staleOnly &&
    (view.hierarchy ?? 'all') === (current.hierarchy ?? 'all')
  )
}

/** A short description for the sidebar row's tooltip. */
export function describeView(view: SavedView): string {
  const parts: string[] = []
  if (view.raw && view.query) parts.push(`bd query: ${view.query}`)
  else if (view.query) parts.push(`filter “${view.query}”`)
  if (view.group !== 'status') parts.push(`grouped by ${view.group}`)
  if (view.includeClosed) parts.push('including closed')
  if (view.staleOnly) parts.push('stale only')
  if (view.hierarchy === 'top') parts.push('top-level only')
  if (view.hierarchy === 'leaf') parts.push('leaves only')
  return parts.length ? parts.join(' · ') : 'the default backlog'
}
