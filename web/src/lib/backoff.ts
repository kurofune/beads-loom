/**
 * Reconnect backoff for the SSE stream: 1s → 30s, doubling, with jitter.
 *
 * Pure and clock-free so the schedule can be asserted rather than waited out.
 * Jitter is supplied by the caller (default `Math.random`) for the same reason.
 *
 * Why jitter: every open Loom tab reconnects at the same instant when the
 * backend restarts. Without it they retry in lockstep and hammer the server at
 * exactly the moments it is least able to cope.
 */
export const BACKOFF_MIN_MS = 1_000
export const BACKOFF_MAX_MS = 30_000

/** Delay before attempt N (0-based: attempt 0 is the first retry). */
export function backoffDelay(
  attempt: number,
  random: () => number = Math.random,
): number {
  const exponent = Math.max(0, Math.floor(attempt))
  // 1s, 2s, 4s, 8s, 16s, 30s, 30s…
  const base = Math.min(BACKOFF_MIN_MS * 2 ** exponent, BACKOFF_MAX_MS)
  // Full jitter over the bottom 25%: enough to break lockstep without making
  // the first retry feel sluggish.
  const jitter = base * 0.25 * random()
  return Math.round(base - jitter)
}

/** The whole schedule, for tests and for documenting the shape in one place. */
export function backoffSchedule(attempts: number, random: () => number = () => 0): number[] {
  return Array.from({ length: attempts }, (_, i) => backoffDelay(i, random))
}
