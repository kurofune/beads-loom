import { describe, expect, it } from 'vitest'
import { backoffDelay, backoffSchedule, BACKOFF_MAX_MS, BACKOFF_MIN_MS } from './backoff'

// A random() of 0 means "no jitter", which makes the schedule assertable.
const noJitter = () => 0

describe('backoffDelay', () => {
  it('doubles from 1s and caps at 30s', () => {
    expect(backoffSchedule(8, noJitter)).toEqual([
      1_000, 2_000, 4_000, 8_000, 16_000, 30_000, 30_000, 30_000,
    ])
  })

  it('starts at the floor and never exceeds the ceiling', () => {
    expect(backoffDelay(0, noJitter)).toBe(BACKOFF_MIN_MS)
    expect(backoffDelay(99, noJitter)).toBe(BACKOFF_MAX_MS)
  })

  it('subtracts jitter, never adds it — the cap is a real cap', () => {
    for (let attempt = 0; attempt < 10; attempt++) {
      expect(backoffDelay(attempt, () => 1)).toBeLessThanOrEqual(backoffDelay(attempt, noJitter))
      expect(backoffDelay(attempt, () => 1)).toBeLessThanOrEqual(BACKOFF_MAX_MS)
    }
  })

  it('keeps jitter within 25%, so retries stay predictable', () => {
    const base = backoffDelay(3, noJitter)
    expect(backoffDelay(3, () => 1)).toBeGreaterThanOrEqual(base * 0.75)
  })

  it('spreads attempts across a range rather than firing in lockstep', () => {
    // Two tabs reconnecting after a backend restart must not retry together.
    const values = new Set([0.1, 0.4, 0.9].map((r) => backoffDelay(4, () => r)))
    expect(values.size).toBe(3)
  })

  it('treats a negative or fractional attempt as the first retry', () => {
    expect(backoffDelay(-3, noJitter)).toBe(BACKOFF_MIN_MS)
    expect(backoffDelay(0.7, noJitter)).toBe(BACKOFF_MIN_MS)
  })

  it('always returns a whole number of milliseconds for setTimeout', () => {
    for (let i = 0; i < 8; i++) expect(Number.isInteger(backoffDelay(i, () => 0.37))).toBe(true)
  })
})
