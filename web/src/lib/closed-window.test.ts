import { describe, expect, it } from 'vitest'
import type { Issue, IssueStatus, Priority } from '@/api/types'
import {
  CLOSED_WINDOWS,
  DEFAULT_CLOSED_WINDOW,
  closedWindowStart,
  filterByCloseWindow,
  inClosedWindow,
  type ClosedWindow,
} from './closed-window'

let n = 0
const mk = (over: Partial<Issue> = {}): Issue =>
  ({
    id: `i${n++}`,
    title: 't',
    type: 'task',
    status: 'open' as IssueStatus,
    priority: 2 as Priority,
    assignee: '',
    labels: [],
    estimate: 0,
    created: '2026-08-01T00:00:00Z',
    updated: '2026-08-01T00:00:00Z',
    closed: null,
    due: null,
    deferUntil: null,
    ready: false,
    pinned: false,
    depCounts: { blockedBy: 0, blocks: 0 },
    parent: null,
    childStats: null,
    ...over,
  }) as Issue

/**
 * Every boundary in this file is a LOCAL calendar boundary, so the fixtures are
 * built with the local-time Date constructor rather than an ISO string with a
 * zone. A `Z` literal would make each assertion pass or fail depending on the
 * machine's zone, which is exactly the off-by-a-day the criterion asks us to
 * catch.
 */
const local = (y: number, m: number, d: number, h = 0, min = 0, s = 0, ms = 0) =>
  new Date(y, m - 1, d, h, min, s, ms)

/** 2026-08-16 is a Sunday; the fixture clock sits mid-afternoon. */
const NOW = local(2026, 8, 16, 14, 30)

const closedAt = (d: Date, over: Partial<Issue> = {}) =>
  mk({ status: 'closed', closed: d.toISOString(), ...over })

describe('CLOSED_WINDOWS', () => {
  it('offers exactly three windows, widest last', () => {
    expect(CLOSED_WINDOWS.map((w) => w.id)).toEqual(['today', '7d', 'all'])
    expect(CLOSED_WINDOWS.map((w) => w.label)).toEqual(['Today', '7d', 'All'])
  })

  it('defaults to a window the operator opted into, not unbounded history', () => {
    expect(DEFAULT_CLOSED_WINDOW).toBe('7d')
    expect(CLOSED_WINDOWS.some((w) => w.id === DEFAULT_CLOSED_WINDOW)).toBe(true)
  })
})

describe('closedWindowStart', () => {
  it('starts Today at the viewer local midnight of the current day', () => {
    expect(closedWindowStart('today', NOW)).toBe(local(2026, 8, 16).getTime())
  })

  it('starts 7d at the local midnight six days back, so the window spans seven calendar days', () => {
    expect(closedWindowStart('7d', NOW)).toBe(local(2026, 8, 10).getTime())
  })

  it('gives All no start at all', () => {
    expect(closedWindowStart('all', NOW)).toBeNull()
  })

  it('anchors on the calendar day, not on a rolling 24 hours', () => {
    // One minute after midnight, Today must still mean the whole of today —
    // a `now - 24h` implementation would put the start in yesterday.
    expect(closedWindowStart('today', local(2026, 8, 16, 0, 1))).toBe(local(2026, 8, 16).getTime())
    expect(closedWindowStart('today', local(2026, 8, 16, 23, 59))).toBe(local(2026, 8, 16).getTime())
  })
})

describe('inClosedWindow — day boundaries', () => {
  it('includes the first instant of today in Today', () => {
    expect(inClosedWindow(local(2026, 8, 16).toISOString(), 'today', NOW)).toBe(true)
  })

  it('excludes the last instant of yesterday from Today', () => {
    expect(inClosedWindow(local(2026, 8, 15, 23, 59, 59, 999).toISOString(), 'today', NOW)).toBe(
      false,
    )
  })

  it('includes the last instant of today in Today', () => {
    expect(inClosedWindow(local(2026, 8, 16, 23, 59, 59, 999).toISOString(), 'today', NOW)).toBe(
      true,
    )
  })

  it('includes the first instant of the seventh day back in 7d', () => {
    expect(inClosedWindow(local(2026, 8, 10).toISOString(), '7d', NOW)).toBe(true)
  })

  it('excludes the last instant before the seventh day back from 7d', () => {
    expect(inClosedWindow(local(2026, 8, 9, 23, 59, 59, 999).toISOString(), '7d', NOW)).toBe(false)
  })

  it('keeps Today a strict subset of 7d', () => {
    const t = local(2026, 8, 16, 9).toISOString()
    expect(inClosedWindow(t, 'today', NOW)).toBe(true)
    expect(inClosedWindow(t, '7d', NOW)).toBe(true)
  })

  it('lets All through regardless of age', () => {
    expect(inClosedWindow(local(2019, 1, 1).toISOString(), 'all', NOW)).toBe(true)
    expect(inClosedWindow(null, 'all', NOW)).toBe(true)
  })

  it('keeps a future close time in every window rather than hiding it everywhere', () => {
    // Clock skew between the Dolt server and the viewer must not make a card
    // vanish from Today, 7d AND All at once.
    const skewed = local(2026, 8, 17, 3).toISOString()
    expect(inClosedWindow(skewed, 'today', NOW)).toBe(true)
    expect(inClosedWindow(skewed, '7d', NOW)).toBe(true)
  })

  it('drops a close time it cannot place in time from the bounded windows', () => {
    expect(inClosedWindow(null, 'today', NOW)).toBe(false)
    expect(inClosedWindow('not a date', '7d', NOW)).toBe(false)
  })
})

describe('filterByCloseWindow', () => {
  const today = closedAt(local(2026, 8, 16, 9), { id: 'today' })
  const threeDays = closedAt(local(2026, 8, 13, 9), { id: 'three' })
  const sixDays = closedAt(local(2026, 8, 10, 0), { id: 'six' })
  const eightDays = closedAt(local(2026, 8, 8, 9), { id: 'eight' })
  const nullClosed = mk({ id: 'null-closed', status: 'closed', closed: null })
  const open = mk({ id: 'open-one', status: 'open' })
  const inProgress = mk({ id: 'wip', status: 'in_progress' })
  const all = [today, threeDays, sixDays, eightDays, nullClosed, open, inProgress]

  const ids = (window: ClosedWindow) => filterByCloseWindow(all, window, NOW).map((i) => i.id)

  it('narrows only the closed cards, never the live columns', () => {
    expect(ids('today')).toEqual(['today', 'open-one', 'wip'])
  })

  it('spans seven calendar days on 7d', () => {
    expect(ids('7d')).toEqual(['today', 'three', 'six', 'open-one', 'wip'])
  })

  it('returns everything, including an unplaceable close time, on All', () => {
    expect(ids('all')).toEqual(all.map((i) => i.id))
  })

  it('preserves input order, leaving the column comparator to do the ordering', () => {
    const shuffled = [eightDays, today, threeDays]
    expect(filterByCloseWindow(shuffled, '7d', NOW).map((i) => i.id)).toEqual(['today', 'three'])
  })

  it('returns the same array identity on All so a memo does not churn', () => {
    expect(filterByCloseWindow(all, 'all', NOW)).toBe(all)
  })
})
