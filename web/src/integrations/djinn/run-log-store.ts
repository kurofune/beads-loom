/**
 * Console lines for live runs, held outside React.
 *
 * A busy run emits log lines several times a second. Keeping them in provider
 * state would re-render everything under the provider — the sidebar, the
 * backlog, the board — for output only the log pane is looking at. So the
 * buffer lives here and the pane subscribes to it with `useSyncExternalStore`,
 * which is exactly the case that API exists for.
 *
 * Per-run arrays are replaced only on append, so an untouched run's snapshot
 * keeps its identity and `useSyncExternalStore` does not loop.
 */
export const STREAM_LOG_CAP = 3_000

const EMPTY: readonly string[] = Object.freeze([])

export class RunLogStore {
  #byRun = new Map<string, readonly string[]>()
  #subscribers = new Set<() => void>()

  append(runId: string, lines: readonly string[]): void {
    if (lines.length === 0) return
    const current = this.#byRun.get(runId) ?? EMPTY
    const next = [...current, ...lines]
    this.#byRun.set(runId, next.length > STREAM_LOG_CAP ? next.slice(-STREAM_LOG_CAP) : next)
    this.#emit()
  }

  /** Stable between appends, which is what useSyncExternalStore requires. */
  get(runId: string): readonly string[] {
    return this.#byRun.get(runId) ?? EMPTY
  }

  subscribe = (fn: () => void): (() => void) => {
    this.#subscribers.add(fn)
    return () => {
      this.#subscribers.delete(fn)
    }
  }

  /** Drop a run's buffer — it has ended and the pane has the scrollback. */
  forget(runId: string): void {
    if (this.#byRun.delete(runId)) this.#emit()
  }

  clear(): void {
    this.#byRun.clear()
    this.#emit()
  }

  #emit(): void {
    for (const fn of this.#subscribers) fn()
  }
}

export const runLogs = new RunLogStore()
