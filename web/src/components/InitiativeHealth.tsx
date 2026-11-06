import type { InitiativeHealth as Health } from '@/api/types'
import { HEALTH_META } from '@/lib/initiatives'

export function InitiativeHealth({ health, onClick }: { health: Health; onClick?: () => void }) {
  const meta = HEALTH_META[health.state] ?? { ...HEALTH_META['no-target'], label: health.state }
  const muted = meta.tone === 'muted'
  const className = muted
    ? 'cursor-pointer text-[11px] text-faint hover:text-muted'
    : 'chip cursor-pointer'
  const common = {
    className,
    title: health.explanation,
    'aria-label': `${meta.label}: ${health.explanation}`,
    style: { color: meta.color },
    ...(!muted ? { 'data-tone': meta.tone } : {}),
  }

  // The initiatives row wraps the chip in its own onClick. Without stopping the
  // bubble one click runs both handlers, pushing the same history entry twice —
  // the detail opens, but the first Back press only eats the duplicate.
  return onClick ? (
    <button type="button" {...common} onClick={(event) => { event.stopPropagation(); onClick() }}>{meta.label}</button>
  ) : (
    <span {...common}>{meta.label}</span>
  )
}
