import { useCallback, useEffect, useState } from 'react'
import clsx from 'clsx'
import {
  deleteView,
  describeView,
  loadViews,
  matchesView,
  saveView,
  type SavedView,
  type SavedViewInput,
} from '@/lib/saved-views'
import { Dialog, Field } from './Dialog'
import { useInteractionLock } from './Live'

/**
 * Saved views.
 *
 * localStorage is not a React store, so the two consumers — the sidebar list
 * and the save button — would otherwise drift apart the moment one wrote. A
 * module-level subscriber set keeps them in sync without dragging a global
 * state library in for four fields.
 */
const listeners = new Set<() => void>()
const notify = () => listeners.forEach((fn) => fn())

export function useSavedViews(project: string): SavedView[] {
  const [views, setViews] = useState<SavedView[]>(() => loadViews(project))

  const refresh = useCallback(() => setViews(loadViews(project)), [project])

  useEffect(() => {
    refresh()
    listeners.add(refresh)
    // Another tab saving a view should show up here too.
    window.addEventListener('storage', refresh)
    return () => {
      listeners.delete(refresh)
      window.removeEventListener('storage', refresh)
    }
  }, [refresh])

  return views
}

export function saveAndNotify(project: string, input: SavedViewInput): void {
  saveView(project, input)
  notify()
}

export function deleteAndNotify(project: string, id: string): void {
  deleteView(project, id)
  notify()
}

/* --- the save button ------------------------------------------------------ */

export function SaveViewButton({
  project,
  current,
}: {
  project: string
  current: SavedViewInput
}) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const views = useSavedViews(project)
  useInteractionLock(open)

  const existing = views.find((v) => matchesView(v, current))
  const isDefault =
    !current.query &&
    !current.staleOnly &&
    !current.includeClosed &&
    current.group === 'status' &&
    (current.hierarchy ?? 'all') === 'all'

  return (
    <>
      <button
        type="button"
        className="btn btn-quiet h-[26px] text-[11.5px]"
        onClick={() => {
          setName(existing?.name ?? '')
          setOpen(true)
        }}
        // Saving the untouched default view would just be a bookmark to the
        // page you are already on.
        disabled={isDefault}
        style={isDefault ? { opacity: 0.45, cursor: 'not-allowed' } : undefined}
        title={
          isDefault
            ? 'Change a filter or grouping first — there is nothing to save yet'
            : existing
              ? `This is the saved view “${existing.name}”`
              : 'Save this filter and grouping as a named view'
        }
      >
        {existing ? `✓ ${existing.name}` : 'Save view'}
      </button>

      {open && (
        <Dialog
          title="Save this view"
          subtitle={describeView({ ...current, id: '', name: '', createdAt: '' })}
          width={420}
          onClose={() => setOpen(false)}
          footer={
            <>
              <button type="button" className="btn btn-quiet" data-secondary onClick={() => setOpen(false)}>
                Cancel
              </button>
              <button
                type="button"
                className="btn"
                disabled={!name.trim()}
                style={
                  name.trim()
                    ? { background: 'var(--accent)', color: 'var(--accent-text)', boxShadow: 'none' }
                    : { opacity: 0.5 }
                }
                onClick={() => {
                  saveAndNotify(project, { ...current, name })
                  setOpen(false)
                }}
              >
                Save
              </button>
            </>
          }
        >
          <Field
            label="Name"
            hint={
              views.some((v) => v.name.toLowerCase() === name.trim().toLowerCase())
                ? 'A view with this name already exists and will be replaced.'
                : `Saved for ${project} only, in this browser.`
            }
          >
            <input
              className="field w-full"
              value={name}
              autoFocus
              placeholder="My P0s, UI layer, stale…"
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && name.trim()) {
                  e.preventDefault()
                  saveAndNotify(project, { ...current, name })
                  setOpen(false)
                }
              }}
            />
          </Field>
        </Dialog>
      )}
    </>
  )
}

/* --- the sidebar section -------------------------------------------------- */

export function SavedViewList({
  project,
  activeId,
  onApply,
}: {
  project: string
  activeId?: string
  onApply: (view: SavedView) => void
}) {
  const views = useSavedViews(project)
  const [confirming, setConfirming] = useState<string | null>(null)

  if (views.length === 0) return null

  return (
    <div className="mt-3">
      <p className="legend mb-1.5 px-2">Saved views</p>
      <ul className="space-y-0.5">
        {views.map((view) => (
          <li key={view.id} className="group flex items-center gap-1 px-1">
            <button
              type="button"
              onClick={() => onApply(view)}
              className={clsx(
                'min-w-0 flex-1 truncate rounded-sm px-1 py-1 text-left text-[11.5px] transition-colors duration-[--dur-fast]',
                view.id === activeId ? 'text-ink' : 'text-muted hover:text-ink',
              )}
              title={describeView(view)}
            >
              {view.id === activeId && (
                <span className="mr-1" style={{ color: 'var(--accent)' }}>
                  ›
                </span>
              )}
              {view.name}
            </button>

            {confirming === view.id ? (
              <span className="flex shrink-0 items-center gap-1">
                <button
                  type="button"
                  className="rounded-xs px-1 text-[10px]"
                  style={{ color: 'var(--danger)' }}
                  onClick={() => {
                    deleteAndNotify(project, view.id)
                    setConfirming(null)
                  }}
                >
                  delete
                </button>
                <button
                  type="button"
                  className="rounded-xs px-1 text-[10px] text-faint"
                  onClick={() => setConfirming(null)}
                >
                  keep
                </button>
              </span>
            ) : (
              <button
                type="button"
                className="shrink-0 rounded-xs px-1 text-[11px] text-faint opacity-0 group-hover:opacity-100 hover:text-ink"
                onClick={() => setConfirming(view.id)}
                aria-label={`Delete ${view.name}`}
                title="Delete this view"
              >
                ×
              </button>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}
