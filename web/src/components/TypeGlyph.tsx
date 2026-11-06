import type { IssueType } from '@/api/types'

/**
 * Issue-type marks.
 *
 * Hand-drawn on a 12×12 grid rather than pulled from an icon set: at 12px an
 * icon library's 24px artwork turns to mush, and bd's type vocabulary (spike,
 * decision, milestone) has no good off-the-shelf equivalent anyway. Each mark
 * is a distinct silhouette so the column is scannable without colour, and each
 * type also owns a hue (`--type-*`, see typeColor) used everywhere a type shows.
 */
const PATHS: Record<string, { d: string; fill?: boolean }> = {
  // rounded square — the default unit of work
  task: { d: 'M3 3h6a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1z' },
  // target — something to hunt down
  bug: { d: 'M6 1.8a4.2 4.2 0 1 0 0 8.4 4.2 4.2 0 0 0 0-8.4zM6 4.6a1.4 1.4 0 1 1 0 2.8 1.4 1.4 0 0 1 0-2.8z' },
  // diamond outline — new surface area
  feature: { d: 'M6 1.4 10.6 6 6 10.6 1.4 6z' },
  // stack of rules — maintenance
  chore: { d: 'M2 3.4h8M2 6h8M2 8.6h5' },
  // nested squares — a container of work
  epic: { d: 'M1.6 1.6h8.8v8.8H1.6zM4.2 4.2h3.6v3.6H4.2z' },
  // flowchart diamond, hollow centre — a fork in the road
  decision: { d: 'M6 1.2 10.8 6 6 10.8 1.2 6zM6 4.2 3.8 6 6 7.8 8.2 6z' },
  // upward wedge — a timeboxed probe
  spike: { d: 'M6 1.5 11 10.5H1z' },
  // bookmark — a narrative
  story: { d: 'M3 1.8h6v8.4L6 7.9l-3 2.3z' },
  // flag — a fixed point in time
  milestone: { d: 'M3 1.4v9.2M3 2.2h6.4L7.8 4.7l1.6 2.5H3z' },
}

const FILLED = new Set(['spike', 'milestone'])

/** The type's hue; a project-custom type falls back to faint ink. */
export function typeColor(type: IssueType): string {
  return `var(--type-${type}, var(--text-faint))`
}

export function TypeGlyph({ type, className }: { type: IssueType; className?: string }) {
  const mark = PATHS[type]
  return (
    <svg
      viewBox="0 0 12 12"
      width="12"
      height="12"
      aria-hidden="true"
      className={className}
      style={{ flex: 'none', color: typeColor(type) }}
    >
      <title>{type}</title>
      {mark ? (
        <path
          d={mark.d}
          fill={FILLED.has(type) ? 'currentColor' : 'none'}
          stroke="currentColor"
          strokeWidth="1.1"
          strokeLinejoin="round"
          strokeLinecap="round"
          fillRule="evenodd"
        />
      ) : (
        // Unknown / project-custom bd type — a neutral placeholder, never a guess.
        <circle cx="6" cy="6" r="2.2" fill="none" stroke="currentColor" strokeWidth="1.1" />
      )}
    </svg>
  )
}
