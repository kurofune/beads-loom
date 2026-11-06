import type { Run, RunBead, RunState, TimelineEntry, Verdict } from '@/api/types'
import { formatDuration } from '@/lib/time'

export { formatDuration }

/**
 * Run-view logic, kept pure.
 *
 * The dashboard is the one screen where a wrong number costs real money, so the
 * cost arithmetic, the log buffer's bounds and the formula ordering all live
 * here where they can be asserted rather than eyeballed.
 */

/* --- cost guardrails ------------------------------------------------------ */

export type GuardLevel = 'none' | 'ok' | 'warn' | 'danger'

export interface CostGuard {
  spent: number
  cap: number | null
  /** 0–1, clamped. Meaningless without a cap; check `level` first. */
  fraction: number
  level: GuardLevel
  /** What the bar should say. */
  label: string
}

export const WARN_AT = 0.7
export const DANGER_AT = 0.9

/**
 * Cost against the session cap.
 *
 * `cap` is a *nullable* number on purpose: the real `.djinn/config.json` in
 * ~/Projects/djinn carries neither guardrail key, so "no cap configured" is the
 * common case and must not render as a full bar or a division by zero. With no
 * cap we still show the spend — it is the number that matters — but no bar and
 * no threshold colour, because there is nothing to be a fraction *of*.
 */
export function costGuard(spent: number, cap: number | null | undefined): CostGuard {
  const safeSpent = Number.isFinite(spent) ? Math.max(0, spent) : 0
  if (cap === null || cap === undefined || !Number.isFinite(cap) || cap <= 0) {
    return {
      spent: safeSpent,
      cap: null,
      fraction: 0,
      level: 'none',
      label: `${formatUsd(safeSpent)} spent · no cap configured`,
    }
  }

  const fraction = Math.min(safeSpent / cap, 1)
  const raw = safeSpent / cap
  const level: GuardLevel = raw >= DANGER_AT ? 'danger' : raw >= WARN_AT ? 'warn' : 'ok'
  return {
    spent: safeSpent,
    cap,
    fraction,
    level,
    label: `${formatUsd(safeSpent)} of ${formatUsd(cap)} · ${Math.round(raw * 100)}%`,
  }
}

/** Costs are small and precision matters to the operator: cents, always. */
export function formatUsd(value: number): string {
  if (!Number.isFinite(value)) return '$0.00'
  return `$${value.toFixed(2)}`
}

/* --- log buffer ----------------------------------------------------------- */

/**
 * The live log pane's backing store.
 *
 * Capped, because a long run emits tens of thousands of lines and an unbounded
 * array in a React state atom will eventually stall the tab. Dropping from the
 * head is safe here: the pane is a *tail*, and the scrollback endpoint serves
 * anything older.
 */
export const LOG_CAP = 2_000

export interface LogBuffer {
  lines: string[]
  /** Lines dropped off the front, so the UI can say so rather than lie. */
  dropped: number
}

export const emptyLog: LogBuffer = { lines: [], dropped: 0 }

export function appendLog(buffer: LogBuffer, incoming: readonly string[]): LogBuffer {
  if (incoming.length === 0) return buffer
  const lines = [...buffer.lines, ...incoming]
  if (lines.length <= LOG_CAP) return { lines, dropped: buffer.dropped }
  const overflow = lines.length - LOG_CAP
  return { lines: lines.slice(overflow), dropped: buffer.dropped + overflow }
}

/** Replace the buffer with scrollback loaded from the offset endpoint. */
export function loadLog(text: string): LogBuffer {
  const lines = text.split('\n')
  // A trailing newline yields one empty element; it is not a line.
  if (lines.at(-1) === '') lines.pop()
  if (lines.length <= LOG_CAP) return { lines, dropped: 0 }
  return { lines: lines.slice(-LOG_CAP), dropped: lines.length - LOG_CAP }
}

/**
 * Autoscroll state.
 *
 * A tail that yanks you back to the bottom while you are reading is worse than
 * one that does not follow at all, so scrolling up pauses it and only returning
 * to the bottom (or pressing resume) re-arms it.
 */
export const AT_BOTTOM_SLOP = 24

export function isAtBottom(el: {
  scrollTop: number
  scrollHeight: number
  clientHeight: number
}): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight <= AT_BOTTOM_SLOP
}

/* --- the djinn formula ---------------------------------------------------- */

/**
 * Formula order — a mirror of `formulaOrder` in `internal/runs/statedir.go`.
 *
 * The backend ranks *loop* directory names (`review-impl-loop`) and exposes the
 * step as the name with `-loop` stripped (`statedir.go:109`), which is what
 * arrives on the wire, so this list is the same sequence in step form:
 *
 *     freshness 10 · ac-quality 20 · impl 30 · review-impl 40
 *     review-tests 50 · review-light 60 · verify 70 · commit-push 80
 *
 * Both sides need it: the backend to sort a timeline, the frontend to lay out
 * chips for steps a bead has not reached yet. Two sorted orders that can drift
 * is a real hazard, and the fix that produced this list is exactly that drift —
 * `freshness` runs *first* and was sorting *last* because it was missing here.
 */
export const FORMULA_STEPS = [
  'freshness',
  'ac-quality',
  'impl',
  'review-impl',
  'review-tests',
  'review-light',
  'verify',
  'commit-push',
] as const

