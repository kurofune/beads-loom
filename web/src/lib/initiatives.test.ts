import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { HealthState, Initiative } from '@/api/types'
import {
  DEFAULT_SORT,
  HEALTH_META,
  loadSort,
  progressOf,
  saveSort,
  sortInitiatives,
} from './initiatives'

it('defines display metadata for every initiative health state', () => {
  const states: HealthState[] = ['on-track', 'at-risk', 'off-track', 'no-target', 'no-scope']

  expect(Object.keys(HEALTH_META).sort()).toEqual([...states].sort())
})

const initiative = (
  id: string,
  values: Partial<Pick<Initiative, 'due' | 'lastActivity' | 'subtree'>> = {},
): Initiative => ({
  id,
  title: `Initiative ${id}`,
  status: 'open',
  priority: 2,
  assignee: '',
  due: null,
  updated: '2026-08-01T00:00:00Z',
  lastActivity: '2026-08-01T00:00:00Z',
  subtree: { total: 4, closed: 1, started: 0, ready: 1, depBlocked: 1, deferScheduled: 0 },
  closeEligible: false,
  needsTarget: false,
  health: { state: 'at-risk', explanation: 'fixture health' },
  noReceipt: { total: 0, noAcceptance: 0, noCloseReason: 0 },
  ...values,
})

beforeEach(() => localStorage.clear())

describe('initiative sorting', () => {
  it('sorts due dates with undated initiatives last in both directions', () => {
    const rows = [
      initiative('none'),
      initiative('later', { due: '2026-09-01T00:00:00Z' }),
      initiative('earlier', { due: '2026-08-15T00:00:00Z' }),
    ]

    expect(sortInitiatives(rows, { key: 'due', dir: 'asc' }).map((row) => row.id)).toEqual([
      'earlier',
      'later',
      'none',
    ])
    expect(sortInitiatives(rows, { key: 'due', dir: 'desc' }).map((row) => row.id)).toEqual([
      'later',
      'earlier',
      'none',
    ])
  })

  it('sorts progress with childless initiatives last in both directions', () => {
    const rows = [
      initiative('none', { subtree: { total: 0, closed: 0, started: 0, ready: 0, depBlocked: 0, deferScheduled: 0 } }),
      initiative('half', { subtree: { total: 4, closed: 2, started: 0, ready: 1, depBlocked: 1, deferScheduled: 0 } }),
      initiative('quarter', { subtree: { total: 4, closed: 1, started: 0, ready: 2, depBlocked: 1, deferScheduled: 0 } }),
    ]

    expect(progressOf(rows[0]!)).toBeNull()
    expect(sortInitiatives(rows, { key: 'progress', dir: 'asc' }).map((row) => row.id)).toEqual([
      'quarter',
      'half',
      'none',
    ])
    expect(sortInitiatives(rows, { key: 'progress', dir: 'desc' }).map((row) => row.id)).toEqual([
      'half',
      'quarter',
      'none',
    ])
  })

  it('sorts activity by the subtree last-activity timestamp', () => {
    const rows = [
      initiative('older', { lastActivity: '2026-08-01T00:00:00Z' }),
      initiative('recent', { lastActivity: '2026-08-03T12:00:00Z' }),
    ]

    expect(sortInitiatives(rows, { key: 'activity', dir: 'desc' }).map((row) => row.id)).toEqual([
      'recent',
      'older',
    ])
  })

  it('falls back to initiative id when sort values tie', () => {
    const rows = [
      initiative('loom-z', { due: '2026-08-20T00:00:00Z' }),
      initiative('loom-a', { due: '2026-08-20T00:00:00Z' }),
    ]

    expect(sortInitiatives(rows, { key: 'due', dir: 'asc' }).map((row) => row.id)).toEqual([
      'loom-a',
      'loom-z',
    ])
  })
})

describe('initiative sort persistence', () => {
  it('uses the default for missing, corrupt, invalid, or throwing storage', () => {
    expect(loadSort('loom')).toEqual(DEFAULT_SORT)

    for (const invalid of [
      'not json',
      '[]',
      '{"key":"due"}',
      '{"key":"unknown","dir":"asc"}',
      '{"key":"due","dir":"sideways"}',
    ]) {
      localStorage.setItem('loom.initiatives.loom', invalid)
      expect(loadSort('loom')).toEqual(DEFAULT_SORT)
    }

    const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('SecurityError')
    })
    expect(loadSort('loom')).toEqual(DEFAULT_SORT)
    spy.mockRestore()
  })

  it('round-trips independently per project and survives quota errors', () => {
    saveSort('loom', { key: 'progress', dir: 'desc' })
    saveSort('djinn', { key: 'activity', dir: 'asc' })

    expect(loadSort('loom')).toEqual({ key: 'progress', dir: 'desc' })
    expect(loadSort('djinn')).toEqual({ key: 'activity', dir: 'asc' })

    const spy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceededError')
    })
    expect(() => saveSort('loom', DEFAULT_SORT)).not.toThrow()
    spy.mockRestore()
  })
})
