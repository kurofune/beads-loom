import { useQueryClient } from '@tanstack/react-query'
import { qk } from '@/api/hooks'

/**
 * A project whose dolt server has gone away, but whose data we still hold.
 *
 * Phase 1 replaced the whole view with an outage screen. That is right when
 * there is nothing to show, and wrong the moment there is: throwing away a
 * backlog the manager was mid-way through reading, because a background server
 * died, punishes them for the server's problem.
 *
 * So the cached rows stay exactly where they were, clearly marked as a
 * photograph rather than a live feed, and every write is disabled — Loom cannot
 * run `bd` against a database it cannot read back. Recovery is automatic: the
 * backend's poller publishes `project-health` when the server returns, which
 * patches the cache and makes this banner disappear on its own.
 */
export function StaleBanner({ project, reason }: { project: string; reason: string }) {
  const qc = useQueryClient()

  return (
    <div
      className="flex shrink-0 items-center gap-2.5 border-b border-line px-4 py-1.5"
      data-tone="warn"
      style={{ background: 'color-mix(in oklab, var(--warn) 12%, transparent)' }}
      role="status"
    >
      <span className="dot" data-status="blocked" data-filled="true" aria-hidden="true" />

      <span className="text-[11.5px] font-medium text-ink">Stale — server unreachable</span>

      <span className="mono min-w-0 flex-1 truncate text-[10.5px] text-muted" title={reason}>
        {reason || `${project}'s dolt server is not answering`}
      </span>

      <span className="shrink-0 text-[10.5px] text-faint">
        showing the last data we saw · writes disabled
      </span>

      <button
        type="button"
        className="btn btn-quiet h-5 shrink-0 px-2 text-[10.5px]"
        onClick={() => void qc.invalidateQueries({ queryKey: qk.projects })}
        title="Re-check now. The backend also retries on its own every 15s."
      >
        retry
      </button>
    </div>
  )
}
