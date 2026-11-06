/**
 * Refetch policy for the initiatives rollup.
 *
 * The rollup (`internal/queries/initiatives.go`) is the most expensive read
 * Loom issues: a depth-50 recursive subtree CTE joined against the transitive
 * blocked CTE (`internal/queries/readiness.go`), with no LIMIT, over the whole
 * dependencies table. Until loom-g1v.5 only an open Initiatives tab paid for it.
 * That bead mounted `useInitiatives` in BoardView to drive the close-eligible
 * nudge, and both invalidation paths — the SSE stream's `invalidate-project`
 * and `invalidateAfterWrite`'s post-write sweep — asked for it by name. So the
 * app's primary view started re-running the whole rollup on *every* write, N
 * times for a burst of N, multiplied by the number of open board tabs.
 *
 * The fix is refetch policy, not a smaller payload. loom-g1v.5's review filed an
 * explicit counter-finding against a board-only endpoint: the board and the
 * Initiatives view must agree on what "ready to close" means, and they only do
 * so because one server-side computation decides it. So the payload is
 * untouched and the *asking* is rationed instead.
 *
 * ## The window
 *
 * Trailing edge, per project. A request opens (or extends) a quiet window;
 * when the window closes, one refetch runs. A burst of N writes therefore costs
 * one rollup, and it costs it *after* the burst — not once at the front and
 * again at the back, which a leading-edge throttle would.
 *
 * The ceiling is what keeps trailing-edge from becoming never. A djinn run
 * writes continuously for minutes; a pure debounce would extend the window on
 * every event and the board's eligible count would sit frozen for the whole
 * run, which is the AC7 no-manual-refresh failure loom-g1v.5 fixed. So a held
 * request runs no later than `ceilingMs` after the burst that opened it, and
 * the stream restarts the window from there.
 *
 * Deliberately not React state, and deliberately not a hook — same reasoning as
 * `WriteCoordinator`: there is one QueryClient per tab, the callers are
 * mutation callbacks and stream handlers rather than components, and a plain
 * object with an explicit lifecycle is easier to test than either.
 */

/** How long the burst must be quiet before the rollup is asked for again. */
export const ROLLUP_QUIET_MS = 1_200

/** The longest a held request may wait, however long the burst runs. */
export const ROLLUP_CEILING_MS = 8_000

export interface RollupRequest {
  /** Ask for the rollup. Runs once per coalesced burst. */
  run: () => void
  /**
   * Answer true to keep waiting. This is what carries loom-323's interaction
   * guard across the window: a refetch deferred onto a timer would otherwise
   * fire *outside* the stream's busy check and land under an open menu — the
   * "refetch under the user's hands" scar. A held request is not dropped; it is
   * asked again a window later, and the lock outranks the ceiling.
   */
  hold?: () => boolean
}

interface Held extends RollupRequest {
  /** When the burst this entry is coalescing opened. */
  since: number
  timer: ReturnType<typeof setTimeout>
}

export interface RollupRefetchOptions {
  quietMs?: number
  ceilingMs?: number
}

export class RollupRefetch {
  readonly quietMs: number
  readonly ceilingMs: number
  #held = new Map<string, Held>()

  constructor(options: RollupRefetchOptions = {}) {
    this.quietMs = options.quietMs ?? ROLLUP_QUIET_MS
    this.ceilingMs = options.ceilingMs ?? ROLLUP_CEILING_MS
  }

  /**
   * Ask for `project`'s rollup to be refetched. Coalesced: the request runs
   * once the burst goes quiet, or at the ceiling if it never does — and not at
   * all while `hold` says the user is mid-interaction.
   */
  request(project: string, request: RollupRequest): void {
    const now = Date.now()
    const held = this.#held.get(project)
    const since = held?.since ?? now
    if (held) clearTimeout(held.timer)

    const untilCeiling = since + this.ceilingMs - now
    const delay = Math.max(0, Math.min(this.quietMs, untilCeiling))
    const timer = setTimeout(() => this.#fire(project), delay)
    this.#held.set(project, { ...request, since, timer })
  }

  #fire(project: string): void {
    const held = this.#held.get(project)
    if (!held) return
    if (held.hold?.()) {
      // Mid-interaction. Ask again a window later rather than refetching under
      // the user's hands, and keep the coalesced burst held meanwhile.
      const timer = setTimeout(() => this.#fire(project), this.quietMs)
      this.#held.set(project, { ...held, timer })
      return
    }
    this.#held.delete(project)
    held.run()
  }

  /** Projects whose rollup refetch is currently held. For assertions, not
   *  for control flow. */
  get pending(): string[] {
    return [...this.#held.keys()]
  }

  /** Drop everything — used when a test resets the world. */
  reset(): void {
    for (const held of this.#held.values()) clearTimeout(held.timer)
    this.#held.clear()
  }
}

/** The app-wide instance. One QueryClient, one rollup budget. */
export const initiativesRefetch = new RollupRefetch()
