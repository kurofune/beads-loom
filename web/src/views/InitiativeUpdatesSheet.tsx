import { useEffect, useMemo, useRef, useState } from 'react'
import clsx from 'clsx'
import type { InitiativeUpdate } from '@/api/types'
import { useInitiativeUpdates } from '@/api/hooks'
import { usePublishUpdate } from '@/api/mutations'
import { Markdown } from '@/components/Markdown'
import { CloseGlyph } from '@/components/CheatSheet'
import { Dialog } from '@/components/Dialog'
import { useInteractionLock } from '@/components/Live'
import { useToast } from '@/components/Toast'
import { DetailSkeleton, EmptyState, ErrorState } from '@/components/states'
import { linkifyCitations } from '@/lib/updates'
import { absoluteTime, relativeTime } from '@/lib/time'

/**
 * One epic's weekly update history.
 *
 * Everything on screen is text an agent wrote and Loom stored — Loom drafts
 * nothing. The sheet's whole job is to make a year of that readable and to
 * carry the one decision Loom owns: publishing a draft, which freezes it.
 *
 * Readability at 52 weeks comes from the shape, not from truncation: the newest
 * update is expanded, every older one is a one-line header button. Nothing is
 * hidden — 52 headers fit a scroll, 52 bodies do not.
 */
export function InitiativeUpdatesSheet({
  project,
  epic,
  onClose,
  onOpenIssue,
  onOpenRun,
}: {
  project: string
  epic: string
  onClose: () => void
  onOpenIssue: (id: string) => void
  /** Opens a cited run's detail page. Only runs the project has are cited as links. */
  onOpenRun: (id: string) => void
}) {
  const { data, isLoading, error, refetch } = useInitiativeUpdates(project, epic)
  const panelRef = useRef<HTMLElement>(null)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const [confirming, setConfirming] = useState<InitiativeUpdate | null>(null)

  const updates = useMemo(() => data?.updates ?? [], [data?.updates])
  const knownIssueIds = useMemo(() => data?.knownIssueIds ?? [], [data?.knownIssueIds])
  const knownRunIds = useMemo(() => data?.knownRunIds ?? [], [data?.knownRunIds])
  const newestWeek = updates[0]?.week

  // Nothing refetches under an open sheet or its confirm dialog.
  useInteractionLock(true)

  useEffect(() => {
    panelRef.current?.focus()
  }, [epic])

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || confirming) return
      event.preventDefault()
      event.stopPropagation()
      onClose()
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [confirming, onClose])

  const isExpanded = (update: InitiativeUpdate) =>
    expanded[update.week] ?? update.week === newestWeek

  const body = () => {
    if (isLoading) return <DetailSkeleton />
    if (error) {
      return (
        <ErrorState
          error={error}
          onRetry={() => {
            void refetch()
          }}
        />
      )
    }
    if (updates.length === 0) {
      return (
        <EmptyState
          title={`No stored updates for ${epic}`}
          detail={
            <>
              Weekly updates are written by an agent or a person, never by Loom. Store this
              week's draft with{' '}
              <span className="mono">bd kv set initiative-retro.{epic}.&lt;YYYY-Www&gt; '&lt;json&gt;'</span>{' '}
              (README, “Weekly initiative updates”), then review and publish it here.
            </>
          }
        />
      )
    }
    return (
      <ol className="pb-8">
        {updates.map((update) => (
          <UpdateRow
            key={update.week}
            update={update}
            expanded={isExpanded(update)}
            knownIssueIds={knownIssueIds}
            knownRunIds={knownRunIds}
            onToggle={() =>
              setExpanded((current) => ({
                ...current,
                [update.week]: !(current[update.week] ?? update.week === newestWeek),
              }))
            }
            onOpenIssue={onOpenIssue}
            onOpenRun={onOpenRun}
            onPublish={() => setConfirming(update)}
          />
        ))}
      </ol>
    )
  }

  return (
    <>
      <div
        className="scrim-in fixed inset-0"
        style={{ background: 'var(--scrim)', zIndex: 'var(--z-scrim)' }}
        onClick={onClose}
        role="presentation"
      />
      <aside
        ref={panelRef}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label={`Weekly updates for ${epic}`}
        className="panel-in fixed top-0 right-0 bottom-0 flex flex-col bg-raised outline-none"
        style={{
          width: 'min(var(--detail-w), 100vw)',
          zIndex: 'var(--z-panel)',
          boxShadow: 'var(--shadow-panel)',
        }}
      >
        <header
          className="flex shrink-0 items-center gap-2 border-b border-line px-5"
          style={{ height: 'var(--header-h)' }}
        >
          <span className="legend">Weekly updates</span>
          <span className="mono text-[11.5px] text-muted">{epic}</span>
          {updates.length > 0 && (
            <span className="text-[11px] text-faint">
              {updates.length} {updates.length === 1 ? 'update' : 'updates'}
            </span>
          )}
          <button
            type="button"
            className="icon-btn ml-auto"
            onClick={onClose}
            aria-label="Close weekly updates"
            title="Close (esc)"
          >
            <CloseGlyph />
          </button>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto">{body()}</div>
      </aside>

      {confirming && (
        <PublishDialog
          project={project}
          epic={epic}
          update={confirming}
          onClose={() => setConfirming(null)}
        />
      )}
    </>
  )
}

/**
 * One week. Collapsed it is a single header button; expanded it adds the body.
 * A draft carries a left border and a tinted background so the two states are
 * distinguishable from across the room, not only by reading the badge.
 */
