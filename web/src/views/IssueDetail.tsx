import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import clsx from 'clsx'
import type { IssueDetail as Detail, IssueEvent, IssueRef } from '@/api/types'
import { useIssue } from '@/api/hooks'
import { usePatchIssue, useReopenIssue } from '@/api/mutations'
import { ProseOrDash } from '@/components/Markdown'
import { CloseGlyph } from '@/components/CheatSheet'
import { CommentComposer } from '@/components/CommentComposer'
import { CriteriaEditor } from '@/components/CriteriaEditor'
import { Section } from '@/components/DetailSection'
import { useLabelRules } from '@/components/labels'
import { useIntegrations } from '@/integrations'
import { InitiativeHealth } from '@/components/InitiativeHealth'
import { CloseDialog } from '@/components/CloseDialog'
import { EditMenu } from '@/components/EditMenu'
import { useToast } from '@/components/Toast'
import { useInteractionLock } from '@/components/Live'
import { rememberIssue } from '@/lib/recent'
import type { MenuKind, MenuRequest } from '@/components/Menu'
import { DetailSkeleton, ErrorState } from '@/components/states'
import { TypeGlyph, typeColor } from '@/components/TypeGlyph'
import {
  Assignee,
  ChildProgress,
  DeferredChip,
  LabelChip,
  PriorityBadge,
  StatusChip,
  StatusDot,
} from '@/components/primitives'
import {
  describeEvent,
  formatEstimate,
  notReadyReason,
  sortLabels,
  splitCriteria,
  statusLabel,
} from '@/lib/beads'
import { absoluteTime, dateOnly, relativeTime } from '@/lib/time'

type ProseField = 'description' | 'acceptance' | 'design' | 'notes'

/**
 * The issue sheet.
 *
 * Laid out as a datasheet, not a card stack: a fixed 92px left gutter of
 * uppercase legends against a single content column. Every section — meta,
 * prose field, dependency bucket — hangs off the same gutter, so the eye has
 * one rail to travel down instead of hunting card headers.
 */
export function IssueDetail({
  project,
  id,
  onClose,
  onNavigate,
}: {
  project: string
  id: string
  onClose: () => void
  onNavigate: (id: string) => void
}) {
  const { data, isLoading, error, refetch } = useIssue(project, id)
  const panelRef = useRef<HTMLElement>(null)

  // Move focus into the sheet so screen readers and Tab both follow it.
  // Esc is bound in BacklogView, which owns both routes.
  useEffect(() => {
    panelRef.current?.focus()
  }, [id])

  // Record the visit with the title, not just the id: the palette no longer
  // holds a corpus it could look the title up in (loom-nlm), and the title is
  // right here on screen. Runs again when the detail resolves, so an entry
  // written before the fetch lands is filled in rather than left bare.
  useEffect(() => {
    rememberIssue(project, id, data?.title ?? '')
  }, [data?.title, id, project])

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
        aria-label={data ? `${data.id} ${data.title}` : 'Issue'}
        className="panel-in fixed top-0 right-0 bottom-0 flex flex-col bg-raised outline-none"
        style={{
          width: 'min(var(--detail-w), 100vw)',
          zIndex: 'var(--z-panel)',
          boxShadow: 'var(--shadow-panel)',
        }}
      >
        {isLoading && <DetailSkeleton />}
        {error && (
          <div className="flex h-full flex-col">
            <SheetHeader id={id} onClose={onClose} />
            <div className="flex-1">
              <ErrorState error={error} onRetry={refetch} />
            </div>
          </div>
        )}
        {data && (
          <SheetBody
            key={data.id}
            project={project}
            issue={data}
            onClose={onClose}
            onNavigate={onNavigate}
          />
        )}
      </aside>
    </>
  )
}

function SheetHeader({
  id,
  type,
  onClose,
}: {
  id: string
  type?: string
  onClose: () => void
}) {
  return (
    <header className="flex shrink-0 items-center gap-2 border-b border-line px-5" style={{ height: 'var(--header-h)' }}>
      {type && (
        <span className="text-faint" title={type}>
          <TypeGlyph type={type} />
        </span>
      )}
      <span className="mono text-[11.5px] text-muted">{id}</span>
      {type && <span className="text-[11px] font-medium" style={{ color: typeColor(type) }}>{type}</span>}
      <button
        type="button"
        className="icon-btn ml-auto"
        onClick={onClose}
        aria-label="Close issue"
        title="Close (esc)"
      >
        <CloseGlyph />
      </button>
    </header>
  )
}

