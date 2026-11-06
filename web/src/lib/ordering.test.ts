import { describe, expect, it } from 'vitest'
import type { Issue, IssueStatus, Priority } from '@/api/types'
import { byCloseTimeDescending, byWorkOrder, readinessTier, sortFor } from './ordering'

let seq = 0
const mk = (over: Partial<Issue> = {}): Issue =>
  ({
    id: `loom-${(seq++).toString(36)}`,
    title: 'title',
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
    notReady: null,
    missingAcceptance: false,
    missingCloseReason: false,
    pinned: false,
    depCounts: { blockedBy: 0, blocks: 0 },
    parent: null,
    childStats: null,
    ...over,
  }) as Issue

const order = (issues: Issue[]) => [...issues].sort(byWorkOrder).map((i) => i.id)

describe('readinessTier', () => {
  it('puts in-progress and hooked work in tier 0, whatever the ready flag says', () => {
    expect(readinessTier(mk({ status: 'in_progress', ready: false, notReady: 'deps' }))).toBe(0)
    expect(readinessTier(mk({ status: 'hooked' }))).toBe(0)
  })

  it('puts ready work in tier 1', () => {
    expect(readinessTier(mk({ status: 'open', ready: true }))).toBe(1)
  })

  it('puts dependency-blocked work in tier 2, including the legacy reading with no reason sent', () => {
    expect(readinessTier(mk({ status: 'blocked' }))).toBe(2)
    expect(readinessTier(mk({ status: 'open', ready: false, notReady: 'deps' }))).toBe(2)
    expect(readinessTier(mk({ status: 'open', ready: false, notReady: undefined }))).toBe(2)
    expect(readinessTier(mk({ status: 'open', ready: false, notReady: null }))).toBe(2)
  })

  it('puts deferred work in tier 3, by status or by a future defer date', () => {
    expect(readinessTier(mk({ status: 'deferred' }))).toBe(3)
    expect(readinessTier(mk({ status: 'open', ready: false, notReady: 'deferred' }))).toBe(3)
  })

  it('puts closed, pinned and project-custom statuses in tier 4, before any readiness reading', () => {
    // Terminal statuses come back from the server with ready=false and no
    // reason, which is exactly the legacy "blocked" reading — so they must be
    // caught before it, or every closed row would sort as blocked work.
    expect(readinessTier(mk({ status: 'closed', ready: false, notReady: undefined }))).toBe(4)
    expect(readinessTier(mk({ status: 'pinned', ready: true }))).toBe(4)
    expect(readinessTier(mk({ status: 'awaiting_review', ready: false, notReady: 'deferred' }))).toBe(3)
    expect(readinessTier(mk({ status: 'awaiting_review', ready: true }))).toBe(1)
  })

  it('reads only status, ready and notReady — dependency counts and dates do not move a tier', () => {
    const base = { status: 'open' as IssueStatus, ready: true }
    expect(readinessTier(mk({ ...base, depCounts: { blockedBy: 5, blocks: 0 } }))).toBe(1)
    expect(readinessTier(mk({ ...base, deferUntil: '2099-01-01T00:00:00Z' }))).toBe(1)
  })
})

describe('byWorkOrder', () => {
  it('leads on priority, even over a better readiness tier', () => {
    const p1Deferred = mk({ id: 'z-p1', priority: 1, status: 'deferred' })
    const p2Working = mk({ id: 'a-p2', priority: 2, status: 'in_progress' })
    expect(order([p2Working, p1Deferred])).toEqual(['z-p1', 'a-p2'])
  })

  it('within a priority, orders in progress → ready → blocked → deferred → other', () => {
    // Ids run against the expected order so an id-only comparator cannot pass.
    const issues = [
      mk({ id: 'a-closed', status: 'closed' }),
      mk({ id: 'b-deferred', status: 'deferred' }),
      mk({ id: 'c-blocked', ready: false, notReady: 'deps' }),
      mk({ id: 'd-ready', ready: true }),
      mk({ id: 'e-working', status: 'in_progress' }),
    ]
    expect(order(issues)).toEqual(['e-working', 'd-ready', 'c-blocked', 'b-deferred', 'a-closed'])
  })

  it('puts a ready bead above a blocked one touched more recently', () => {
    const blockedFresh = mk({ id: 'a-blocked', notReady: 'deps', updated: '2026-09-01T00:00:00Z' })
    const readyStale = mk({ id: 'b-ready', ready: true, updated: '2026-01-01T00:00:00Z' })
    expect(order([blockedFresh, readyStale])).toEqual(['b-ready', 'a-blocked'])
  })

  it('among ready work, the bead that unblocks more sorts first', () => {
    const unblocksNone = mk({ id: 'a-none', ready: true, depCounts: { blockedBy: 0, blocks: 0 } })
    const unblocksThree = mk({ id: 'b-three', ready: true, depCounts: { blockedBy: 0, blocks: 3 } })
    expect(order([unblocksNone, unblocksThree])).toEqual(['b-three', 'a-none'])
  })

  it('among in-progress work, the bead that unblocks more sorts first', () => {
    const a = mk({ id: 'a-none', status: 'in_progress', depCounts: { blockedBy: 0, blocks: 0 } })
    const b = mk({ id: 'b-two', status: 'in_progress', depCounts: { blockedBy: 0, blocks: 2 } })
    expect(order([a, b])).toEqual(['b-two', 'a-none'])
  })

  it('among blocked work, the bead with fewest open blockers sorts first', () => {
    const four = mk({ id: 'a-four', notReady: 'deps', depCounts: { blockedBy: 4, blocks: 9 } })
    const one = mk({ id: 'b-one', notReady: 'deps', depCounts: { blockedBy: 1, blocks: 0 } })
    expect(order([four, one])).toEqual(['b-one', 'a-four'])
  })

  it('ignores dependency counts in the deferred and other tiers', () => {
    const a = mk({ id: 'a', status: 'deferred', depCounts: { blockedBy: 9, blocks: 0 } })
    const b = mk({ id: 'b', status: 'deferred', depCounts: { blockedBy: 0, blocks: 9 } })
    expect(order([b, a])).toEqual(['a', 'b'])
  })

  it('then puts the oldest created first', () => {
    const newer = mk({ id: 'a-newer', ready: true, created: '2026-08-10T00:00:00Z' })
    const older = mk({ id: 'b-older', ready: true, created: '2026-08-01T00:00:00Z' })
    expect(order([newer, older])).toEqual(['b-older', 'a-newer'])
  })

  it('ignores updated entirely: issues differing only in updated sort by id', () => {
    const b = mk({ id: 'b', ready: true, updated: '2026-09-01T00:00:00Z' })
    const a = mk({ id: 'a', ready: true, updated: '2026-01-01T00:00:00Z' })
    expect(order([b, a])).toEqual(['a', 'b'])
  })
})

describe('sortFor', () => {
  it('returns byWorkOrder for every live status and byCloseTimeDescending for closed', () => {
    for (const s of ['open', 'in_progress', 'blocked', 'deferred', 'pinned', 'hooked', 'custom']) {
      expect(sortFor(s)).toBe(byWorkOrder)
    }
    expect(sortFor('closed')).toBe(byCloseTimeDescending)
  })
})
