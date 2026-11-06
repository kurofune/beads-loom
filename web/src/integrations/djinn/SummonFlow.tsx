import { useCallback, useEffect, useReducer } from 'react'
import { useMutation } from '@tanstack/react-query'
import { api, ApiError } from '@/api/client'
import type { Issue, SummonPreview, SummonResponse } from '@/api/types'
import { Dialog } from '@/components/Dialog'
import { useToast } from '@/components/Toast'
import { useInteractionLock } from '@/components/Live'
import { PriorityBadge } from '@/components/primitives'
import { useIntegrationState } from '@/integrations'
import {
  canDispatch,
  DEFAULT_KNOBS,
  dispatchBody,
  droppedBeads,
  summonReducer,
  type SummonKnobs,
  type SummonPhase,
} from './summon'

/**
 * Summoner dispatch.
 *
 * Two screens, deliberately. The **preview** answers "what would happen"; the
 * **confirm** answers "do it". They are separate because dispatch spawns
 * autonomous agents against a real repository and spends real money, and the
 * single most valuable property of this flow is that neither of those can
 * happen without a person reading a list and pressing a red button.
 *
 * Cancel is the default action on both screens.
 */
export function SummonFlow({
  project,
  staged,
  issues,
  epic,
  defaultWorkers,
  onClose,
  onDispatched,
}: {
  project: string
  /** Bead ids from the tray. */
  staged: string[]
  /** For titles and priorities in the ordered list. */
  issues: Issue[]
  epic?: string
  defaultWorkers?: number
  onClose: () => void
  onDispatched: (runId: string) => void
}) {
  const [state, dispatch] = useReducer(summonReducer, { phase: 'idle' } as SummonPhase)
  const toast = useToast()
  useInteractionLock(true)
  const djinn = useIntegrationState(project, 'djinn')
  // Without dispatch even the dry run needs a summoner that is not there, so
  // the flow says why instead of failing a request.
  const blocked = djinn?.capabilities.includes('dispatch')
    ? null
    : (djinn?.reasons?.dispatch ?? `summoner cannot dispatch in ${project}`)

  const preview = useMutation<SummonPreview, ApiError, boolean>({
    mutationFn: (preflight) =>
      api.summonPreview(project, { beads: staged, ...(epic ? { epic } : {}), preflight }),
    onMutate: (preflight) => dispatch({ type: 'preview', preflight }),
    onSuccess: (result) => dispatch({ type: 'previewed', preview: result }),
    onError: (err) =>
      dispatch({ type: 'failed', message: err.message, ...(err.stderr ? { detail: err.stderr } : {}) }),
  })

  const summon = useMutation<SummonResponse, ApiError, void>({
    mutationFn: () => {
      if (state.phase !== 'dispatching') throw new Error('not confirmed')
      return api.summon(project, dispatchBody(state.preview, state.knobs, epic))
    },
    onSuccess: (result) => {
      dispatch({ type: 'dispatched' })
      toast.push({ tone: 'ok', title: `Summoned — run ${result.runId}` })
      onDispatched(result.runId)
    },
    onError: (err) => {
      toast.error('Could not summon', err)
      dispatch({ type: 'failed', message: err.message, ...(err.stderr ? { detail: err.stderr } : {}) })
    },
  })

  // The dry run fires once when the flow opens. It is non-mutating — summoner's
  // own --dry-run — so this is safe to do without asking.
  const runPreview = preview.mutate
  useEffect(() => {
    if (!blocked) runPreview(false)
  }, [blocked, runPreview])

  const confirm = useCallback(() => {
    dispatch({ type: 'confirm' })
  }, [])

  // `confirm` only flips the machine; the request fires off the resulting
  // state, so there is exactly one path into a dispatch.
  useEffect(() => {
    if (state.phase === 'dispatching' && !summon.isPending) summon.mutate()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.phase])

  const titleOf = (id: string) => issues.find((i) => i.id === id)?.title ?? ''
  const priorityOf = (id: string) => issues.find((i) => i.id === id)?.priority

  if (blocked) {
    return (
      <Dialog
        title="Summon preview"
        subtitle={`summoner cannot dispatch in ${project}.`}
        width={620}
        onClose={onClose}
        footer={
          <>
            <button type="button" className="btn btn-quiet" data-secondary onClick={onClose}>
              Cancel
            </button>
            <button type="button" className="btn" disabled title={blocked}>
              Continue…
            </button>
          </>
        }
      >
        <div className="space-y-3">
          <p className="text-[12.5px] leading-relaxed text-muted">{blocked}</p>
          {staged.length > 0 && (
            <ul className="space-y-0.5">
              {staged.map((id) => (
                <li key={id} className="flex items-baseline gap-2 text-[12px]">
                  <span className="mono text-[11px] text-faint">{id}</span>
                  <span className="truncate text-muted">{titleOf(id)}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </Dialog>
    )
  }

  if (state.phase === 'error') {
    // The one failure worth explaining rather than reporting: summoner is not
    // installed. That is a setup problem with a fix, not a transient error, and
    // "503 summoner not found in PATH" tells the user nothing they can act on.
    const missingSummoner = /summoner not found/i.test(state.message)

    return (
      <Dialog
        title={missingSummoner ? 'summoner is not installed' : 'Summon failed'}
        onClose={onClose}
        footer={
          <>
            <button type="button" className="btn btn-quiet" data-secondary onClick={onClose}>
              Close
            </button>
            {!missingSummoner && (
              <button type="button" className="btn" onClick={() => preview.mutate(false)}>
                Try the preview again
              </button>
            )}
          </>
        }
      >
        {missingSummoner ? (
          <div className="space-y-3">
            <p className="text-[12.5px] leading-relaxed text-muted">
              Loom dispatches work by running <span className="mono text-ink">summoner</span>,
              which spawns djinn workers in temporary worktrees. It is not on this machine's{' '}
              <span className="mono">PATH</span>, so there is nothing to dispatch with.
            </p>
            <div>
              <p className="legend mb-1.5">Install it</p>
              <pre className="mono rounded-sm bg-sunken px-2.5 py-2 text-[11px] leading-[1.6] text-muted ring-1 ring-line">
                {'cd ~/Projects/djinn\ngo install ./cmd/summoner\n\n# then confirm Loom can see it:\nwhich summoner'}
              </pre>
            </div>
            <p className="text-[11.5px] leading-relaxed text-faint">
              Loom looks it up on the <span className="mono">PATH</span> of the process it
              is running under, so if you installed it in another shell you may need to
              restart Loom. Everything else — the backlog, the board, the graph — works
              without it.
            </p>
          </div>
        ) : (
          <>
            <p className="text-[12.5px] text-ink">{state.message}</p>
            {state.detail && (
              <pre className="mono mt-2 max-h-48 overflow-auto rounded-sm bg-sunken px-2.5 py-2 text-[11px] leading-[1.5] whitespace-pre-wrap text-muted">
                {state.detail}
              </pre>
            )}
          </>
        )}
      </Dialog>
    )
  }

  if (state.phase === 'confirm' || state.phase === 'dispatching') {
    return (
      <ConfirmDialog
        preview={state.preview}
        knobs={state.knobs}
        epic={epic}
        busy={state.phase === 'dispatching'}
        onKnobs={(knobs) => dispatch({ type: 'knobs', knobs })}
        onBack={() => dispatch({ type: 'back' })}
        onConfirm={confirm}
        titleOf={titleOf}
      />
    )
  }

  const busy = state.phase === 'previewing'
  const result = state.phase === 'preview' ? state.preview : null
  const dropped = result ? droppedBeads(staged, result) : []

  return (
    <Dialog
      title="Summon preview"
      subtitle={
        busy
          ? `summoner --dry-run${(state as { preflight?: boolean }).preflight ? ' --preflight' : ''} in ${project}`
          : `What summoner would dispatch in ${project}. Nothing has run yet.`
      }
      width={620}
      onClose={onClose}
      footer={
        <>
          {result && !result.preflight && (
            <button
              type="button"
              className="btn btn-quiet mr-auto"
              onClick={() => preview.mutate(true)}
              disabled={busy}
              title="Runs an LLM check per bead for operator-required criteria. Takes about a minute."
            >
              Run deeper preflight check
            </button>
          )}
          <button type="button" className="btn btn-quiet" data-secondary onClick={onClose}>
            Cancel
          </button>
          <button
            type="button"
            className="btn"
            disabled={!result || !canDispatch(result) || busy}
            onClick={() =>
              result &&
              dispatch({
                type: 'proceed',
                knobs: { ...DEFAULT_KNOBS, maxWorkers: defaultWorkers ?? DEFAULT_KNOBS.maxWorkers },
              })
            }
          >
            Continue…
          </button>
        </>
      }
    >
      {busy && (
        <div className="flex items-center gap-2.5 py-6">
          <Spinner />
          <span className="text-[12.5px] text-muted">
            {(state as { preflight?: boolean }).preflight
              ? 'Running the deeper preflight check — this is an LLM pass per bead and takes about a minute…'
              : 'Asking summoner what it would dispatch…'}
          </span>
        </div>
      )}

      {result && (
        <div className="space-y-4">
          {/* The loud block. needs-operator means summoner physically cannot
              finish the bead unattended — the manager will be woken up. It
              comes first, before the happy list. */}
          {result.needsOperator.length > 0 && (
            <section
              className="rounded-md px-3 py-2.5"
              data-tone="warn"
              style={{
                background: 'color-mix(in oklab, var(--warn) 16%, transparent)',
                boxShadow: 'inset 0 0 0 1px color-mix(in oklab, var(--warn) 45%, transparent)',
              }}
            >
              <p className="flex items-center gap-2">
                <WarnTriangle />
                <span className="text-[12.5px] font-semibold text-ink">
                  {result.needsOperator.length} bead
                  {result.needsOperator.length === 1 ? '' : 's'} cannot finish unattended
                </span>
              </p>
              <p className="mt-1 text-[11.5px] leading-relaxed text-muted">
                These carry <span className="mono">needs-operator</span>. A worker will reach a
                step that requires a human and stop there — it will not fail cleanly, it will
                wait. Summon them only if you are around to answer.
              </p>
              <ul className="mt-2 space-y-1">
                {result.needsOperator.map((bead) => (
                  <li key={bead} className="flex items-baseline gap-2 text-[11.5px]">
                    <span className="mono shrink-0" style={{ color: 'var(--warn)' }}>
                      {bead}
                    </span>
                    <span className="min-w-0 truncate text-muted">{titleOf(bead)}</span>
                  </li>
                ))}
              </ul>
            </section>
          )}

          <section>
            <p className="legend mb-1.5">
              Dispatch order · {result.ordered.length} bead
              {result.ordered.length === 1 ? '' : 's'}
              {result.preflight && (
                <span className="ml-2" style={{ color: 'var(--accent)' }}>
                  preflight checked
                </span>
              )}
            </p>
            {result.ordered.length === 0 ? (
              <p className="rounded-md bg-sunken px-3 py-3 text-[12px] text-faint">
                summoner would dispatch nothing. Every staged bead is listed below with the
                reason.
              </p>
            ) : (
              <ol className="space-y-0.5">
                {result.ordered.map((bead, i) => (
                  <li key={bead} className="flex items-center gap-2 rounded-sm px-1 py-1">
                    <span className="mono w-5 shrink-0 text-right text-[10.5px] text-faint">
                      {i + 1}
                    </span>
                    {priorityOf(bead) !== undefined && <PriorityBadge priority={priorityOf(bead)!} />}
                    <span className="mono w-[92px] shrink-0 truncate text-[11px] text-muted">
                      {bead}
                    </span>
                    <span className="min-w-0 flex-1 truncate text-[12px] text-ink">
                      {titleOf(bead)}
                    </span>
                    {result.needsOperator.includes(bead) && (
                      <span className="chip shrink-0" data-tone="warn" data-solid="true">
                        needs operator
                      </span>
                    )}
                  </li>
                ))}
              </ol>
            )}
          </section>

          {dropped.length > 0 && (
            <section>
              <p className="legend mb-1.5">Staged but not dispatched · {dropped.length}</p>
              <ul className="space-y-1">
                {dropped.map(({ bead, reason }) => (
                  <li key={bead} className="flex items-baseline gap-2 text-[11.5px]">
                    <span className="mono w-[92px] shrink-0 truncate text-faint">{bead}</span>
                    <span className="min-w-0 flex-1 text-muted">{reason}</span>
                  </li>
                ))}
              </ul>
            </section>
          )}
        </div>
      )}
    </Dialog>
  )
}

function ConfirmDialog({
  preview,
  knobs,
  epic,
  busy,
  onKnobs,
  onBack,
  onConfirm,
  titleOf,
}: {
  preview: SummonPreview
  knobs: SummonKnobs
  epic?: string
  busy: boolean
  onKnobs: (knobs: Partial<SummonKnobs>) => void
  onBack: () => void
  onConfirm: () => void
  titleOf: (id: string) => string
}) {
  const workers = Math.min(knobs.maxWorkers, preview.ordered.length)

  return (
    <Dialog
      title={`Summon ${workers} worker${workers === 1 ? '' : 's'}?`}
      subtitle="This spawns autonomous agents against the real repository."
      width={520}
      onClose={onBack}
      footer={
        <>
          {/* Cancel is the default and the safe action, and it is first. */}
          <button type="button" className="btn" data-secondary onClick={onBack} disabled={busy}>
            Cancel
          </button>
          <button
            type="button"
            className="btn"
            onClick={onConfirm}
            disabled={busy}
            style={{
              background: 'var(--danger)',
              color: 'var(--text-inverted)',
              boxShadow: 'none',
              ...(busy ? { opacity: 0.6 } : {}),
            }}
          >
            {busy ? 'Summoning…' : `Summon ${workers} worker${workers === 1 ? '' : 's'}`}
          </button>
        </>
      }
    >
      <div className="space-y-4">
        <p
          className="flex items-start gap-2 rounded-md px-3 py-2.5 text-[11.5px] leading-relaxed"
          data-tone="danger"
          style={{ background: 'color-mix(in oklab, var(--danger) 12%, transparent)' }}
        >
          <WarnTriangle />
          <span className="text-muted">
            <span className="font-semibold text-ink">This spends real money.</span> Each worker
            runs a Claude session per bead until it closes the bead or halts. Cost is charged to
            whatever credentials djinn is configured with, and Loom cannot refund it.
          </span>
        </p>

        <dl className="grid grid-cols-[110px_1fr] gap-x-4 gap-y-2 text-[12px]">
          <dt className="text-faint">beads</dt>
          <dd className="text-ink">
            {preview.ordered.length}
            {preview.ordered.length > 0 && (
              <span className="mono ml-2 text-[11px] text-faint">
                {preview.ordered.slice(0, 3).join(', ')}
                {preview.ordered.length > 3 && ` +${preview.ordered.length - 3}`}
              </span>
            )}
          </dd>

          <dt className="text-faint">epic scope</dt>
          <dd className={epic ? 'mono text-ink' : 'text-faint'}>{epic ?? 'none — explicit bead list'}</dd>

          <dt className="text-faint">max workers</dt>
          <dd>
            <input
              type="number"
              min={1}
              max={16}
              className="field w-[76px]"
              value={knobs.maxWorkers}
              onChange={(e) => onKnobs({ maxWorkers: Math.max(1, Number(e.target.value) || 1) })}
              aria-label="Max workers"
            />
            <span className="ml-2 text-[11px] text-faint">concurrent djinn sessions</span>
          </dd>

          <dt className="text-faint">max beads</dt>
          <dd>
            <input
              type="number"
              min={0}
              className="field w-[76px]"
              value={knobs.maxBeads}
              onChange={(e) => onKnobs({ maxBeads: Math.max(0, Number(e.target.value) || 0) })}
              aria-label="Max beads"
            />
            <span className="ml-2 text-[11px] text-faint">0 = unbounded</span>
          </dd>

          <dt className="text-faint">push</dt>
          <dd>
            <label className="flex items-center gap-2 text-[11.5px] text-muted">
              <input
                type="checkbox"
                checked={knobs.noPush}
                onChange={(e) => onKnobs({ noPush: e.target.checked })}
              />
              <span>
                <span className="mono">--no-push</span> — local-only validation drain
              </span>
            </label>
          </dd>
        </dl>

        {preview.needsOperator.length > 0 && (
          <p className="rounded-md px-3 py-2 text-[11.5px] leading-relaxed" data-tone="warn"
            style={{ background: 'color-mix(in oklab, var(--warn) 14%, transparent)' }}
          >
            <span className="font-semibold text-ink">
              {preview.needsOperator.length} of these cannot finish unattended
            </span>{' '}
            <span className="text-muted">
              ({preview.needsOperator.map((b) => titleOf(b) || b).join('; ')}). A worker will stop
              and wait for you.
            </span>
          </p>
        )}
      </div>
    </Dialog>
  )
}

function WarnTriangle() {
  return (
    <svg viewBox="0 0 12 12" width="12" height="12" aria-hidden="true" className="mt-[2px] shrink-0" style={{ color: 'var(--c)' }}>
      <path d="M6 1.2 11.4 10.4H0.6z" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinejoin="round" />
      <path d="M6 4.6v2.6" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
      <circle cx="6" cy="8.7" r="0.6" fill="currentColor" />
    </svg>
  )
}

function Spinner() {
  return (
    <svg viewBox="0 0 12 12" width="14" height="14" aria-hidden="true" className="animate-spin text-faint">
      <circle cx="6" cy="6" r="4.2" fill="none" stroke="currentColor" strokeWidth="1.4" opacity="0.25" />
      <path d="M6 1.8a4.2 4.2 0 0 1 4.2 4.2" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  )
}
