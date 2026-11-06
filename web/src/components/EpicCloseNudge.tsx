import { useEffect, useMemo, useState } from 'react'
import type { Initiative } from '@/api/types'
import { epicNudgeSignature, isEpicNudgeDismissed } from '@/lib/epic-nudge'
import { CloseDialog } from './CloseDialog'
import { Dialog } from './Dialog'
import { useInteractionLock } from './Live'

const STORAGE_KEY = 'loom.epicNudge.'

function readDismissed(project: string): string | null {
  try {
    return sessionStorage.getItem(STORAGE_KEY + project)
  } catch {
    return null
  }
}

function storeDismissed(project: string, signature: string): void {
  try {
    sessionStorage.setItem(STORAGE_KEY + project, signature)
  } catch {
    // Private mode can reject writes; this advisory must never take down the board.
  }
}

export function EpicCloseNudge({
  project,
  eligible,
  readOnly,
}: {
  project: string
  eligible: Initiative[]
  readOnly: boolean
}) {
  const signature = useMemo(() => epicNudgeSignature(eligible), [eligible])
  const [dismissed, setDismissed] = useState(() => readDismissed(project))
  const [panelOpen, setPanelOpen] = useState(false)
  const [closeDialogOpen, setCloseDialogOpen] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())

  useEffect(() => setDismissed(readDismissed(project)), [project])
  useInteractionLock(panelOpen || closeDialogOpen)

  if (eligible.length === 0 || readOnly || isEpicNudgeDismissed(dismissed, signature)) {
    return null
  }

  const openPanel = () => {
    setSelected(new Set(eligible.map((initiative) => initiative.id)))
    setPanelOpen(true)
  }
  const selectedIDs = eligible
    .map((initiative) => initiative.id)
    .filter((id) => selected.has(id))

  return (
    <>
      <div className="flex items-center gap-0.5">
        <button
          type="button"
          className="btn btn-quiet h-[26px] text-[11.5px]"
          onClick={openPanel}
        >
          {eligible.length} {eligible.length === 1 ? 'epic' : 'epics'} ready to close
        </button>
        <button
          type="button"
          className="btn btn-quiet h-[26px] px-1.5 text-[12px]"
          aria-label="Dismiss"
          title="Dismiss for this eligible set"
          onClick={() => {
            storeDismissed(project, signature)
            setDismissed(signature)
          }}
        >
          ×
        </button>
      </div>

      {panelOpen && (
        <Dialog
          title="Epics ready to close"
          subtitle="Every descendant is closed. Choose the epics to close out."
          onClose={() => setPanelOpen(false)}
          width={620}
          footer={
            <>
              <button
                type="button"
                className="btn btn-quiet"
                data-secondary
                onClick={() => setPanelOpen(false)}
              >
                Cancel
              </button>
              <button
                type="button"
                className="btn"
                disabled={selectedIDs.length === 0}
                style={selectedIDs.length > 0
                  ? { background: 'var(--accent)', color: 'var(--accent-text)', boxShadow: 'none' }
                  : { opacity: 0.5 }}
                onClick={() => {
                  setPanelOpen(false)
                  setCloseDialogOpen(true)
                }}
              >
                Close {selectedIDs.length} {selectedIDs.length === 1 ? 'epic' : 'epics'}…
              </button>
            </>
          }
        >
          <div
            data-testid="eligible-epics-scroll"
            className="max-h-[min(50vh,320px)] overflow-y-auto"
          >
            {eligible.map((initiative) => (
              <label
                key={initiative.id}
                className="flex cursor-pointer items-start gap-3 border-b border-line px-1 py-2.5 last:border-0"
              >
                <input
                  type="checkbox"
                  checked={selected.has(initiative.id)}
                  onChange={(event) => {
                    setSelected((current) => {
                      const next = new Set(current)
                      if (event.target.checked) next.add(initiative.id)
                      else next.delete(initiative.id)
                      return next
                    })
                  }}
                />
                <span className="mono w-[110px] shrink-0 text-[11px] text-muted">
                  {initiative.id}
                </span>
                <span className="min-w-0 flex-1 text-[12px] text-ink">{initiative.title}</span>
                <span className="mono shrink-0 text-[10.5px] text-faint">
                  {initiative.subtree.closed}/{initiative.subtree.total}
                </span>
              </label>
            ))}
          </div>
        </Dialog>
      )}

      {closeDialogOpen && (
        <CloseDialog
          project={project}
          ids={selectedIDs}
          onClose={() => setCloseDialogOpen(false)}
          onClosed={() => {
            setCloseDialogOpen(false)
            setPanelOpen(false)
          }}
        />
      )}
    </>
  )
}
