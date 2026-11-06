import { useEffect, useRef, type ReactNode } from 'react'

/**
 * Modal shell. Deliberately small: a scrim, an escape hatch, focus containment,
 * and a consistent header/footer rhythm. Everything else is the caller's.
 */
export function Dialog({
  title,
  subtitle,
  onClose,
  children,
  footer,
  width = 460,
}: {
  title: string
  subtitle?: ReactNode
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
  width?: number
}) {
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    // Focus the first real control, not the dialog box — a reason field the
    // user has to click into is a reason field that gets left blank.
    const first = ref.current?.querySelector<HTMLElement>(
      'input, textarea, select, button:not([data-secondary])',
    )
    ;(first ?? ref.current)?.focus()

    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
        onClose()
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [onClose])

  return (
    <div
      className="scrim-in fixed inset-0 flex items-start justify-center overflow-y-auto p-8"
      style={{ background: 'var(--scrim)', zIndex: 'var(--z-modal)' }}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
      role="presentation"
    >
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
        className="panel-in my-auto w-full overflow-hidden rounded-lg bg-overlay outline-none"
        style={{ maxWidth: width, boxShadow: 'var(--shadow-overlay)' }}
      >
        <header className="border-b border-line px-5 py-3">
          <h2 className="text-[13px] font-semibold text-ink">{title}</h2>
          {subtitle && <p className="mt-0.5 text-[11.5px] text-faint">{subtitle}</p>}
        </header>

        <div className="px-5 py-4">{children}</div>

        {footer && (
          <footer className="flex items-center justify-end gap-2 border-t border-line px-5 py-3">
            {footer}
          </footer>
        )}
      </div>
    </div>
  )
}

/** Field wrapper: legend in the gutter style used by the detail sheet. */
export function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: ReactNode
  children: ReactNode
}) {
  return (
    <label className="block">
      <span className="legend mb-1.5 block">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-[11px] leading-relaxed text-faint">{hint}</span>}
    </label>
  )
}
