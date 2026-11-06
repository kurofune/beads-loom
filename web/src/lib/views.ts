/** The per-project views core owns. Integrations add theirs through `views`. */
export const CORE_VIEWS = ['initiatives', 'backlog', 'board', 'graph', 'sweep'] as const

export type CoreView = (typeof CORE_VIEWS)[number]

/** A core view, or the path of a view a mounted integration contributes. */
export type ViewKind = CoreView | (string & {})

export const isCoreView = (view: string): view is CoreView =>
  (CORE_VIEWS as readonly string[]).includes(view)

/**
 * The view to open in another project when switching from `view`: the same
 * kind when the target has it, the backlog otherwise.
 */
export function portableView(view: string, targetPaths: readonly string[]): ViewKind {
  return isCoreView(view) || targetPaths.includes(view) ? view : 'backlog'
}
