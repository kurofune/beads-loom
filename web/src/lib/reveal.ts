/**
 * Keeping a keyboard-focused element on screen.
 *
 * A focus move that the viewport does not follow is a focus move the user
 * cannot see: press `j` past the fold and the highlighted card is nowhere on
 * screen, so the board reads as if the key did nothing (loom-reg).
 *
 * The browser has `scrollIntoView({ block: 'nearest' })` for this, and the
 * palette and menu lists use it. The board cannot: its focused card sits inside
 * *two* scroll containers (its column's card list, and the board itself), the
 * horizontal one only ever moves on a column step, and — the reason this is
 * code rather than a one-liner — jsdom implements no layout at all, so a board
 * built on `scrollIntoView` could not be tested at the one thing that matters,
 * that the focused card ends up inside the container's rect. This computes the
 * same nearest-edge deltas the CSSOM `nearest` rule specifies, from rects, so
 * the geometry is a pure function with a test.
 */

/** One axis of a rect: `top`/`bottom`, or `left`/`right`. */
export interface Span {
  start: number
  end: number
}

/** The attribute a scroll container marks itself with to opt into `reveal`. */
export const SCROLL_CONTAINER = '[data-scroll]'

/**
 * How far to scroll so `box` sits inside `view` — moving by the nearest edge,
 * never re-centring.
 *
 * Zero when the box already fits, which is the whole point: a card the user can
 * already see must not be yanked to the middle of the column just because focus
 * landed on it. Positive scrolls the container forward (down / right).
 *
 * The four moving cases are the CSSOM-View `nearest` rule: align whichever edge
 * is out of view, except when the box is larger than the view, where aligning
 * the near edge would push the whole box out the other side — then the far edge
 * is the one that lands part of it on screen.
 */
export function nearestEdgeDelta(box: Span, view: Span): number {
  const before = box.start < view.start
  const after = box.end > view.end

  // Already spanning the view: either edge could be chased, and chasing one
  // only hides the other. Nothing is nearer than not moving.
  if (before && after) return 0
  if (!before && !after) return 0

  const bigger = box.end - box.start > view.end - view.start
  if (before) return bigger ? box.end - view.end : box.start - view.start
  return bigger ? box.start - view.start : box.end - view.end
}

/**
 * Scroll every marked container between `el` and the document so `el` is fully
 * visible in each, by the nearest edge.
 *
 * Rects are re-read at every level because scrolling the inner container moves
 * the element inside the outer one — the second delta must be measured after
 * the first scroll, not before it.
 */
export function reveal(el: Element | null | undefined): void {
  if (!el) return

  let node = el.parentElement?.closest<HTMLElement>(SCROLL_CONTAINER) ?? null
  while (node) {
    const view = node.getBoundingClientRect()
    const box = el.getBoundingClientRect()

    const dy = nearestEdgeDelta({ start: box.top, end: box.bottom }, { start: view.top, end: view.bottom })
    const dx = nearestEdgeDelta({ start: box.left, end: box.right }, { start: view.left, end: view.right })
    // Guarded so a container that is already showing the element is never
    // written to — an assignment of +0 still fires scroll handlers.
    if (dy !== 0) node.scrollTop += dy
    if (dx !== 0) node.scrollLeft += dx

    node = node.parentElement?.closest<HTMLElement>(SCROLL_CONTAINER) ?? null
  }
}