function UpdateRow({
  update,
  expanded,
  knownIssueIds,
  knownRunIds,
  onToggle,
  onOpenIssue,
  onOpenRun,
  onPublish,
}: {
  update: InitiativeUpdate
  expanded: boolean
  knownIssueIds: string[]
  knownRunIds: string[]
  onToggle: () => void
  onOpenIssue: (id: string) => void
  onOpenRun: (id: string) => void
  onPublish: () => void
}) {
  const draft = update.status === 'draft'
  const rendered = useMemo(
    () => (expanded ? linkifyCitations(update.body, knownIssueIds, knownRunIds) : ''),
    [expanded, update.body, knownIssueIds, knownRunIds],
  )

  return (
    <li
      className={clsx('border-b border-line', draft && 'bg-sunken')}
      style={draft ? { borderLeft: '2px solid var(--warn)' } : { borderLeft: '2px solid transparent' }}
      data-week={update.week}
      data-status={update.status}
    >
      <button
        type="button"
        className="flex w-full items-center gap-2.5 px-5 py-2.5 text-left transition-colors hover:bg-hover"
        onClick={onToggle}
        aria-expanded={expanded}
        title={expanded ? `Collapse ${update.week}` : `Expand ${update.week}`}
      >
        <span className="text-[11px] text-faint" aria-hidden="true">
          {expanded ? '▾' : '▸'}
        </span>
        <span className="mono w-[70px] shrink-0 text-[12px] text-ink">{update.week}</span>
        <StatusBadge status={update.status} parseError={update.parseError} />
        <span
          className="mono ml-auto shrink-0 text-[10.5px] text-faint"
          title={absoluteTime(update.generatedAt)}
        >
          {relativeTime(update.generatedAt)}
        </span>
      </button>

      {expanded && (
        <div className="px-5 pb-4 pl-[calc(1.25rem+18px)]">
          {update.parseError ? (
            <p className="mono rounded-sm bg-overlay px-2.5 py-2 text-[11px] leading-relaxed text-muted ring-1 ring-line">
              This week's stored value could not be read: {update.parseError}
            </p>
          ) : (
            <Markdown onIssueLink={onOpenIssue} onRunLink={onOpenRun}>
              {rendered}
            </Markdown>
          )}

          {draft && !update.parseError && (
            <div className="mt-3 flex items-center gap-2">
              <button
                type="button"
                className="btn h-6 text-[11.5px]"
                onClick={onPublish}
                title="Publish this draft — a published update is never overwritten"
              >
                Publish…
              </button>
              <span className="text-[11px] text-faint">
                Publishing freezes this week's update.
              </span>
            </div>
          )}
        </div>
      )}
    </li>
  )
}

function StatusBadge({ status, parseError }: { status: string; parseError: string }) {
  if (parseError) {
    return (
      <span className="chip" data-tone="danger" data-solid="true">
        UNREADABLE
      </span>
    )
  }
  if (status === 'draft') {
    return (
      <span className="chip" data-tone="warn" data-solid="true">
        DRAFT
      </span>
    )
  }
  if (status === 'published') {
    return (
      <span className="chip" data-tone="accent">
        PUBLISHED
      </span>
    )
  }
  return (
    <span className="chip" data-tone="neutral" data-outline="true">
      {status || 'unknown'}
    </span>
  )
}

/**
 * The publish confirm.
 *
 * It names the epic and the week and states the consequence before the button,
 * because publishing cannot be undone through Loom. Failures render here rather
 * than as a toast that outlives the dialog: the citation refusal names the
 * offending claim, and the operator needs it in front of the draft.
 */
function PublishDialog({
  project,
  epic,
  update,
  onClose,
}: {
  project: string
  epic: string
  update: InitiativeUpdate
  onClose: () => void
}) {
  const publish = usePublishUpdate(project)
  const toast = useToast()
  const [failure, setFailure] = useState<string | null>(null)

  useInteractionLock(true)

  const submit = () => {
    if (publish.isPending) return
    setFailure(null)
    publish.mutate(
      { epic, week: update.week, bodySha: update.bodySha },
      {
        onSuccess: () => {
          toast.push({ tone: 'ok', title: `Published ${epic} ${update.week}` })
          onClose()
        },
        // bd's own stderr, the citation refusal and the conflict text all arrive
        // as ApiError.detail; showing it verbatim is the point.
        onError: (err) => setFailure(err.detail),
      },
    )
  }

  return (
    <Dialog
      title={`Publish ${epic} ${update.week}?`}
      subtitle="A published update is frozen — Loom never overwrites one."
      onClose={onClose}
      footer={
        <>
          <button type="button" className="btn btn-quiet" data-secondary onClick={onClose}>
            Cancel
          </button>
          <button type="button" className="btn" onClick={submit} disabled={publish.isPending}>
            {publish.isPending ? 'Publishing…' : 'Publish'}
          </button>
        </>
      }
    >
      <p className="text-[12px] leading-relaxed text-muted">
        This marks the stored draft for <span className="mono">{epic}</span>{' '}
        <span className="mono">{update.week}</span> as published. Its text is not changed and it
        cannot be edited or re-published afterwards.
      </p>
      {failure && (
        <p
          role="alert"
          className="mono mt-3 rounded-sm px-2.5 py-2 text-[11px] leading-relaxed whitespace-pre-wrap"
          style={{
            color: 'var(--danger)',
            background: 'color-mix(in oklab, var(--danger) 10%, transparent)',
          }}
        >
          {failure}
        </p>
      )}
    </Dialog>
  )
}
