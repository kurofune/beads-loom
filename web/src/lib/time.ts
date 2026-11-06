/**
 * Relative time, tuned for a table column ~34px wide.
 *
 * Deliberately not Intl.RelativeTimeFormat: "3 days ago" costs three times the
 * horizontal space of "3d" and a manager scanning a backlog is comparing
 * magnitudes, not reading prose. The absolute timestamp is always one hover
 * away via absoluteTime().
 */
const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR
const WEEK = 7 * DAY
const YEAR = 365 * DAY

export function relativeTime(iso: string | null | undefined, now: number = Date.now()): string {
  if (!iso) return '—'
  const then = Date.parse(iso)
  if (Number.isNaN(then)) return '—'

  const delta = now - then
  const future = delta < 0
  const abs = Math.abs(delta)

  let out: string
  if (abs < MINUTE) out = 'now'
  else if (abs < HOUR) out = `${Math.floor(abs / MINUTE)}m`
  else if (abs < DAY) out = `${Math.floor(abs / HOUR)}h`
  else if (abs < WEEK) out = `${Math.floor(abs / DAY)}d`
  else if (abs < YEAR) out = `${Math.floor(abs / WEEK)}w`
  else out = `${Math.floor(abs / YEAR)}y`

  if (out === 'now') return out
  return future ? `in ${out}` : out
}

/** Full timestamp for tooltips and the history feed. */
export function absoluteTime(iso: string | null | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

/** Date only — for due / defer-until, which have no meaningful time part. */
export function dateOnly(iso: string | null | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleDateString(undefined, { month: 'short', day: '2-digit', year: 'numeric' })
}

/** "4m 02s", "1h 12m" — the shape an ops console wants, not "about an hour". */
export function formatDuration(ms: number): string {
  const seconds = Math.floor(ms / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ${String(seconds % 60).padStart(2, '0')}s`
  const hours = Math.floor(minutes / 60)
  return `${hours}h ${String(minutes % 60).padStart(2, '0')}m`
}
