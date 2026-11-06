import { describe, expect, it } from 'vitest'
import { absoluteTime, dateOnly, relativeTime } from './time'

const NOW = Date.parse('2026-08-02T12:00:00Z')
const minus = (ms: number) => new Date(NOW - ms).toISOString()

const SECOND = 1000
const MINUTE = 60 * SECOND
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

describe('relativeTime', () => {
  it('collapses to the largest whole unit', () => {
    expect(relativeTime(minus(30 * SECOND), NOW)).toBe('now')
    expect(relativeTime(minus(5 * MINUTE), NOW)).toBe('5m')
    expect(relativeTime(minus(3 * HOUR), NOW)).toBe('3h')
    expect(relativeTime(minus(2 * DAY), NOW)).toBe('2d')
    expect(relativeTime(minus(3 * 7 * DAY), NOW)).toBe('3w')
    expect(relativeTime(minus(400 * DAY), NOW)).toBe('1y')
  })

  it('floors rather than rounds, so nothing ever reads older than it is', () => {
    expect(relativeTime(minus(119 * MINUTE), NOW)).toBe('1h')
    expect(relativeTime(minus(47 * HOUR), NOW)).toBe('1d')
  })

  it('marks the future — due dates and defer-until land here', () => {
    expect(relativeTime(new Date(NOW + 3 * DAY).toISOString(), NOW)).toBe('in 3d')
    expect(relativeTime(new Date(NOW + 30 * SECOND).toISOString(), NOW)).toBe('now')
  })

  it('renders an em dash for null, undefined and garbage', () => {
    expect(relativeTime(null, NOW)).toBe('—')
    expect(relativeTime(undefined, NOW)).toBe('—')
    expect(relativeTime('not a date', NOW)).toBe('—')
  })
})

describe('absoluteTime / dateOnly', () => {
  it('degrade to an em dash rather than "Invalid Date"', () => {
    expect(absoluteTime(null)).toBe('—')
    expect(absoluteTime('nope')).toBe('—')
    expect(dateOnly(null)).toBe('—')
    expect(dateOnly('nope')).toBe('—')
  })

  it('renders a real timestamp when given one', () => {
    expect(absoluteTime('2026-08-02T12:00:00Z')).not.toBe('—')
    expect(dateOnly('2026-08-02T12:00:00Z')).toMatch(/2026/)
  })
})
