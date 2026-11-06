import { describe, expect, it } from 'vitest'
import type { IssueList } from '@/api/types'
import {
  FIRST_PAGE,
  NEXT_PAGE,
  countSummary,
  dedupeById,
  loadMoreLabel,
  nextOffset,
  nextPageSize,
  pageSizeAt,
  stalled,
} from './paging'

const page = (count: number, total: number, from = 0): IssueList => ({
  issues: Array.from({ length: count }, (_, i) => ({ id: `bead-${from + i}` }) as never),
  total,
})

describe('pageSizeAt', () => {
  it('keeps the first page at the server default so first paint does not regress', () => {
    expect(pageSizeAt(0)).toBe(FIRST_PAGE)
    expect(FIRST_PAGE).toBe(200)
  })

  it('asks for the largest page the server honours verbatim once the reader wants more', () => {
    expect(pageSizeAt(FIRST_PAGE)).toBe(NEXT_PAGE)
    expect(pageSizeAt(FIRST_PAGE + NEXT_PAGE)).toBe(NEXT_PAGE)
    // queries.MaxLimit — anything above it is silently clamped server-side.
    expect(NEXT_PAGE).toBe(1000)
  })
})

describe('nextOffset', () => {
  it('is the rows in hand while the server says more match', () => {
    expect(nextOffset([page(200, 312)])).toBe(200)
    expect(nextOffset([page(200, 6030), page(1000, 6030, 200)])).toBe(1200)
  })

  it('counts rows actually received, not the page size asked for', () => {
    // A short page (the server clamped, or rows closed under us) must not
    // leave a hole in the offset arithmetic.
    expect(nextOffset([page(137, 312)])).toBe(137)
  })

  it('stops once the rows in hand cover the server total', () => {
    expect(nextOffset([page(200, 312), page(112, 312, 200)])).toBeUndefined()
    expect(nextOffset([page(3, 3)])).toBeUndefined()
  })

  it('trusts the freshest total, because the backlog moves while it is read', () => {
    // Page 2 reports 205 where page 1 said 312: work closed between the two
    // requests. The 212 rows in hand already cover the newer figure, so there
    // is nothing left to ask for — against page 1's stale 312 this would keep
    // requesting an offset the server can no longer fill.
    expect(nextOffset([page(200, 312), page(12, 205, 200)])).toBeUndefined()
  })

  it('stops on an empty page rather than asking for the same offset forever', () => {
    expect(nextOffset([page(200, 312), page(0, 312, 200)])).toBeUndefined()
  })

  it('has nothing to ask for before the first page lands', () => {
    expect(nextOffset([])).toBeUndefined()
  })

  /**
   * loom-rz0 corner 1. A LIMIT/OFFSET seam has no snapshot, so page 2 can open
   * on a row page 1 already held. Counting rows *received* makes 312 raw rows
   * cover a total of 312 while the table can only reach 311 of them — paging
   * stops with the footer reading "311 of 312 loaded" and the control that
   * would have fetched the 312th already gone.
   *
   * The offset is therefore the count the footer prints: rows the table can
   * actually reach. Naming the same number in both places is what makes them
   * converge instead of stopping one short.
   */
  it('keeps paging when a seam duplicate leaves a row the table cannot reach', () => {
    // 200 rows, then 112 that re-open on the last of them: 311 reachable.
    const pages = [page(200, 312), page(112, 312, 199)]
    expect(dedupeById(pages.flatMap((p) => p.issues))).toHaveLength(311)
    expect(nextOffset(pages)).toBe(311)
    expect(stalled(pages)).toBe(false)
  })

  it('converges once the row behind the seam duplicate arrives', () => {
    const pages = [page(200, 312), page(112, 312, 199), page(1, 312, 311)]
    expect(dedupeById(pages.flatMap((p) => p.issues))).toHaveLength(312)
    expect(nextOffset(pages)).toBeUndefined()
    expect(stalled(pages)).toBe(false)
    expect(countSummary({ shown: 312, loaded: 312, total: 312 })).toBe('312 issues')
  })

  it('stops rather than re-asking the same offset when a page brings nothing new', () => {
    // Every row of the third page is already in hand: asking again from the
    // same offset would spin forever.
    const pages = [page(200, 312), page(112, 312, 199), page(1, 312, 310)]
    expect(nextOffset(pages)).toBeUndefined()
  })
})

/**
 * Forward paging that gave up while the server still counts rows the table
 * cannot reach. The reader needs a way out of that state (`refetch` re-reads
 * the pages from the start, which is where the seam hid the row), so the view
 * has to be able to tell it apart from "everything is in hand".
 */
