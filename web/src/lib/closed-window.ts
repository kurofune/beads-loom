import type { Issue } from '@/api/types'

/**
 * How much of the Closed column the board is willing to show.
 *
 * Closed is the one column with no natural ceiling — every column but this one
 * is bounded by how much work is actually in flight, while Closed grows for as
 * long as the project does. So it is the one column that needs a window, and
 * the window belongs on the column header rather than in a settings surface:
 * narrowing that is invisible is the "where did that bead go?" failure the
 * board already guards against elsewhere.
 *
 * Three windows, not four. Measured against this repo's own backlog on
 * 2026-08-16: Today=2, 7d=10, 30d=57, All=57 — a 30d chip would select exactly
 * what All selects and never change anything on screen, which is a control that
 * teaches you it does nothing.
 */
export type ClosedWindow = 'today' | '7d' | 'all'

export const CLOSED_WINDOWS: { id: ClosedWindow; label: string }[] = [
  { id: 'today', label: 'Today' },
  { id: '7d', label: '7d' },
  { id: 'all', label: 'All' },
]

/**
 * Opening the Closed column is opting into the column, not into unbounded
 * history — so the board starts narrowed. This is safe to default *because*
 * the chips are on the header showing their own state: the narrowing is a
 * visible affordance one click from All, not a silent rule.
 */
export const DEFAULT_CLOSED_WINDOW: ClosedWindow = '7d'

/** Calendar days each bounded window spans, counting the current day as one. */
const SPAN: Record<Exclude<ClosedWindow, 'all'>, number> = { today: 1, '7d': 7 }

/**
 * The first instant a window admits, in the *viewer's* zone, or null for All.
 *
 * Anchored on the calendar day rather than on a rolling `now - 24h`: at 00:01
 * a rolling window would make "Today" mean "most of yesterday", and a manager
 * reading "Today" is reading a date, not a duration. Constructing the boundary
 * through the local Date constructor is also what makes it survive a DST shift
 * — subtracting `6 * 86_400_000` from a timestamp lands an hour off twice a
 * year, and an hour off at midnight is a whole day wrong.
 */
export function closedWindowStart(
  window: ClosedWindow,
  now: Date | number = Date.now(),
): number | null {
  if (window === 'all') return null
  const d = new Date(now)
  return new Date(d.getFullYear(), d.getMonth(), d.getDate() - (SPAN[window] - 1)).getTime()
}

/**
 * Whether a close timestamp falls inside the window.
 *
 * Deliberately open at the top end. A close time in the future is clock skew
 * between the Dolt server that wrote it and the browser reading it, and a
 * bounded upper edge would drop such a card out of Today *and* 7d at once,
 * leaving it findable only under All — the disappearance this window is
 * supposed to avoid. A timestamp that cannot be placed in time at all (absent,
 * or unparseable) is not *known* to be in a bounded window, so it shows under
 * All and nowhere else.
 */
export function inClosedWindow(
  closed: string | null | undefined,
  window: ClosedWindow,
  now: Date | number = Date.now(),
): boolean {
  const start = closedWindowStart(window, now)
  if (start === null) return true
  if (!closed) return false
  const t = Date.parse(closed)
  return Number.isNaN(t) ? false : t >= start
}

/**
 * Apply the window to a board's issues.
 *
 * Only `closed` rows are candidates — the window is a property of the Closed
 * column, and an open card has no close timestamp to judge. Run this in the
 * same pipeline position as the quick filter and the hierarchy filter, i.e.
 * BEFORE the lanes are built, so the header count, the keyboard walk order and
 * the lane totals all describe the cards actually on screen.
 *
 * Order is preserved and never imposed: the column comparator
 * (`sortFor('closed')`) owns Closed ordering, and this must not compete with it.
 */
export function filterByCloseWindow(
  issues: Issue[],
  window: ClosedWindow,
  now: Date | number = Date.now(),
): Issue[] {
  // Identity, not a copy: All is the no-op window, and returning a fresh array
  // would invalidate every downstream memo on every render for no change.
  if (window === 'all') return issues
  return issues.filter((i) => i.status !== 'closed' || inClosedWindow(i.closed, window, now))
}
