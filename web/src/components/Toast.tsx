import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from 'react'
import { ApiError } from '@/api/client'
import { CloseGlyph } from './CheatSheet'

/**
 * Toasts.
 *
 * The only thing on screen allowed to appear unbidden, so the bar for showing
 * one is high: a write failed, or a write succeeded in a way the user cannot
 * see from where they are standing. Failures carry bd's stderr verbatim in
 * mono — that sentence is usually the entire explanation, and paraphrasing it
 * would throw away the only actionable detail.
 */
export type ToastTone = 'error' | 'ok' | 'info'

export interface Toast {
  id: number
  tone: ToastTone
  title: string
  /** Verbatim machine output — bd's stderr. Rendered mono, never reworded. */
  detail?: string
  /** Undo and friends. Failures stay until dismissed. */
  action?: { label: string; run: () => void }
}

interface ToastApi {
  push: (toast: Omit<Toast, 'id'>) => void
  /** Shorthand for the overwhelmingly common case. */
  error: (title: string, err: unknown) => void
  dismiss: (id: number) => void
}

const ToastContext = createContext<ToastApi | null>(null)

const AUTO_DISMISS_MS = 4200

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const nextId = useRef(1)

  const dismiss = useCallback((id: number) => {
    setToasts((list) => list.filter((t) => t.id !== id))
  }, [])

  const push = useCallback(
    (toast: Omit<Toast, 'id'>) => {
      const id = nextId.current++
      setToasts((list) => [...list.slice(-3), { ...toast, id }])
      // Errors persist: a failed write the user did not notice is worse than
      // a toast that outstays its welcome.
      if (toast.tone !== 'error') {
        window.setTimeout(() => dismiss(id), AUTO_DISMISS_MS)
      }
    },
    [dismiss],
  )

  const error = useCallback(
    (title: string, err: unknown) => {
      const api = err instanceof ApiError ? err : null
      push({
        tone: 'error',
        title,
        detail: api ? (api.stderr ?? api.message) : err instanceof Error ? err.message : String(err),
      })
    },
    [push],
  )

  const value = useMemo<ToastApi>(() => ({ push, error, dismiss }), [push, error, dismiss])

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div
        className="pointer-events-none fixed right-4 bottom-4 flex w-[380px] flex-col gap-2"
        style={{ zIndex: 'var(--z-modal)' }}
        role="status"
        aria-live="polite"
      >
        {toasts.map((toast) => (
          <ToastCard key={toast.id} toast={toast} onDismiss={() => dismiss(toast.id)} />
        ))}
      </div>
    </ToastContext.Provider>
  )
}

function ToastCard({ toast, onDismiss }: { toast: Toast; onDismiss: () => void }) {
  const tone = toast.tone === 'error' ? 'danger' : toast.tone === 'ok' ? 'accent' : 'neutral'
  return (
    <div
      data-tone={tone}
      className="panel-in pointer-events-auto overflow-hidden rounded-md bg-overlay"
      style={{ boxShadow: 'var(--shadow-overlay)' }}
    >
      <div className="flex items-start gap-2.5 px-3 py-2.5">
        <span
          className="mt-[5px] h-1.5 w-1.5 shrink-0 rounded-full"
          style={{ background: 'var(--c)' }}
          aria-hidden="true"
        />
        <div className="min-w-0 flex-1">
          <p className="text-[12.5px] font-medium text-ink">{toast.title}</p>
          {toast.detail && (
            <pre className="mono mt-1 max-h-24 overflow-auto text-[11px] leading-[1.45] whitespace-pre-wrap text-muted">
              {toast.detail}
            </pre>
          )}
          {toast.action && (
            <button
              type="button"
              className="btn btn-quiet mt-2 h-6 px-2 text-[11.5px]"
              onClick={() => {
                toast.action?.run()
                onDismiss()
              }}
            >
              {toast.action.label}
            </button>
          )}
        </div>
        <button type="button" className="icon-btn shrink-0" onClick={onDismiss} aria-label="Dismiss">
          <CloseGlyph />
        </button>
      </div>
    </div>
  )
}

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext)
  if (!ctx) throw new Error('useToast must be used inside <ToastProvider>')
  return ctx
}
