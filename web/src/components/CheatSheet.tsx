import { useEffect, useRef } from 'react'
import type { Project } from '@/api/types'
import { useIntegrations } from '@/integrations'
import { SHORTCUTS, SURFACE_LABEL, type Shortcut, type Surface } from '@/keyboard/registry'

/**
 * The `?` sheet.
 *
 * Rendered straight from the shortcut registry, so it lists exactly what is
 * bound — never an aspirational key map. The "also by" column is the receipt
 * for the rule that nothing in Loom is keyboard-only.
 *
 * Grouped **by surface** rather than by category, because the question someone
 * actually has is "what works *here*". A binding live everywhere appears once
 * under Everywhere; one that only works on the board appears under Board.
 *
 * The current project's mounted integrations add their shortcuts and their
 * views' sections; an integration the project lacks adds no row.
 */
const SURFACE_ORDER: Surface[] = ['*', 'backlog', 'board', 'graph', 'now', 'detail']

interface SheetSection {
  id: string
  label: string
  rows: Shortcut[]
}

function sections(extra: Shortcut[], views: { id: string; surface?: { label: string; shortcuts: string[] } }[]): SheetSection[] {
  const all = [...SHORTCUTS, ...extra]
  const core = SURFACE_ORDER.map((surface) => ({
    id: surface,
    label: SURFACE_LABEL[surface],
    rows: all.filter((s) =>
      surface === '*' ? !s.surfaces || s.surfaces.length === 0 : s.surfaces?.includes(surface),
    ),
  }))
  const integration = views.flatMap((v) =>
    v.surface
      ? [{ id: v.id, label: v.surface.label, rows: all.filter((s) => v.surface?.shortcuts.includes(s.id)) }]
      : [],
  )
  // Integration views sit before Now, where Runs always sat.
  const at = core.findIndex((c) => c.id === 'now')
  return [...core.slice(0, at), ...integration, ...core.slice(at)].filter((c) => c.rows.length > 0)
}

export function CheatSheet({ project, onClose }: { project: Project | undefined; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null)
  const mounted = useIntegrations(project)
  const list = sections(
    mounted.shortcuts.map((s) => s.shortcut),
    mounted.views,
  )

  useEffect(() => {
    ref.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' || e.key === '?') {
        e.preventDefault()
        onClose()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div
      className="scrim-in fixed inset-0 flex items-center justify-center p-6"
      style={{ background: 'var(--scrim)', zIndex: 'var(--z-modal)' }}
      onClick={onClose}
      role="presentation"
    >
      <div
        ref={ref}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label="Keyboard shortcuts"
        className="w-full max-w-[560px] overflow-hidden rounded-lg bg-overlay outline-none"
        style={{ boxShadow: 'var(--shadow-overlay)' }}
        onClick={(e) => e.stopPropagation()}
      >
        <header className="flex items-center justify-between border-b border-line px-5 py-3">
          <h2 className="text-[13px] font-semibold text-ink">Keyboard</h2>
          <button type="button" className="icon-btn" onClick={onClose} aria-label="Close">
            <CloseGlyph />
          </button>
        </header>

        <div className="max-h-[70vh] space-y-5 overflow-y-auto px-5 py-4">
          {list.map((section) => {
            return (
              <section key={section.id}>
                <p className="legend mb-2">{section.label}</p>
                <ul className="space-y-1">
                  {section.rows.map((s) => (
                    <li key={s.id} className="flex items-baseline gap-3 py-0.5">
                      <span className="flex shrink-0 items-center gap-1" style={{ width: 72 }}>
                        {(s.display ?? s.keys).map((k, i) => (
                          <kbd key={i} className="kbd">
                            {k === 'Escape' ? 'esc' : k === 'Enter' ? '↵' : k}
                          </kbd>
                        ))}
                      </span>
                      <span className="flex-1 text-[12px] text-muted">{s.label}</span>
                      {s.alsoBy && (
                        <span className="shrink-0 text-[11px] text-faint">{s.alsoBy}</span>
                      )}
                    </li>
                  ))}
                </ul>
              </section>
            )
          })}
        </div>

        <footer className="border-t border-line px-5 py-2.5">
          <p className="text-[11px] text-faint">
            Chords are typed in sequence — <kbd className="kbd">g</kbd>{' '}
            <kbd className="kbd">l</kbd>, not held together. More shortcuts land with the
            board and command palette.
          </p>
        </footer>
      </div>
    </div>
  )
}

export function CloseGlyph() {
  return (
    <svg viewBox="0 0 12 12" width="12" height="12" aria-hidden="true">
      <path
        d="M3 3l6 6M9 3l-6 6"
        stroke="currentColor"
        strokeWidth="1.3"
        strokeLinecap="round"
      />
    </svg>
  )
}
