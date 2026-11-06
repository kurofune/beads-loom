/**
 * Staged-for-summon tray.
 *
 * Phase 2 builds the state machine; phase 4 wires the Summon button to the
 * summoner endpoints. Modelling it now means phase 4 changes one handler
 * instead of inventing the lifecycle under time pressure.
 *
 * Staging is a *suggestion*, not a commitment. Beads land here only by an
 * explicit gesture — the rail, `q`, the bulk bar, the detail panel — never as a
 * side effect of moving a card, which is what made staging undiscoverable
 * before loom-0n6. Every one of those gestures reverses, because a queue you
 * cannot back out of is one nobody experiments with. The tray must never block,
 * and dismissing it must never lose the underlying beads' state.
 */
export interface TrayState {
  /** Insertion-ordered: the tray reads as a history of what you just staged. */
  staged: readonly string[]
  /** The user closed the tray; it stays closed until something new is staged. */
  dismissed: boolean
  /** Staging is per project — switching projects starts a fresh tray. */
  project: string | null
}

export const emptyTray: TrayState = { staged: [], dismissed: false, project: null }

export type TrayAction =
  | { type: 'stage'; project: string; ids: readonly string[] }
  | { type: 'unstage'; id: string }
  | { type: 'dismiss' }
  | { type: 'clear' }
  /** Called on project change; a no-op when the project is unchanged. */
  | { type: 'project'; project: string }
  /**
   * The queue read back from storage. A tray already bound to another project
   * keeps its own state, as a project switch would reset the stored one.
   */
  | { type: 'restore'; stored: TrayState }

export function trayReducer(state: TrayState, action: TrayAction): TrayState {
  switch (action.type) {
    case 'stage': {
      const ids = action.ids.filter(Boolean)
      if (ids.length === 0) return state
      // Staging into a different project replaces rather than merges: a tray
      // mixing beads from two projects could not be summoned as one run.
      const base = state.project === action.project ? state.staged : []
      const merged = [...base]
      for (const id of ids) if (!merged.includes(id)) merged.push(id)
      // Nothing new: leave the state alone entirely. This is what stops a
      // re-staged id (a second drag of the same card into `open`) from
      // re-opening a tray the user has already dismissed.
      if (state.project === action.project && merged.length === state.staged.length) {
        return state
      }
      // Genuinely new work does re-open a dismissed tray — that is the point.
      return { staged: merged, dismissed: false, project: action.project }
    }

    case 'unstage': {
      if (!state.staged.includes(action.id)) return state
      const staged = state.staged.filter((id) => id !== action.id)
      // Emptying by hand is equivalent to dismissing.
      return staged.length === 0 ? { ...emptyTray, project: state.project } : { ...state, staged }
    }

    case 'dismiss':
      return state.dismissed ? state : { ...state, dismissed: true }

    case 'clear':
      return { ...emptyTray, project: state.project }

    case 'project':
      return state.project === action.project
        ? state
        : { ...emptyTray, project: action.project }

    case 'restore':
      return state.project === null || state.project === action.stored.project ? action.stored : state
  }
}

/**
 * Split a staging request into what may enter the queue and what may not.
 *
 * The only hard rule is "no closed work": summoning dispatches against the
 * ready frontier, and a closed bead in the queue is a summon that can never
 * happen. Everything else — including an id whose status the caller cannot
 * resolve — is allowed through, because every staging affordance operates on
 * issues it can see.
 */
/**
 * Which way `q` swings.
 *
 * True only when every target is already queued, so a press over a mixed
 * selection stages the stragglers rather than emptying the queue. The opposite
 * rule (unstage if *any* are staged) makes the key destructive in a way the
 * operator cannot predict by looking at the cards. No targets means no verb.
 */
export function allStaged(staged: readonly string[], targets: readonly string[]): boolean {
  return targets.length > 0 && targets.every((id) => staged.includes(id))
}

export function partitionStageable(
  ids: readonly string[],
  statusOf: (id: string) => string | undefined,
): { stageable: string[]; closed: string[] } {
  const stageable: string[] = []
  const closed: string[] = []
  for (const id of ids) {
    if (statusOf(id) === 'closed') closed.push(id)
    else stageable.push(id)
  }
  return { stageable, closed }
}

/**
 * What a reload gets back from sessionStorage.
 *
 * The store is outside our type system, so every field is re-validated rather
 * than trusted: junk staged entries are dropped, flags re-coerced, and a queue
 * with no project is discarded outright — ids only mean something inside one
 * project's id space.
 */
export function restoreTray(raw: string | null): TrayState {
  if (raw === null) return emptyTray
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return emptyTray
  }
  if (typeof parsed !== 'object' || parsed === null) return emptyTray
  const record = parsed as Record<string, unknown>
  const staged = Array.isArray(record.staged)
    ? [...new Set(record.staged.filter((id): id is string => typeof id === 'string'))]
    : []
  const project = typeof record.project === 'string' ? record.project : null
  if (staged.length > 0 && project === null) return emptyTray
  return { staged, dismissed: record.dismissed === true, project }
}

/** The tray shows only when it has something to say. */
export const trayVisible = (state: TrayState): boolean =>
  state.staged.length > 0 && !state.dismissed
