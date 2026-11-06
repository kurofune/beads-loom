import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { ROLLUP_CEILING_MS, ROLLUP_QUIET_MS, RollupRefetch, initiativesRefetch } from './rollup-refetch'

/**
 * loom-zx1: the board mounts the initiatives rollup, and every write — local or
 * streamed — asked for it again. A burst of N writes cost N runs of an
 * unbounded recursive CTE. These tests are about the *coalescing behaviour*, not
 * about any constant: they drive bursts and count the runs that come out.
 */
describe('RollupRefetch', () => {
  let r: RollupRefetch
  let run: Mock<() => void>

  beforeEach(() => {
    vi.useFakeTimers()
    r = new RollupRefetch()
    run = vi.fn()
  })
  afterEach(() => {
    r.reset()
    vi.useRealTimers()
  })

  it('runs a lone request once the burst goes quiet, not before', () => {
    r.request('loom', { run })
    expect(run).not.toHaveBeenCalled()

    vi.advanceTimersByTime(ROLLUP_QUIET_MS - 1)
    expect(run).not.toHaveBeenCalled()

    vi.advanceTimersByTime(1)
    expect(run).toHaveBeenCalledTimes(1)
  })

  it('collapses a burst of N requests into one run — the regression', () => {
    // Ten writes land in the time it takes an agent to close ten beads.
    for (let i = 0; i < 10; i++) {
      r.request('loom', { run })
      vi.advanceTimersByTime(50)
    }
    expect(run).not.toHaveBeenCalled()

    vi.advanceTimersByTime(ROLLUP_QUIET_MS)
    expect(run).toHaveBeenCalledTimes(1)
  })

  it('keeps each project on its own window', () => {
    const other = vi.fn()
    r.request('loom', { run })
    r.request('djinn', { run: other })
    r.request('loom', { run })

    vi.advanceTimersByTime(ROLLUP_QUIET_MS)
    expect(run).toHaveBeenCalledTimes(1)
    expect(other).toHaveBeenCalledTimes(1)
  })

  it('runs the latest callback, not the one that opened the burst', () => {
    const first = vi.fn()
    const last = vi.fn()
    r.request('loom', { run: first })
    r.request('loom', { run: last })

    vi.advanceTimersByTime(ROLLUP_QUIET_MS)
    expect(first).not.toHaveBeenCalled()
    expect(last).toHaveBeenCalledTimes(1)
  })

  it('starts a fresh window after firing, so the next write still lands', () => {
    r.request('loom', { run })
    vi.advanceTimersByTime(ROLLUP_QUIET_MS)
    expect(run).toHaveBeenCalledTimes(1)
    expect(r.pending).toEqual([])

    r.request('loom', { run })
    vi.advanceTimersByTime(ROLLUP_QUIET_MS)
    expect(run).toHaveBeenCalledTimes(2)
  })

  it('never starves under a write stream that never goes quiet', () => {
    // A running agent writes faster than the quiet window ever closes. Pure
    // debouncing would defer forever and the board's count would freeze.
    const step = Math.floor(ROLLUP_QUIET_MS / 2)
    for (let elapsed = 0; elapsed < ROLLUP_CEILING_MS * 2; elapsed += step) {
      r.request('loom', { run })
      vi.advanceTimersByTime(step)
    }
    expect(run.mock.calls.length).toBeGreaterThanOrEqual(1)
    // …and the ceiling is a ceiling, not a second per-write refetch: two
    // ceilings' worth of a stream is a couple of runs, nowhere near the
    // ~2 * ceiling/step writes that produced it.
    expect(run.mock.calls.length).toBeLessThan((ROLLUP_CEILING_MS * 2) / step / 2)
  })

  it('holds a request no longer than the ceiling', () => {
    const step = 100
    for (let elapsed = 0; elapsed < ROLLUP_CEILING_MS; elapsed += step) {
      r.request('loom', { run })
      vi.advanceTimersByTime(step)
    }
    // The burst opened at t=0; by the ceiling the held request has run.
    vi.advanceTimersByTime(1)
    expect(run).toHaveBeenCalledTimes(1)
  })

  it('reset drops pending work instead of firing it later', () => {
    r.request('loom', { run })
    expect(r.pending).toEqual(['loom'])
    r.reset()
    expect(r.pending).toEqual([])

    vi.advanceTimersByTime(ROLLUP_CEILING_MS * 2)
    expect(run).not.toHaveBeenCalled()
  })

  it('ships one app-wide instance, as the one QueryClient deserves', () => {
    expect(initiativesRefetch).toBeInstanceOf(RollupRefetch)
  })

  /**
   * loom-323 (SCARS: "refetch under the user's hands"). Deferring a refetch
   * onto a timer moves it *out* of the stream's interaction guard, so the
   * window has to carry that guard itself — otherwise this bead's fix would
   * trade a cost regression for the exact scar the guard exists to prevent.
   */
  describe('under the interaction lock', () => {
    it('waits out an open menu instead of refetching under it', () => {
      let locked = true
      r.request('loom', { run, hold: () => locked })

      vi.advanceTimersByTime(ROLLUP_QUIET_MS * 3)
      expect(run).not.toHaveBeenCalled()
      expect(r.pending).toEqual(['loom'])

      locked = false
      vi.advanceTimersByTime(ROLLUP_QUIET_MS)
      expect(run).toHaveBeenCalledTimes(1)
    })

    it('holds past the ceiling too — the lock outranks the ceiling', () => {
      r.request('loom', { run, hold: () => true })
      vi.advanceTimersByTime(ROLLUP_CEILING_MS * 3)
      expect(run).not.toHaveBeenCalled()
    })

    it('still collapses the burst it held', () => {
      let locked = true
      for (let i = 0; i < 6; i++) {
        r.request('loom', { run, hold: () => locked })
        vi.advanceTimersByTime(100)
      }
      locked = false
      vi.advanceTimersByTime(ROLLUP_QUIET_MS)
      expect(run).toHaveBeenCalledTimes(1)
    })
  })
})
