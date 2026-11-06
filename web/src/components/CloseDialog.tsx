import { useCallback, useEffect, useRef, useState } from 'react'
import { closeClaimedBy } from '@/api/client'
import { useBulkClose, useCloseIssue } from '@/api/mutations'
import { useWriteBlock } from '@/api/hooks'
import { Dialog, Field } from './Dialog'
import { ReadOnlyNote } from './ReadOnlyBadge'
import { useToast } from './Toast'

/**
 * Closing needs a reason.
 *
 * The backend enforces it (400 on an empty reason) and so does this dialog,
 * because a closed backlog with no reasons is unreadable six weeks later —
 * "why did we drop this?" is the single most common question a manager asks of
 * closed work. The suggestions are one click so the common cases cost nothing.
 */
const SUGGESTIONS = ['fixed', 'done', 'wontfix', 'duplicate', 'obsolete', 'superseded']

export function CloseDialog({
  project,
  ids,
  onClose,
  onClosed,
}: {
  project: string
  ids: string[]
  onClose: () => void
  onClosed?: (ids: string[]) => void
}) {
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  // The assignee bd named when it refused the close because another actor
  // holds the issue; null while there is no claim to confirm past.
  const [claimedBy, setClaimedBy] = useState<string | null>(null)
  const confirmRef = useRef<HTMLButtonElement>(null)
  const close = useCloseIssue(project)
  const bulk = useBulkClose(project)
  const toast = useToast()
  const writeBlock = useWriteBlock(project)

  useEffect(() => {
    if (claimedBy !== null) confirmRef.current?.focus()
  }, [claimedBy])

  const submit = async (force = false) => {
    const text = reason.trim()
    if (!text || busy || writeBlock) return
    setBusy(true)
    try {
      if (ids.length === 1) {
        // --force goes to bd only from the confirm below, after the user has
        // seen whose claim it overrides.
        await close.mutateAsync({ id: ids[0]!, reason: text, force })
        toast.push({ tone: 'ok', title: `Closed ${ids[0]}` })
      } else {
        // One request, one `bd close id1 id2 …`, one invalidation — instead of
        // N POSTs and N refetch storms. (Review finding N3.)
        const result = await bulk.mutateAsync({ ids, reason: text })
        toast.push({ tone: 'ok', title: `Closed ${result.updated} issues` })
      }
      onClosed?.(ids)
      onClose()
    } catch (err) {
      const assignee = ids.length === 1 && !force ? closeClaimedBy(err) : undefined
      if (assignee !== undefined) {
        setClaimedBy(assignee)
        return
      }
      // bd applies a multi-id close atomically, so a failure means nothing was
      // closed — say that plainly instead of implying a partial result.
      toast.error(ids.length === 1 ? `Could not close ${ids[0]}` : 'Could not close the issues', err)
    } finally {
      setBusy(false)
    }
  }

  // Stable, so Dialog's focus effect does not re-run on every render.
  const dismissClaim = useCallback(() => setClaimedBy(null), [])

  return (
    <Dialog
      title={ids.length === 1 ? `Close ${ids[0]}` : `Close ${ids.length} issues`}
      subtitle="bd records the reason on the bead; it is what makes a closed backlog readable later."
      // While the confirm is up, Escape and the scrim back out of the confirm
      // only, so the typed reason survives.
      onClose={claimedBy !== null ? dismissClaim : onClose}
      footer={
        claimedBy !== null ? (
          <>
            <button type="button" className="btn btn-quiet" data-secondary onClick={dismissClaim}>
              Go back
            </button>
            <button
              ref={confirmRef}
              type="button"
              className="btn"
              disabled={busy || Boolean(writeBlock)}
              title={writeBlock ?? undefined}
              style={
                busy || writeBlock
                  ? { opacity: 0.5 }
                  : { background: 'var(--accent)', color: 'var(--accent-text)', boxShadow: 'none' }
              }
              onClick={() => void submit(true)}
            >
              {busy ? 'Closing…' : 'Close anyway'}
            </button>
          </>
        ) : (
          <>
            <button type="button" className="btn btn-quiet" data-secondary onClick={onClose}>
              Cancel
            </button>
            <button
              type="button"
              className="btn"
              disabled={!reason.trim() || busy || Boolean(writeBlock)}
              title={writeBlock ?? undefined}
              style={
                reason.trim() && !busy && !writeBlock
                  ? { background: 'var(--accent)', color: 'var(--accent-text)', boxShadow: 'none' }
                  : { opacity: 0.5 }
              }
              onClick={() => void submit()}
            >
              {busy ? 'Closing…' : 'Close issue'}
            </button>
          </>
        )
      }
    >
      {writeBlock && <ReadOnlyNote reason={writeBlock} />}

      {claimedBy !== null && (
        <div
          role="alert"
          className="mb-4 rounded-md border px-3 py-2 text-[12px] text-ink"
          style={{
            borderColor: 'var(--warn)',
            background: 'color-mix(in oklab, var(--warn) 12%, transparent)',
          }}
        >
          <p className="font-semibold">Close anyway?</p>
          <p className="mt-0.5 text-muted">
            {ids[0]} is claimed by <strong className="text-ink">{claimedBy || 'another actor'}</strong>.
            Closing it overrides their claim.
          </p>
        </div>
      )}

      <Field label="Reason" hint="Required. Shown on the card and in bd's close_reason.">
        <input
          className="field w-full"
          value={reason}
          placeholder="fixed, wontfix, superseded by loom-x…"
          onChange={(e) => setReason(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              // With the confirm up, Enter must not send a second plain close;
              // the user answers the confirm with its own buttons.
              if (claimedBy === null) void submit()
            }
          }}
        />
      </Field>

      <div className="mt-3 flex flex-wrap gap-1.5">
        {SUGGESTIONS.map((s) => (
          <button
            key={s}
            type="button"
            className="chip"
            data-tone="neutral"
            data-outline="true"
            onClick={() => setReason(s)}
          >
            {s}
          </button>
        ))}
      </div>
    </Dialog>
  )
}
