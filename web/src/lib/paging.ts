import type { IssueList } from '@/api/types'

/**
 * Backlog paging maths — pure, so the page sizes, the offset arithmetic and
 * every sentence the footer prints are testable without a DOM or a server.
 *
 * The Backlog is the one view that can actually carry a project's whole result
 * set: it is virtualized, so 6,030 rows cost the same to render as 30. What it
 * could not do until loom-7qh was *fetch* them — one request went out, the
 * server capped it, and any epic subtree past the cap was silently a first page
 * (loom-g1v.2 made the footer say so; this module is what lets the reader act
 * on it). The board took the other trade — one big page plus a sample notice
 * (`board.ts`) — because a kanban of 6,030 cards is not a readable artifact at
 * any page size.
 */

/**
 * The first page, matching `queries.DefaultLimit` (internal/queries/filter.go).
 *
 * Sent explicitly rather than left to the server's default: the offset for page
 * two is this view's arithmetic, so the number it is arithmetic *about* has to
 * be this view's too.
 */
export const FIRST_PAGE = 200

/**
 * Every page after the first, at `queries.MaxLimit` — the largest page the
 * server honours verbatim (above it, `Filter.Page` silently clamps).
 *
 * Asymmetric on purpose. The first page is about time to first paint, so it
 * stays small; every later page is a reader who has already said "give me the
 * rest", so it is about getting there — six clicks to the end of a 6,030-row
 * project rather than thirty.
 */
export const NEXT_PAGE = 1000

/** How many rows to ask for, given how many are already in hand. */
export function pageSizeAt(offset: number): number {
  return offset === 0 ? FIRST_PAGE : NEXT_PAGE
}

/**
 * Rows the accumulated pages can actually put on screen — the same figure the
 * footer prints, because `useIssues` dedupes the pages before counting them.
 *
 * Raw row count and reachable row count are the same number until a seam
 * returns a row already held, and every place they are allowed to drift apart
 * is a place the footer and the table stop describing the same set.
 */
function reachableCount(pages: readonly IssueList[]): number {
  const seen = new Set<string>()
  for (const p of pages) for (const row of p.issues) seen.add(row.id)
  return seen.size
}

/**
 * Where the next request starts, or `undefined` when there is nothing left.
 *
 * Counts rows *received* rather than rows requested, so a short page — the
 * server clamped, or rows closed out from under the reader between requests —
 * cannot leave a hole in the sequence. The total comes from the freshest page
 * for the same reason: the backlog moves while it is being read, and the count
 * that arrived with the newest rows is the one that describes them.
 *
 * Rows received are counted the way the footer counts them: once each. A
 * LIMIT/OFFSET seam has no snapshot, so page two can open on a row page one
 * already held, and against the *raw* count 312 rows cover a total of 312
 * while the table can only reach 311 of them — paging stopped with the footer
 * reading "311 of 312 loaded" and the control that would have fetched the
 * missing row already gone (loom-rz0). Naming the same number in both places
 * is what lets them converge instead of stopping one short.
 */
export function nextOffset(pages: readonly IssueList[]): number | undefined {
  const last = pages[pages.length - 1]
  // Nothing fetched yet, or the server had nothing left to give: asking again
  // at the same offset would spin forever against a shrinking backlog.
  if (!last || last.issues.length === 0) return undefined
  const reached = reachableCount(pages)
  if (reached >= last.total) return undefined
  // The last page was all rows already in hand. The offset it implies is the
  // offset that page already came from, so asking again would spin exactly as
  // an empty page would; `stalled` is what tells the view to offer the refetch
  // that re-reads the pages from the start, which is where the seam hid the row.
  if (pages.length > 1 && reachableCount(pages.slice(0, -1)) === reached) return undefined
  return reached
}

/**
 * Forward paging gave up while the server still counts rows the table cannot
 * reach — the one state where `nextOffset` returning `undefined` does NOT mean
 * "everything is in hand".
 *
 * The view needs the two apart: converged means the load-more control has done
 * its job and goes away, stalled means the reader is short of the total with
 * no forward request that can close the gap, and hiding that is the SCARS.md
 * "invisible affordances" failure rather than a fix for it.
 */
export function stalled(pages: readonly IssueList[]): boolean {
  const last = pages[pages.length - 1]
  if (!last) return false
  return nextOffset(pages) === undefined && reachableCount(pages) < last.total
}

const n = (v: number) => v.toLocaleString('en-US')

/**
 * How many rows one more click actually brings back.
 *
 * Exported because the label was not the only sentence on that control: the
 * tooltip beside it named `total - loaded` unclamped, so the same button read
 * "Load 1,000 more" and promised "the same 5,830 outstanding rows" (loom-rz0).
 * One number with one caller each is a drift trap; one number with one
 * definition is not.
 */
export function nextPageSize(loaded: number, total: number): number {
  return Math.min(NEXT_PAGE, Math.max(0, total - loaded))
}

/**
 * The load-more control's label.
 *
 * Names the exact number of rows the click will pull, never the number
 * outstanding: a button reading "Load 5,830 more" that returns 1,000 of them is
 * a promise the server will not keep.
 */
export function loadMoreLabel(loaded: number, total: number): string {
  return `Load ${n(nextPageSize(loaded, total))} more`
}

export interface CountState {
  /** Rows the table can put on screen right now, after client predicates. */
  shown: number
  /** Rows fetched from the server so far. */
  loaded: number
  /** Rows the server says match the same request. */
  total: number
}

/**
 * The status line's sentence, in the four states the Backlog actually has.
 *
 * The distinction that matters is `loaded` vs `total`. While they differ, every
 * client-side number on screen describes an arbitrary first page, so the label
 * says "loaded" and keeps the server's figure beside it — a reader who has
 * filtered 312 rows down to 1 must be able to see that 112 rows the filter
 * never looked at are still on the server. Once the two converge the extra
 * clause is noise and disappears, which is also the check that the load-more
 * control actually reached everything.
 */
export function countSummary({ shown, loaded, total }: CountState): string {
  if (loaded >= total) {
    // The rows in hand can *exceed* the freshest total: work closed while the
    // reader paged, and `nextOffset` deliberately trusts the newer figure. The
    // bare count then has to name the rows rendered above it, because a footer
    // reading "150 issues" under 212 visible rows is the table calling the
    // footer a liar (loom-rz0).
    if (shown >= total) return `${n(shown)} ${shown === 1 ? 'issue' : 'issues'}`
    return `${n(shown)} of ${n(total)} ${total === 1 ? 'issue' : 'issues'}`
  }
  return shown >= loaded
    ? `${n(loaded)} of ${n(total)} loaded`
    : `${n(shown)} of ${n(loaded)} loaded · ${n(total)} match`
}

/**
 * First sighting of each id wins.
 *
 * LIMIT/OFFSET paging has no snapshot: an issue created or closed between two
 * requests shifts the window, and the same row can arrive on both sides of the
 * seam. Two rows with one id would collide as React keys and be counted twice
 * in the footer, so the seam is closed here, once, where every consumer of the
 * accumulated set gets it.
 */
export function dedupeById<T extends { id: string }>(rows: readonly T[]): T[] {
  const seen = new Set<string>()
  const out: T[] = []
  for (const row of rows) {
    if (seen.has(row.id)) continue
    seen.add(row.id)
    out.push(row)
  }
  return out
}
