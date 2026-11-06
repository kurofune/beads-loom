/**
 * Serialises post-write invalidation across overlapping mutations.
 *
 * The problem (review finding N8): mutation A's `onSettled` invalidates the
 * lists while mutation B is still in flight. The refetch it triggers can return
 * *pre-B* data and land on top of B's optimistic patch, reverting B's change on
 * screen until B settles and corrects it. It converges, so it shows up as a
 * flicker under rapid successive edits — but it is the same class of bug as
 * `loom-323`, and it deserves the same treatment.
 *
 * The SSE guard already parks *stream*-driven invalidations while writes are in
 * flight. This is its mutation-driven twin: an invalidation is held until the
 * **last** in-flight write settles, then the accumulated projects are
 * invalidated once.
 *
 * Deliberately not React state. There is one QueryClient for the app and the
 * mutation callbacks are not components, so a plain object with an explicit
 * lifecycle is both simpler and easier to test than a hook.
 */
export class WriteCoordinator {
  #inflight = 0
  #pending = new Set<string>()

  /** Call when a mutation starts (onMutate). */
  begin(): void {
    this.#inflight += 1
  }

  /**
   * Call when a mutation finishes (onSettled), naming the project it touched.
   *
   * Returns the projects to invalidate now — empty while other writes are still
   * running, and the full accumulated set when this was the last one.
   */
  end(project: string): string[] {
    this.#pending.add(project)
    this.#inflight = Math.max(0, this.#inflight - 1)
    if (this.#inflight > 0) return []
    const due = [...this.#pending]
    this.#pending.clear()
    return due
  }

  /** In-flight writes. Exposed for assertions, not for control flow. */
  get inflight(): number {
    return this.#inflight
  }

  /** Projects whose invalidation is currently held back. */
  get deferred(): string[] {
    return [...this.#pending]
  }

  /** Drop everything — used when a test or a project switch resets the world. */
  reset(): void {
    this.#inflight = 0
    this.#pending.clear()
  }
}

/** The app-wide instance. One QueryClient, one coordinator. */
export const writes = new WriteCoordinator()