export type FormulaStep = (typeof FORMULA_STEPS)[number]

/**
 * Steps that are drawn only when a bead actually ran them.
 *
 * The rest of the formula is drawn even when unreached, because a greyed chip
 * says "it never got there" where a missing one would say "this step does not
 * exist". These two are the other way round: they are *conditional* on the
 * formula variant, so a permanent grey chip would assert a step that was never
 * going to run.
 *
 * Measured against the 28 beads with state directories in `~/Projects/djinn`:
 * `freshness` is a gate ahead of the chain, present on 14 of them and absent on
 * the other 14; `review-light` appears once, and on that bead it stands *in
 * place of* review-impl + review-tests rather than beside them.
 */
export const CONDITIONAL_STEPS: ReadonlySet<string> = new Set(['freshness', 'review-light'])

/** Human labels; the directory names are terse and repetitive. */
export const STEP_LABEL: Record<string, string> = {
  freshness: 'Freshness',
  'ac-quality': 'AC quality',
  impl: 'Implement',
  'review-impl': 'Review impl',
  'review-tests': 'Review tests',
  'review-light': 'Review light',
  verify: 'Verify',
  'commit-push': 'Commit & push',
}

export const stepLabel = (step: string): string => STEP_LABEL[step] ?? step

/**
 * Rank a step by formula position. Currently has no callers — `stepProgress`
 * gets its order by walking `FORMULA_STEPS` directly — but it is the frontend's
 * counterpart to `loopRank` in `statedir.go` and is pinned by a test.
 */
export function stepOrder(step: string): number {
  const i = (FORMULA_STEPS as readonly string[]).indexOf(step)
  // An unrecognised step sorts last rather than first: djinn may add steps, and
  // burying a known one under an unknown would be worse than the reverse.
  return i < 0 ? FORMULA_STEPS.length : i
}

export interface StepProgress {
  step: string
  label: string
  reached: boolean
  /** Highest iteration seen for this step. */
  iter: number
  entries: TimelineEntry[]
}

/**
 * Collapse a bead's timeline into one chip per formula step.
 *
 * Steps that were never reached still appear, greyed — the shape of the formula
 * is the point, and a missing chip reads as "this step does not exist" rather
 * than "this bead never got there".
 */
export function stepProgress(timeline: readonly TimelineEntry[]): StepProgress[] {
  const byStep = new Map<string, TimelineEntry[]>()
  for (const entry of timeline) {
    const list = byStep.get(entry.step) ?? []
    list.push(entry)
    byStep.set(entry.step, list)
  }

  // A conditional step earns its chip by having run; everything else in the
  // formula is drawn either way. Both keep their formula position — a
  // freshness gate that did run belongs at the front, not appended.
  const known = FORMULA_STEPS.filter(
    (step) => !CONDITIONAL_STEPS.has(step) || byStep.has(step),
  ).map((step) => step as string)
  const extra = [...byStep.keys()].filter((s) => !known.includes(s)).sort()

  return [...known, ...extra].map((step) => {
    const entries = (byStep.get(step) ?? []).sort((a, b) => a.iter - b.iter)
    return {
      step,
      label: stepLabel(step),
      reached: entries.length > 0,
      iter: entries.reduce((max, e) => Math.max(max, e.iter), 0),
      entries,
    }
  })
}

/* --- run presentation ----------------------------------------------------- */

export const RUN_STATE_LABEL: Record<RunState, string> = {
  running: 'Running',
  stopping: 'Stopping',
  stopped: 'Stopped',
  finished: 'Finished',
  failed: 'Failed',
}

/** Which status token a run state borrows, so runs read like the rest of Loom. */
export const RUN_STATE_TOKEN: Record<RunState, string> = {
  running: 'in_progress',
  stopping: 'pinned',
  stopped: 'deferred',
  finished: 'closed',
  failed: 'blocked',
}

export const isLive = (state: RunState): boolean => state === 'running' || state === 'stopping'

export const VERDICT_TONE: Record<string, 'ok' | 'danger' | 'warn' | 'neutral'> = {
  COMPLETE: 'ok',
  FAILED: 'danger',
  CRASHED: 'danger',
}

export const verdictTone = (verdict: Verdict | undefined): 'ok' | 'danger' | 'warn' | 'neutral' =>
  verdict ? (VERDICT_TONE[verdict] ?? 'neutral') : 'neutral'

export interface RunTotals {
  total: number
  done: number
  failed: number
  active: number
  retried: number
}

export function runTotals(beads: readonly RunBead[]): RunTotals {
  let done = 0
  let failed = 0
  let active = 0
  let retried = 0
  for (const bead of beads) {
    if (bead.exitedAt === null) active += 1
    else if (bead.verdict === 'COMPLETE') done += 1
    else failed += 1
    if (bead.retries > 0) retried += 1
  }
  return { total: beads.length, done, failed, active, retried }
}

/** Elapsed wall clock, live for a running run and fixed once it ends. */
export function runDuration(run: Run, now: number = Date.now()): string {
  const start = Date.parse(run.startedAt)
  if (Number.isNaN(start)) return '—'
  const end = run.endedAt ? Date.parse(run.endedAt) : now
  return formatDuration(Math.max(0, end - start))
}
