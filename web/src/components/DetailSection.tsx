import type { ReactNode } from 'react'

/** One datasheet row: legend in the gutter, content in the column. */
export function Section({
  legend,
  children,
  aside,
  tone,
}: {
  legend: string
  children: ReactNode
  aside?: ReactNode
  tone?: 'danger'
}) {
  return (
    <section className="grid grid-cols-[92px_1fr] gap-x-4 border-t border-line px-5 py-3.5">
      <div className="pt-[2px]">
        <p className="legend" style={tone === 'danger' ? { color: 'var(--danger)' } : undefined}>
          {legend}
        </p>
      </div>
      <div className="min-w-0">
        {aside && <div className="mb-2">{aside}</div>}
        {children}
      </div>
    </section>
  )
}