describe('stalled', () => {
  it('is false while every row the server counts is in hand', () => {
    expect(stalled([page(200, 312), page(112, 312, 200)])).toBe(false)
  })

  it('is false before the first page lands', () => {
    expect(stalled([])).toBe(false)
  })

  it('is true when a page brought only rows already held and the total is still short', () => {
    expect(stalled([page(200, 312), page(112, 312, 199), page(1, 312, 310)])).toBe(true)
  })

  it('is true when the server stops sending rows it still counts', () => {
    expect(stalled([page(200, 312), page(0, 312, 200)])).toBe(true)
  })
})

describe('loadMoreLabel', () => {
  it('names the exact number of rows the button will pull', () => {
    expect(loadMoreLabel(200, 312)).toBe('Load 112 more')
  })

  it('never promises more than one page', () => {
    expect(loadMoreLabel(200, 6030)).toBe('Load 1,000 more')
  })
})

/**
 * loom-rz0 corner 3. The label was clamped and the tooltip beside it was not,
 * so the same control said "Load 1,000 more" and "the same 5,830 outstanding
 * rows". One number, exported, so a second caller cannot drift from the first.
 */
describe('nextPageSize', () => {
  it('is the rows outstanding while they fit in one page', () => {
    expect(nextPageSize(200, 312)).toBe(112)
  })

  it('never exceeds the largest page the server honours', () => {
    expect(nextPageSize(200, 6030)).toBe(NEXT_PAGE)
  })

  it('is never negative when the backlog shrank below the rows in hand', () => {
    expect(nextPageSize(212, 150)).toBe(0)
  })

  it('is the number the load-more label prints', () => {
    expect(loadMoreLabel(200, 6030)).toBe(`Load ${nextPageSize(200, 6030).toLocaleString('en-US')} more`)
  })
})

describe('countSummary', () => {
  it('reads as a bare count when the table holds the whole result', () => {
    expect(countSummary({ shown: 42, loaded: 42, total: 42 })).toBe('42 issues')
  })

  it('counts one issue as an issue', () => {
    expect(countSummary({ shown: 1, loaded: 1, total: 1 })).toBe('1 issue')
    expect(countSummary({ shown: 0, loaded: 1, total: 1 })).toBe('0 of 1 issue')
  })

  it('names the narrowing when a client filter hides loaded rows', () => {
    expect(countSummary({ shown: 1, loaded: 312, total: 312 })).toBe('1 of 312 issues')
  })

  it('says loaded, not showing, while rows are still unfetched', () => {
    expect(countSummary({ shown: 200, loaded: 200, total: 312 })).toBe('200 of 312 loaded')
  })

  /**
   * The honesty case this bead exists for: a filter over a partial fetch
   * matched 1 of the 200 rows in hand, and 112 rows the filter never saw are
   * still on the server. "1 of 200 issues" is the sentence that hides them.
   */
  it('separates what the filter saw from what the server holds', () => {
    expect(countSummary({ shown: 1, loaded: 200, total: 312 })).toBe(
      '1 of 200 loaded · 312 match',
    )
  })

  /**
   * loom-rz0 corner 2, on the shape `nextOffset`'s "trusts the freshest total"
   * sibling already constructs: 212 rows in hand, freshest total 150, because
   * work closed while the reader paged. The bare-count branch printed the
   * server's 150 under 212 rendered rows — a footer naming fewer rows than the
   * table above it.
   */
  it('never names fewer rows than are rendered above it when the backlog shrinks', () => {
    expect(countSummary({ shown: 212, loaded: 212, total: 150 })).toBe('212 issues')
  })

  it('still names the narrowing when a filter hides rows of a shrunken backlog', () => {
    expect(countSummary({ shown: 12, loaded: 212, total: 150 })).toBe('12 of 150 issues')
  })

  it('groups the thousands so a scale project stays readable', () => {
    expect(countSummary({ shown: 1200, loaded: 1200, total: 6030 })).toBe(
      '1,200 of 6,030 loaded',
    )
  })
})

describe('dedupeById', () => {
  it('keeps offset pagination from showing a row twice when the backlog shifts', () => {
    const rows = [{ id: 'a' }, { id: 'b' }, { id: 'a' }, { id: 'c' }]
    expect(dedupeById(rows).map((r) => r.id)).toEqual(['a', 'b', 'c'])
  })

  it('keeps the first sighting, so a page-1 row does not jump to the end', () => {
    const rows = [
      { id: 'a', page: 1 },
      { id: 'a', page: 2 },
    ]
    expect(dedupeById(rows)[0]?.page).toBe(1)
  })
})
