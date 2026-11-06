import { useState } from 'react'
import type { Initiative } from '@/api/types'
import { usePatchIssue } from '@/api/mutations'
import { NO_TARGET_LABEL, targetNudgeReason } from '@/lib/target-nudge'
import { Dialog, Field } from './Dialog'
import { useInteractionLock } from './Live'
import { useToast } from './Toast'

/**
 * The missing-target nudge: an epic that has descendants and no due date, so
 * the health ladder cannot say whether it will land (loom-o6m).
 *
 * Render and interaction follow the close-eligible nudge (EpicCloseNudge):
 * a badge, hover text saying why the epic is flagged, a click opening a Dialog
 * that holds the interaction lock, and a persisted dismiss. Two deliberate
 * differences: the dismiss is per-epic, because the question ("what date is
 * this epic for?") is per-epic; and the panel is not read-only prose — it sets
 * the date, or takes the epic out of the running for good with the bd-native
 * no-target label. Both go through the existing PATCH write path, so neither
 * costs a new bd verb.
 *
 * The badge and the panel are separate components on purpose. Initiatives rows
 * are virtualized and carry a `transform`, which makes a `position: fixed`
 * scrim resolve against the ROW instead of the viewport — so the panel renders
 * at the view root, where IssueDetail and the updates sheet already live.
 */
export function EpicTargetBadge({
  initiative,
  dismissed,
  readOnly,
  onOpen,
}: {
  initiative: Initiative
  dismissed: boolean
  readOnly: boolean
  onOpen: () => void
}) {
  if (!initiative.needsTarget || dismissed || readOnly) return null

  const reason = targetNudgeReason(initiative)
  return (
    <button
      type="button"
      className="chip cursor-pointer"
      data-tone="warn"
      data-outline="true"
      data-testid={`target-nudge-${initiative.id}`}
      title={reason}
      aria-label={`Set a target for ${initiative.id}. ${reason}`}
      onClick={(event) => {
        event.stopPropagation()
        onOpen()
      }}
    >
      set date
    </button>
  )
}

/** Offsets a manager actually plans in, as whole days from today. */
const PRESETS: { label: string; days: number }[] = [
  { label: '1 week', days: 7 },
  { label: '2 weeks', days: 14 },
  { label: '1 month', days: 30 },
]

/** `YYYY-MM-DD` for a date input, in the browser's own calendar days. */
export function dateInputValue(from: Date, addDays = 0): string {
  const day = new Date(from)
  day.setDate(day.getDate() + addDays)
  const month = `${day.getMonth() + 1}`.padStart(2, '0')
  const date = `${day.getDate()}`.padStart(2, '0')
  return `${day.getFullYear()}-${month}-${date}`
}

export function EpicTargetPanel({
  project,
  initiative,
  onClose,
  onDismiss,
}: {
  project: string
  initiative: Initiative
  onClose: () => void
  /** Persist a per-epic dismissal; the owner decides where it is stored. */
  onDismiss: (epic: string) => void
}) {
  // Two weeks out is a starting point, not a default: pre-filled so the common
  // case is one click, and a real editable value, never a silent guess.
  const [due, setDue] = useState(() => dateInputValue(new Date(), 14))
  const [busy, setBusy] = useState(false)
  const patch = usePatchIssue(project)
  const toast = useToast()

  useInteractionLock(true)

  const reason = targetNudgeReason(initiative)

  const setTarget = async () => {
    if (!due || busy) return
    setBusy(true)
    try {
      await patch.mutateAsync({ id: initiative.id, patch: { due } })
      toast.push({ tone: 'ok', title: `${initiative.id} is due ${due}` })
      onClose()
    } catch (err) {
      toast.error(`Could not set a due date on ${initiative.id}`, err)
    } finally {
      setBusy(false)
    }
  }

  const markUntimed = async () => {
    if (busy) return
    setBusy(true)
    try {
      await patch.mutateAsync({ id: initiative.id, patch: { labelsAdd: [NO_TARGET_LABEL] } })
      toast.push({ tone: 'ok', title: `${initiative.id} marked untimed` })
      onClose()
    } catch (err) {
      toast.error(`Could not label ${initiative.id} ${NO_TARGET_LABEL}`, err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      title={`Set a target for ${initiative.id}`}
      subtitle={reason}
      onClose={onClose}
      width={520}
      footer={
        <>
          <button
            type="button"
            className="btn btn-quiet"
            data-secondary
            disabled={busy}
            onClick={() => {
              onDismiss(initiative.id)
              onClose()
            }}
          >
            Dismiss
          </button>
          <button
            type="button"
            className="btn btn-quiet"
            data-secondary
            disabled={busy}
            onClick={() => void markUntimed()}
          >
            Not time-boxed
          </button>
          <button
            type="button"
            className="btn"
            disabled={!due || busy}
            style={due && !busy
              ? { background: 'var(--accent)', color: 'var(--accent-text)', boxShadow: 'none' }
              : { opacity: 0.5 }}
            onClick={() => void setTarget()}
          >
            {busy ? 'Saving…' : 'Set target'}
          </button>
        </>
      }
    >
      <p className="mb-3 text-[12px] leading-relaxed text-muted">
        <span className="font-medium text-ink">{initiative.title}</span> —{' '}
        {initiative.subtree.closed} of {initiative.subtree.total} descendants closed,{' '}
        {initiative.subtree.started} started. With a date, health projects the trailing close rate
        against what the remaining work needs.
      </p>

      <Field label="Due date" hint="Written to the bead with bd update --due.">
        <input
          type="date"
          className="field w-full"
          value={due}
          min={dateInputValue(new Date())}
          onChange={(event) => setDue(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault()
              void setTarget()
            }
          }}
        />
      </Field>

      <div className="mt-3 flex flex-wrap items-center gap-1.5">
        <span className="legend mr-1">from today</span>
        {PRESETS.map((preset) => (
          <button
            key={preset.label}
            type="button"
            className="chip cursor-pointer"
            data-tone="neutral"
            data-outline="true"
            onClick={() => setDue(dateInputValue(new Date(), preset.days))}
          >
            {preset.label}
          </button>
        ))}
      </div>

      <p className="mt-4 border-t border-line pt-3 text-[11px] leading-relaxed text-faint">
        <span className="font-medium">Not time-boxed</span> tags the epic{' '}
        <span className="mono">{NO_TARGET_LABEL}</span> and stops the prompt for good — the same
        opt-out as <span className="mono">bd tag {initiative.id} {NO_TARGET_LABEL}</span>.{' '}
        <span className="font-medium">Dismiss</span> only hides it until this epic needs a target
        again.
      </p>
    </Dialog>
  )
}
