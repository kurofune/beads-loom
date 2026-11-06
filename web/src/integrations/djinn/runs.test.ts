import { describe, expect, it } from 'vitest'
import type { RunBead, TimelineEntry } from '@/api/types'
import {
  appendLog,
  costGuard,
  DANGER_AT,
  emptyLog,
  formatDuration,
  formatUsd,
  isAtBottom,
  loadLog,
  LOG_CAP,
  runTotals,
  stepProgress,
  stepOrder,
  FORMULA_STEPS,
  STEP_LABEL,
  verdictTone,
  WARN_AT,
} from './runs'

describe('costGuard', () => {
  it('reports no cap rather than a full bar when none is configured', () => {
    // The real .djinn/config.json carries neither guardrail key, so this is the
    // COMMON case. Drawing a bar would invent a denominator that does not exist.
    const guard = costGuard(5.74, null)
    expect(guard.level).toBe('none')
    expect(guard.cap).toBeNull()
    expect(guard.fraction).toBe(0)
    expect(guard.label).toContain('no cap configured')
  })

  it('treats undefined the same as null', () => {
    expect(costGuard(1, undefined).level).toBe('none')
  })

  it('escalates at 70% and 90%', () => {
    expect(costGuard(6.9, 10).level).toBe('ok')
    expect(costGuard(10 * WARN_AT, 10).level).toBe('warn')
    expect(costGuard(8.9, 10).level).toBe('warn')
    expect(costGuard(10 * DANGER_AT, 10).level).toBe('danger')
    expect(costGuard(12, 10).level).toBe('danger')
  })

  it('clamps the bar at 100% while still reporting the real overspend', () => {
    const guard = costGuard(25, 10)
    expect(guard.fraction).toBe(1)
    expect(guard.label).toContain('$25.00')
    expect(guard.label).toContain('250%')
  })

  it('never divides by zero or renders a negative spend', () => {
    expect(costGuard(5, 0).level).toBe('none')
    expect(costGuard(-3, 10).spent).toBe(0)
    expect(costGuard(Number.NaN, 10).spent).toBe(0)
  })

  it('formats money to the cent, because the operator is paying it', () => {
    expect(formatUsd(0)).toBe('$0.00')
    expect(formatUsd(5.7449)).toBe('$5.74')
    expect(formatUsd(Number.NaN)).toBe('$0.00')
  })
})

describe('log buffer', () => {
  const lines = (n: number, from = 0) => Array.from({ length: n }, (_, i) => `line ${from + i}`)

  it('appends in order', () => {
    const b = appendLog(appendLog(emptyLog, ['a']), ['b', 'c'])
    expect(b.lines).toEqual(['a', 'b', 'c'])
    expect(b.dropped).toBe(0)
  })

  it('is a no-op for an empty batch, preserving identity', () => {
    const b = appendLog(emptyLog, ['a'])
    expect(appendLog(b, [])).toBe(b)
  })

  it('caps and drops from the head, counting what it dropped', () => {
    // A long run emits tens of thousands of lines; an unbounded array stalls
    // the tab. Dropping the head is safe because this is a *tail*.
    const b = appendLog(emptyLog, lines(LOG_CAP + 250))
    expect(b.lines).toHaveLength(LOG_CAP)
    expect(b.dropped).toBe(250)
    expect(b.lines[0]).toBe('line 250')
    expect(b.lines.at(-1)).toBe(`line ${LOG_CAP + 249}`)
  })

  it('accumulates the dropped count across appends', () => {
    let b = appendLog(emptyLog, lines(LOG_CAP))
    b = appendLog(b, lines(10, LOG_CAP))
    b = appendLog(b, lines(10, LOG_CAP + 10))
    expect(b.dropped).toBe(20)
    expect(b.lines).toHaveLength(LOG_CAP)
  })

  it('loads scrollback and drops a trailing empty line', () => {
    expect(loadLog('a\nb\nc\n').lines).toEqual(['a', 'b', 'c'])
    expect(loadLog('a\nb\nc').lines).toEqual(['a', 'b', 'c'])
    expect(loadLog('').lines).toEqual([])
  })

  it('keeps the newest lines when scrollback exceeds the cap', () => {
    const loaded = loadLog(lines(LOG_CAP + 5).join('\n'))
    expect(loaded.lines).toHaveLength(LOG_CAP)
    expect(loaded.lines.at(-1)).toBe(`line ${LOG_CAP + 4}`)
    expect(loaded.dropped).toBe(5)
  })
})

describe('autoscroll', () => {
  it('is armed at the bottom and within the slop', () => {
    expect(isAtBottom({ scrollTop: 900, scrollHeight: 1000, clientHeight: 100 })).toBe(true)
    expect(isAtBottom({ scrollTop: 880, scrollHeight: 1000, clientHeight: 100 })).toBe(true)
  })

  it('disarms once the user has scrolled up to read', () => {
    // A tail that yanks you back mid-sentence is worse than one that does not
    // follow at all.
    expect(isAtBottom({ scrollTop: 400, scrollHeight: 1000, clientHeight: 100 })).toBe(false)
  })

  it('is armed when there is nothing to scroll', () => {
    expect(isAtBottom({ scrollTop: 0, scrollHeight: 100, clientHeight: 100 })).toBe(true)
  })
})

