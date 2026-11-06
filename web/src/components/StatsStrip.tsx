import { useMemo } from 'react'
import type { ApiError } from '@/api/client'
import type { Stats, TrendPoint } from '@/api/types'
import { useIntegrations } from '@/integrations'
import { InlineError, InlineLoading } from './states'

/**
 * The backlog's stats strip.
 *
 * One row, four facts, no chrome. The trend is a sparkline rather than a chart
 * because the question it answers is "are we closing faster than we're opening"
 * — a shape, not a set of values — and a real chart with axes would cost four
 * times the vertical space to say the same thing less quickly.
 *
 * Every number is a *link into an answer*: ready frontier is what can start,
 * dep-blocked is what is stuck behind something, and the two are deliberately
 * adjacent because their ratio is the health of the backlog. Mounted
 * integrations add their own tiles at the end (djinn: spend).
 *
 * Until the counts arrive the strip holds its row with a loading bar; a failed
 * count is said inline with a Retry, beside the last good counts when there
 * are some. The project is up when this renders, so a slow count (504
 * query_timeout) is never the unreachable-server panel.
 */
export function StatsStrip({
  project,
  stats,
  error,
  onRetry,
  onFilterReady,
  onFilterStale,
  staleCount,
}: {
  project: string
  stats: Stats | undefined
  error?: ApiError | null
  onRetry?: () => void
  onFilterReady?: () => void
  onFilterStale?: () => void
  staleCount?: number
}) {
  if (!stats) {
    return (
      <div className="flex shrink-0 items-center gap-4 border-b border-line px-4 py-1.5" data-testid="stats-strip">
        {error ? (
          <InlineError error={error} what="stats" onRetry={onRetry} />
        ) : (
          <InlineLoading label="Loading stats" />
        )}
      </div>
    )
  }
  return (
    <StatsRow
      project={project}
      stats={stats}
      error={error}
      onRetry={onRetry}
      onFilterReady={onFilterReady}
      onFilterStale={onFilterStale}
      staleCount={staleCount}
    />
  )
}

function StatsRow({
  project,
  stats,
  error,
  onRetry,
  onFilterReady,
  onFilterStale,
  staleCount,
}: {
  project: string
  stats: Stats
  error?: ApiError | null
  onRetry?: () => void
  onFilterReady?: () => void
  onFilterStale?: () => void
  staleCount?: number
}) {
  const ready = stats.readyFrontier ?? stats.ready
  const trend = stats.trend ?? []
  const { statTiles } = useIntegrations(project)

  return (
    <div
      className="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-1 border-b border-line px-4 py-1.5"
      data-testid="stats-strip"
    >
      <Sparkline points={trend} />

      <button
        type="button"
        onClick={onFilterReady}
        className="mono flex items-baseline gap-1 text-[10.5px] hover:underline"
        style={{ color: 'var(--accent)' }}
        title="The ready frontier — issues with no unmet dependencies, straight from the ready_issues view"
      >
        <span className="text-[12px]">{ready}</span> ready
      </button>

      <span
        className="mono flex items-baseline gap-1 text-[10.5px] text-faint"
        title="Open, but waiting on an unmet dependency. Defer-scheduled work is excluded."
      >
        <span className="text-[12px] text-muted">{stats.depBlocked}</span> blocked
      </span>

      {stats.deferScheduled !== undefined && stats.deferScheduled > 0 && (
        <span
          className="mono flex items-baseline gap-1 text-[10.5px] text-faint"
          title="Parked on purpose with a future defer date — not blocked by anything"
        >
          <span className="text-[12px] text-muted">{stats.deferScheduled}</span> deferred
        </span>
      )}

      {staleCount !== undefined && staleCount > 0 && (
        <button
          type="button"
          onClick={onFilterStale}
          className="mono flex items-baseline gap-1 text-[10.5px] hover:underline"
          style={{ color: 'var(--warn)' }}
          title="Untouched for longer than the staleness window"
        >
          <span className="text-[12px]">{staleCount}</span> stale
        </button>
      )}

      <span className="mono text-[10.5px] text-faint" title="Closed in the last 7 days">
        <span className="text-[12px] text-muted">{stats.closedLast7d}</span> closed 7d
      </span>

      {statTiles.map((tile) => (
        <tile.component key={tile.id} project={project} />
      ))}

      {error && (
        <span className="shrink-0 whitespace-nowrap">
          <InlineError error={error} what="stats" onRetry={onRetry} />
        </span>
      )}
    </div>
  )
}

const SPARK_W = 108
const SPARK_H = 18

/**
 * Opened vs closed over 30 days.
 *
 * The backend zero-fills, so there are no gaps to patch and the x-axis is
 * simply the index. Two overlaid areas: opened above the line, closed below —
 * so "closing more than opening" reads as the closed band dominating, without
 * needing a legend or a second glance.
 */
function Sparkline({ points }: { points: TrendPoint[] }) {
  const path = useMemo(() => {
    if (points.length < 2) return null
    const max = Math.max(1, ...points.map((p) => Math.max(p.opened, p.closed)))
    const x = (i: number) => (i / (points.length - 1)) * SPARK_W
    const mid = SPARK_H / 2
    const up = (v: number) => mid - (v / max) * (mid - 1)
    const down = (v: number) => mid + (v / max) * (mid - 1)

    const line = (get: (p: TrendPoint) => number, y: (v: number) => number) =>
      points.map((p, i) => `${i === 0 ? 'M' : 'L'} ${x(i).toFixed(1)} ${y(get(p)).toFixed(1)}`).join(' ')

    return {
      opened: `${line((p) => p.opened, up)} L ${SPARK_W} ${mid} L 0 ${mid} Z`,
      closed: `${line((p) => p.closed, down)} L ${SPARK_W} ${mid} L 0 ${mid} Z`,
      totalOpened: points.reduce((n, p) => n + p.opened, 0),
      totalClosed: points.reduce((n, p) => n + p.closed, 0),
    }
  }, [points])

  if (!path) return null

  return (
    <span
      className="flex items-center gap-2"
      title={`Last 30 days — ${path.totalOpened} opened, ${path.totalClosed} closed`}
    >
      <svg width={SPARK_W} height={SPARK_H} aria-hidden="true" className="shrink-0">
        <line
          x1="0"
          y1={SPARK_H / 2}
          x2={SPARK_W}
          y2={SPARK_H / 2}
          stroke="var(--border)"
          strokeWidth="1"
        />
        <path d={path.opened} fill="var(--status-open)" opacity="0.45" />
        <path d={path.closed} fill="var(--accent)" opacity="0.5" />
      </svg>
      <span className="mono text-[10px] text-faint">
        <span style={{ color: 'var(--status-open)' }}>+{path.totalOpened}</span>
        {' / '}
        <span style={{ color: 'var(--accent)' }}>−{path.totalClosed}</span>
        <span className="ml-1">30d</span>
      </span>
    </span>
  )
}