function SheetBody({
  project,
  issue,
  onClose,
  onNavigate,
}: {
  project: string
  issue: Detail
  onClose: () => void
  onNavigate: (id: string) => void
}) {
  const mounted = useIntegrations(project)
  const labelRules = useLabelRules()
  const criteria = splitCriteria(issue.acceptanceCriteria)
  const { deps } = issue
  const estimate = formatEstimate(issue.estimate)
  const toast = useToast()
  const reopen = useReopenIssue(project)
  const patchIssue = usePatchIssue(project)
  const [menu, setMenu] = useState<MenuRequest | null>(null)
  const [closing, setClosing] = useState(false)
  const [editing, setEditing] = useState<ProseField | null>(null)
  const [draft, setDraft] = useState('')
  const [criteriaDraft, setCriteriaDraft] = useState<string[]>([''])
  const [confirmClear, setConfirmClear] = useState<ProseField | null>(null)

  const openEditor = (field: ProseField) => {
    setConfirmClear(null)
    if (field === 'acceptance') {
      const rows = issue.acceptanceCriteria.split('\n').filter((line) => line.trim().length > 0)
      setCriteriaDraft(rows.length > 0 ? rows : [''])
    } else {
      setDraft(issue[field])
    }
    setEditing(field)
  }

  const cancelEditor = () => {
    setConfirmClear(null)
    setEditing(null)
  }

  const commitEditor = (field: ProseField, value: string, failedCriteria?: string[]) => {
    const patch =
      field === 'description'
        ? { description: value }
        : field === 'acceptance'
          ? { acceptance: value }
          : field === 'design'
            ? { design: value }
            : { notes: value }
    patchIssue.mutate(
      { id: issue.id, patch },
      {
        onSuccess: () => toast.push({ tone: 'ok', title: `Updated ${field} of ${issue.id}` }),
        onError: (err) => {
          toast.error(`Could not update ${field} of ${issue.id}`, err)
          if (field === 'acceptance') {
            setCriteriaDraft(failedCriteria ?? (value ? value.split('\n') : ['']))
          } else {
            setDraft(value)
          }
          setEditing(field)
        },
      },
    )
    setConfirmClear(null)
    setEditing(null)
  }

  const saveEditor = (field: Exclude<ProseField, 'acceptance'>) => {
    if (draft.trim() === '') {
      if (issue[field] !== '') setConfirmClear(field)
      else setEditing(null)
      return
    }
    commitEditor(field, draft)
  }

  const saveAcceptance = () => {
    const acceptance = criteriaDraft.map((row) => row.trim()).filter(Boolean).join('\n')
    if (acceptance === '') {
      if (issue.acceptanceCriteria !== '') setConfirmClear('acceptance')
      else setEditing(null)
      return
    }
    commitEditor('acceptance', acceptance, criteriaDraft)
  }

  // Same guard as the list views: nothing refetches under an open popover.
  useInteractionLock(Boolean(menu) || closing || editing !== null)

  /** Every metadata chip in this sheet is also a button; the popovers are the
   *  same ones the backlog rows and board cards open. */
  const openMenu = (kind: MenuKind) => (e: React.MouseEvent<HTMLElement>) =>
    setMenu({ kind, anchor: e.currentTarget.getBoundingClientRect(), ids: [issue.id] })

  return (
    <>
      <SheetHeader id={issue.id} type={issue.type} onClose={onClose} />

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="px-5 py-4">
          <h2 className="text-[15px] leading-snug font-semibold text-ink">{issue.title}</h2>
        </div>

        {/* — Metadata datasheet ——————————————————————————————————————— */}
        <Section legend="Status">
          <div className="flex flex-wrap items-center gap-2">
            <button type="button" onClick={openMenu('status')} title="Change status (s)">
              <StatusChip status={issue.status} />
            </button>
            <button type="button" onClick={openMenu('priority')} title="Change priority (p)">
              <PriorityBadge priority={issue.priority} />
            </button>
            {/* Three outcomes, not two: an issue deferred to next sprint is
                not blocked by anything, and saying "blocked by ?" about it is
                false. (Review finding I2.) */}
            {issue.status === 'open' &&
              (issue.ready ? (
                <span className="chip" data-tone="accent">
                  ready
                </span>
              ) : notReadyReason(issue) === 'deferred' ? (
                <DeferredChip issue={issue} />
              ) : (
                <span className="chip" data-tone="danger" data-solid="true">
                  blocked by {issue.depCounts.blockedBy || '?'}
                </span>
              ))}
            {issue.pinned && (
              <span className="chip" data-status="pinned">
                pinned
              </span>
            )}
          </div>
          {issue.closeReason && (
            <p className="mt-2 text-[12px] text-muted">
              <span className="text-faint">closed:</span> {issue.closeReason}
            </p>
          )}

          <div className="mt-2.5 flex items-center gap-2">
            {issue.status === 'closed' ? (
              <button
                type="button"
                className="btn h-6 text-[11.5px]"
                onClick={() =>
                  reopen.mutate(
                    { id: issue.id },
                    {
                      onSuccess: () => toast.push({ tone: 'ok', title: `Reopened ${issue.id}` }),
                      onError: (err) => toast.error(`Could not reopen ${issue.id}`, err),
                    },
                  )
                }
              >
                {reopen.isPending ? 'Reopening…' : 'Reopen'}
              </button>
            ) : (
              <button
                type="button"
                className="btn h-6 text-[11.5px]"
                onClick={() => setClosing(true)}
                title="bd close — a reason is required"
              >
                Close issue…
              </button>
            )}
            {mounted.issueActions.map((action) => (
              <action.component key={action.id} project={project} issue={issue} />
            ))}
          </div>
        </Section>

        {issue.type === 'epic' && issue.health && (
          <Section legend="Health">
            <div className="flex items-start gap-2.5">
              <InitiativeHealth health={issue.health} />
              <p className="text-[12px] leading-relaxed text-muted">{issue.health.explanation}</p>
            </div>
          </Section>
        )}

        <Section legend="Assignee">
          <div className="flex items-center gap-2">
            <button
              type="button"
              className="flex items-center gap-2"
              onClick={openMenu('assignee')}
              title="Change assignee (a)"
            >
              <Assignee name={issue.assignee} size={20} />
              <span className={clsx('text-[12.5px]', issue.assignee ? 'text-ink' : 'text-faint')}>
                {issue.assignee || 'Unassigned'}
              </span>
            </button>
            {issue.owner && issue.owner !== issue.assignee && (
              <span className="text-[11.5px] text-faint">· owner {issue.owner}</span>
            )}
          </div>
        </Section>

        <Section legend="Labels">
          <div className="flex flex-wrap items-center gap-1.5">
            {sortLabels(issue.labels, labelRules).map((label) => (
              <button key={label} type="button" onClick={openMenu('labels')} title="Edit labels (l)">
                <LabelChip label={label} />
              </button>
            ))}
            <button
              type="button"
              className="chip"
              data-tone="neutral"
              data-outline="true"
              onClick={openMenu('labels')}
              title="Edit labels (l)"
            >
              + label
            </button>
          </div>
        </Section>

        <Section legend="Dates">
          <dl className="grid grid-cols-2 gap-x-6 gap-y-1.5 text-[11.5px]">
            <Fact term="created" value={relativeTime(issue.created)} title={absoluteTime(issue.created)} />
            <Fact term="updated" value={relativeTime(issue.updated)} title={absoluteTime(issue.updated)} />
            {issue.due && <Fact term="due" value={dateOnly(issue.due)} />}
            {issue.deferUntil && <Fact term="deferred until" value={dateOnly(issue.deferUntil)} />}
            {issue.closed && (
              <Fact term="closed" value={relativeTime(issue.closed)} title={absoluteTime(issue.closed)} />
            )}
            {estimate && <Fact term="estimate" value={estimate} title={`${issue.estimate} minutes`} />}
          </dl>
        </Section>

        {/* — Epic-as-brief ————————————————————————————————————————————
            The parent epic's description carries the shared brief (diagnostic
            question, reference standard), so a child is not readable without
            one click to its parent. This card is that click. */}
        {deps.parent && (
          <Section legend="Parent brief">
            <button
              type="button"
              onClick={() => onNavigate(deps.parent!.id)}
              className="group flex w-full items-start gap-2.5 rounded-md bg-sunken px-3 py-2.5 text-left ring-1 ring-line transition-colors duration-[--dur-fast] hover:bg-hover"
            >
              <StatusDot status={deps.parent.status} />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-[12.5px] text-ink">{deps.parent.title}</span>
                <span className="mono mt-0.5 block text-[10.5px] text-faint">
                  {deps.parent.id} · {statusLabel(deps.parent.status)}
                </span>
              </span>
              <span className="text-[11px] text-faint group-hover:text-muted">open →</span>
            </button>
          </Section>
        )}

        {/* — The four bead prose fields, each visibly distinct ————————— */}
        <Section legend="Description">
          {editing === 'description' ? (
            <ProseTextEditor
              field="description"
              value={draft}
              onChange={setDraft}
              onSave={() => saveEditor('description')}
              onCancel={cancelEditor}
              confirmClear={confirmClear === 'description'}
              onKeepEditing={() => setConfirmClear(null)}
              onClear={() => commitEditor('description', '')}
            />
          ) : (
            <div className="flex items-start gap-3">
              <div className="min-w-0 flex-1">
                <ProseOrDash value={issue.description} />
              </div>
              <ProseEditButton field="description" onEdit={openEditor} />
            </div>
          )}
        </Section>

        <Section legend="Acceptance">
          {editing === 'acceptance' ? (
            <div
              onKeyDown={(event) => {
                if (event.key === 'Escape') {
                  event.preventDefault()
                  event.stopPropagation()
                  cancelEditor()
                } else if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
                  event.preventDefault()
                  saveAcceptance()
                }
              }}
            >
              <CriteriaEditor value={criteriaDraft} onChange={setCriteriaDraft} autoFocus />
              <div className="mt-2 flex justify-end gap-1.5">
                <button
                  type="button"
                  className="btn btn-quiet h-6 text-[11.5px]"
                  onClick={cancelEditor}
                >
                  Cancel
                </button>
                <button
                  type="button"
                  className="btn h-6 text-[11.5px]"
                  aria-label="Save acceptance"
                  onClick={saveAcceptance}
                >
                  Save
                </button>
              </div>
              {confirmClear === 'acceptance' && (
                <ClearConfirm
                  field="acceptance"
                  onClear={() => commitEditor('acceptance', '')}
                  onKeepEditing={() => setConfirmClear(null)}
                />
              )}
            </div>
          ) : (
            <div className="flex items-start gap-3">
              <div className="min-w-0 flex-1">
                {criteria.length === 0 ? (
                  <span className="text-faint">—</span>
                ) : (
                  <ul className="space-y-1.5">
                    {criteria.map((line, i) => (
                      <li key={i} className="flex items-start gap-2.5">
                        <span
                          className="mt-[3px] h-3 w-3 shrink-0 rounded-full border border-line-strong"
                          aria-hidden="true"
                        />
                        <span className="text-[12.5px] leading-[1.45] text-muted">{line}</span>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
              <ProseEditButton field="acceptance" onEdit={openEditor} />
            </div>
          )}
        </Section>

        <Section legend="Design">
          {editing === 'design' ? (
            <ProseTextEditor
              field="design"
              value={draft}
              onChange={setDraft}
              onSave={() => saveEditor('design')}
              onCancel={cancelEditor}
              confirmClear={confirmClear === 'design'}
              onKeepEditing={() => setConfirmClear(null)}
              onClear={() => commitEditor('design', '')}
            />
          ) : (
            <div className="flex items-start gap-3">
              <div className="min-w-0 flex-1">
                <ProseOrDash value={issue.design} />
              </div>
              <ProseEditButton field="design" onEdit={openEditor} />
            </div>
          )}
        </Section>

        <Section legend="Notes">
          {editing === 'notes' ? (
            <ProseTextEditor
              field="notes"
              value={draft}
              onChange={setDraft}
              onSave={() => saveEditor('notes')}
              onCancel={cancelEditor}
              confirmClear={confirmClear === 'notes'}
              onKeepEditing={() => setConfirmClear(null)}
              onClear={() => commitEditor('notes', '')}
            />
          ) : (
            <div className="flex items-start gap-3">
              <div className="min-w-0 flex-1">
                <ProseOrDash value={issue.notes} />
              </div>
              <ProseEditButton field="notes" onEdit={openEditor} />
            </div>
          )}
        </Section>

        {/* — Children rollup ——————————————————————————————————————————— */}
        {deps.children.length > 0 && (
          <Section
            legend="Children"
            aside={
              issue.childStats && (
                <ChildProgress total={issue.childStats.total} closed={issue.childStats.closed} started={issue.childStats.started} />
              )
            }
          >
            <RefList refs={deps.children} onNavigate={onNavigate} />
          </Section>
        )}

        {/* — Typed dependencies, styled by what they MEAN ——————————————— */}
        {deps.blockedBy.length > 0 && (
          <Section legend="Blocked by" tone="danger">
            <RefList refs={deps.blockedBy} onNavigate={onNavigate} loud />
          </Section>
        )}

        {deps.blocks.length > 0 && (
          <Section legend="Blocks">
            <RefList refs={deps.blocks} onNavigate={onNavigate} />
          </Section>
        )}

        {deps.discoveredFrom.length > 0 && (
          <Section legend="Discovered from">
            {/* Provenance, NOT a blocker — dashed rule and muted type so it
                never reads as something standing in the way. */}
            <p className="mb-2 text-[11px] text-faint">
              Audit trail. These do not affect readiness.
            </p>
            <div className="border-l border-dashed border-line pl-3">
              <RefList refs={deps.discoveredFrom} onNavigate={onNavigate} muted />
            </div>
          </Section>
        )}

        {mounted.detailSections.map((section) => (
          <section.component key={section.id} project={project} issueId={issue.id} onOpenIssue={onNavigate} />
        ))}

        <Section legend="Comments">
          {issue.comments.length > 0 && (
            <ul className="mb-3 space-y-3">
              {issue.comments.map((c) => (
                <li key={c.id}>
                  <p className="mb-1 flex items-baseline gap-2">
                    <span className="text-[11.5px] font-medium text-ink">{c.author}</span>
                    <span
                      className="mono text-[10.5px] text-faint"
                      title={absoluteTime(c.created)}
                    >
                      {relativeTime(c.created)}
                    </span>
                  </p>
                  <p className="text-[12.5px] leading-[1.5] text-muted">{c.text}</p>
                </li>
              ))}
            </ul>
          )}
          <CommentComposer project={project} id={issue.id} />
        </Section>

        {issue.events.length > 0 && (
          <Section legend="History">
            <ol className="space-y-1">
              {[...issue.events]
                .sort((a, b) => Date.parse(b.created) - Date.parse(a.created))
                .map((e, i) => (
                  <EventRow key={`${e.type}-${e.created}-${i}`} event={e} />
                ))}
            </ol>
          </Section>
        )}

        <div className="h-8" />
      </div>

      {menu && (
        <EditMenu
          project={project}
          request={menu}
          issues={[issue]}
          onClose={() => setMenu(null)}
          onRequestIssueClose={() => setClosing(true)}
        />
      )}

      {closing && (
        <CloseDialog
          project={project}
          ids={[issue.id]}
          onClose={() => setClosing(false)}
          onClosed={() => setClosing(false)}
        />
      )}
    </>
  )
}

function ProseTextEditor({
  field,
  value,
  onChange,
  onSave,
  onCancel,
  confirmClear,
  onKeepEditing,
  onClear,
}: {
  field: Exclude<ProseField, 'acceptance'>
  value: string
  onChange: (value: string) => void
  onSave: () => void
  onCancel: () => void
  confirmClear: boolean
  onKeepEditing: () => void
  onClear: () => void
}) {
  const label = field[0]!.toUpperCase() + field.slice(1)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  useLayoutEffect(() => {
    const textarea = textareaRef.current
    if (!textarea) return
    textarea.focus()
    textarea.setSelectionRange(value.length, value.length)
  }, [])

  return (
    <div
      onKeyDown={(event) => {
        if (event.key === 'Escape') {
          event.preventDefault()
          event.stopPropagation()
          onCancel()
        } else if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
          event.preventDefault()
          onSave()
        }
      }}
    >
      <textarea
        ref={textareaRef}
        className="field w-full resize-y py-1.5"
        style={{ minHeight: 92 }}
        aria-label={label}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
      <div className="mt-2 flex justify-end gap-1.5">
        <button type="button" className="btn btn-quiet h-6 text-[11.5px]" onClick={onCancel}>
          Cancel
        </button>
        <button
          type="button"
          className="btn h-6 text-[11.5px]"
          aria-label={`Save ${field}`}
          onClick={onSave}
        >
          Save
        </button>
      </div>
      {confirmClear && (
        <ClearConfirm field={field} onClear={onClear} onKeepEditing={onKeepEditing} />
      )}
    </div>
  )
}

function ClearConfirm({
  field,
  onClear,
  onKeepEditing,
}: {
  field: ProseField
  onClear: () => void
  onKeepEditing: () => void
}) {
  return (
    <div className="mt-2 flex items-center gap-2 rounded-md bg-sunken px-2.5 py-2 ring-1 ring-line">
      <span className="mr-auto text-[11.5px] text-muted">This clears {field}.</span>
      <button type="button" className="btn btn-quiet h-6 text-[11.5px]" onClick={onKeepEditing}>
        Keep editing
      </button>
      <button type="button" className="btn h-6 text-[11.5px]" onClick={onClear}>
        Clear
      </button>
    </div>
  )
}

function ProseEditButton({
  field,
  onEdit,
}: {
  field: ProseField
  onEdit: (field: ProseField) => void
}) {
  return (
    <button
      type="button"
      className="chip shrink-0"
      data-tone="neutral"
      data-outline="true"
      aria-label={`Edit ${field}`}
      onClick={() => onEdit(field)}
    >
      edit
    </button>
  )
}


function Fact({ term, value, title }: { term: string; value: string; title?: string }) {
  return (
    <div className="flex items-baseline gap-2">
      <dt className="w-[86px] shrink-0 text-faint">{term}</dt>
      <dd className="mono text-muted" title={title}>
        {value}
      </dd>
    </div>
  )
}

function RefList({
  refs,
  onNavigate,
  loud,
  muted,
}: {
  refs: IssueRef[]
  onNavigate: (id: string) => void
  loud?: boolean
  muted?: boolean
}) {
  return (
    <ul className="space-y-0.5">
      {refs.map((ref) => (
        <li key={ref.id}>
          <button
            type="button"
            onClick={() => onNavigate(ref.id)}
            className={clsx(
              'flex w-full items-center gap-2 rounded-sm py-1 pr-2 pl-1 text-left transition-colors duration-[--dur-fast] hover:bg-hover',
              loud && 'bg-[color-mix(in_oklab,var(--danger)_8%,transparent)]',
            )}
          >
            <StatusDot status={ref.status} />
            <PriorityBadge priority={ref.priority} />
            <span className="mono w-[70px] shrink-0 truncate text-[10.5px] text-faint">
              {ref.id}
            </span>
            <span
              className={clsx(
                'min-w-0 flex-1 truncate text-[12px]',
                muted ? 'text-faint' : 'text-muted',
              )}
              title={ref.title}
            >
              {ref.title}
            </span>
            {ref.status === 'open' && ref.ready && (
              <span className="chip" data-tone="accent">
                ready
              </span>
            )}
          </button>
        </li>
      ))}
    </ul>
  )
}

const EVENT_LABEL: Record<string, string> = {
  created: 'created',
  status_changed: 'status',
  priority_changed: 'priority',
  assignee_changed: 'assignee',
  closed: 'closed',
  reopened: 'reopened',
  commented: 'comment',
}

function EventRow({ event }: { event: IssueEvent }) {
  const label = EVENT_LABEL[event.type] ?? event.type.replace(/_/g, ' ')
  const change = describeEvent(event)

  return (
    <li className="flex items-baseline gap-2 text-[11.5px]">
      <span className="w-[62px] shrink-0 text-faint">{label}</span>
      {change ? (
        <span className="mono min-w-0 flex-1 truncate text-muted" title={change}>
          {change}
        </span>
      ) : (
        <span className="min-w-0 flex-1" />
      )}
      {event.actor && <span className="shrink-0 text-faint">{event.actor}</span>}
      <span className="mono shrink-0 text-faint" title={absoluteTime(event.created)}>
        {relativeTime(event.created)}
      </span>
    </li>
  )
}
