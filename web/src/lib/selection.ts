/**
 * Multi-select state for the backlog and the board.
 *
 * A reducer rather than a bag of useState calls because shift-range selection
 * needs an anchor, and anchor bookkeeping is exactly the kind of thing that
 * rots into "works unless you shift-click upward after a toggle". It is pure
 * and ordered-list-driven, so the same machine serves a flat table and a
 * multi-column board — the caller supplies whatever visual order it has.
 */
export interface SelectionState {
  ids: ReadonlySet<string>
  /** Where a shift-range measures from: the last individually-touched row. */
  anchor: string | null
}

export const emptySelection: SelectionState = { ids: new Set(), anchor: null }

export type SelectionAction =
  | { type: 'toggle'; id: string }
  | { type: 'only'; id: string }
  /** Shift-click / shift-x: select from the anchor to `id` in visual order. */
  | { type: 'range'; id: string; order: readonly string[] }
  | { type: 'all'; order: readonly string[] }
  | { type: 'clear' }
  /** Drop ids that no longer exist (filtered out, closed, project switched). */
  | { type: 'retain'; order: readonly string[] }

export function selectionReducer(
  state: SelectionState,
  action: SelectionAction,
): SelectionState {
  switch (action.type) {
    case 'toggle': {
      const ids = new Set(state.ids)
      if (ids.has(action.id)) ids.delete(action.id)
      else ids.add(action.id)
      // The anchor follows the last row the user touched by hand, even when
      // that touch was a deselect — that is where the next range starts.
      return { ids, anchor: action.id }
    }

    case 'only':
      return { ids: new Set([action.id]), anchor: action.id }

    case 'range': {
      const from = state.anchor ?? action.id
      const start = action.order.indexOf(from)
      const end = action.order.indexOf(action.id)
      // An anchor that has scrolled out of the filtered list degrades to a
      // plain toggle rather than selecting nothing.
      if (start < 0 || end < 0) return selectionReducer(state, { type: 'toggle', id: action.id })
      const [lo, hi] = start <= end ? [start, end] : [end, start]
      const ids = new Set(state.ids)
      for (const id of action.order.slice(lo, hi + 1)) ids.add(id)
      // The anchor stays put so a second shift-click re-measures from the same
      // origin, the way every file manager behaves.
      return { ids, anchor: state.anchor ?? action.id }
    }

    case 'all':
      return { ids: new Set(action.order), anchor: state.anchor }

    case 'clear':
      return emptySelection

    case 'retain': {
      const visible = new Set(action.order)
      const ids = new Set([...state.ids].filter((id) => visible.has(id)))
      if (ids.size === state.ids.size) return state // no change: keep identity
      return { ids, anchor: state.anchor && visible.has(state.anchor) ? state.anchor : null }
    }
  }
}

export const selectedIds = (state: SelectionState): string[] => [...state.ids]