describe('runTotals', () => {
  const bead = (over: Partial<RunBead>): RunBead =>
    ({ id: 'x', spawnedAt: '', exitedAt: '2026-08-01T00:00:00Z', retries: 0, ...over }) as RunBead

  it('counts done, failed, in-flight and retried', () => {
    const totals = runTotals([
      bead({ verdict: 'COMPLETE' }),
      bead({ verdict: 'FAILED' }),
      bead({ verdict: 'CRASHED', retries: 2 }),
      bead({ exitedAt: null }),
    ])
    expect(totals).toEqual({ total: 4, done: 1, failed: 2, active: 1, retried: 1 })
  })

  it('treats an unknown verdict as failed rather than silently done', () => {
    // Real logs contain CRASHED as well as COMPLETE/FAILED; anything that is
    // not an explicit COMPLETE did not succeed.
    expect(runTotals([bead({ verdict: 'WEIRD' })]).failed).toBe(1)
  })

  it('handles an empty run', () => {
    expect(runTotals([])).toEqual({ total: 0, done: 0, failed: 0, active: 0, retried: 0 })
  })
})

describe('verdictTone', () => {
  it('maps the observed verdicts, and stays neutral for the rest', () => {
    expect(verdictTone('COMPLETE')).toBe('ok')
    expect(verdictTone('FAILED')).toBe('danger')
    expect(verdictTone('CRASHED')).toBe('danger')
    expect(verdictTone('SOMETHING_NEW')).toBe('neutral')
    expect(verdictTone(undefined)).toBe('neutral')
  })
})

describe('stepProgress', () => {
  const entry = (step: string, iter: number): TimelineEntry => ({
    loop: `${step}-loop`,
    step,
    iter,
    procedure: step,
    mtime: '2026-08-01T00:00:00Z',
  })

  it('lays out the whole formula, including steps never reached', () => {
    // A missing chip would read as "this step does not exist"; a greyed one
    // reads as "it never got there", which is the fact the manager wants.
    const steps = stepProgress([entry('ac-quality', 1), entry('impl', 1)])
    expect(steps.map((s) => s.step)).toEqual([
      'ac-quality',
      'impl',
      'review-impl',
      'review-tests',
      'verify',
      'commit-push',
    ])
    expect(steps.filter((s) => s.reached).map((s) => s.step)).toEqual(['ac-quality', 'impl'])
  })

  it('puts freshness first, where it ran, instead of after commit-push', () => {
    // Round-2 N5. `freshness` is rank 10 in statedir.go's formulaOrder — it
    // runs *before* ac-quality — but was absent from FORMULA_STEPS, so it fell
    // into the unknown-steps bucket and rendered last: a gate that decides
    // whether the rest happens, drawn after the final step.
    const steps = stepProgress([entry('freshness', 1), entry('ac-quality', 1)])
    expect(steps.map((s) => s.step)).toEqual([
      'freshness',
      'ac-quality',
      'impl',
      'review-impl',
      'review-tests',
      'verify',
      'commit-push',
    ])
  })

  it('slots review-light between review-tests and verify', () => {
    const steps = stepProgress([entry('review-light', 1)])
    expect(steps.map((s) => s.step)).toEqual([
      'ac-quality',
      'impl',
      'review-impl',
      'review-tests',
      'review-light',
      'verify',
      'commit-push',
    ])
  })

  it('draws the conditional steps only when a bead actually ran them', () => {
    // Unlike the core chain, freshness and review-light depend on the formula
    // variant. A permanent grey chip would assert a step that was never going
    // to run — the opposite of the reason unreached core steps are drawn.
    const plain = stepProgress([entry('impl', 1)]).map((s) => s.step)
    expect(plain).not.toContain('freshness')
    expect(plain).not.toContain('review-light')
    expect(plain).toContain('commit-push')
  })

  it('mirrors the backend formulaOrder exactly', () => {
    // internal/runs/statedir.go ranks loop names 10..80; the wire carries the
    // name with `-loop` stripped. Two independently maintained sort orders is
    // how N5 happened, so this pins them to each other.
    const backend = [
      'freshness',
      'ac-quality',
      'impl',
      'review-impl',
      'review-tests',
      'review-light',
      'verify',
      'commit-push',
    ]
    expect([...FORMULA_STEPS]).toEqual(backend)
    expect(backend.map(stepOrder)).toEqual([0, 1, 2, 3, 4, 5, 6, 7])
    expect(stepOrder('brand-new-step')).toBe(FORMULA_STEPS.length)
    expect(backend.every((s) => s in STEP_LABEL)).toBe(true)
  })

  it('reports the highest iteration per step', () => {
    const steps = stepProgress([entry('impl', 1), entry('impl', 3), entry('impl', 2)])
    expect(steps.find((s) => s.step === 'impl')).toMatchObject({ reached: true, iter: 3 })
  })

  it('appends unknown steps after the formula instead of dropping them', () => {
    // djinn may add steps; burying a known one under an unknown would be worse
    // than the reverse, but silently dropping it would be worst of all.
    const steps = stepProgress([entry('brand-new-step', 1)])
    expect(steps.at(-1)?.step).toBe('brand-new-step')
    expect(steps.at(-1)?.reached).toBe(true)
  })

  it('returns the full formula for an empty timeline', () => {
    expect(stepProgress([]).every((s) => !s.reached)).toBe(true)
    expect(stepProgress([])).toHaveLength(6)
  })
})

describe('formatDuration', () => {
  it('reads like an ops console, not like prose', () => {
    expect(formatDuration(9_000)).toBe('9s')
    expect(formatDuration(62_000)).toBe('1m 02s')
    expect(formatDuration(3_600_000)).toBe('1h 00m')
    expect(formatDuration(4_530_000)).toBe('1h 15m')
    expect(formatDuration(0)).toBe('0s')
  })
})
