import { useMemo, useState } from 'react'
import type { CreateIssueRequest, Issue, IssueType, Priority } from '@/api/types'
import { useCreateIssue } from '@/api/mutations'
import { useProjectSchema, useWriteBlock } from '@/api/hooks'
import { priorityLabel } from '@/lib/beads'
import { Dialog, Field } from './Dialog'
import { ReadOnlyNote } from './ReadOnlyBadge'
import { Menu, type MenuItem } from './Menu'
import { TypeGlyph } from './TypeGlyph'
import { LabelChip } from './primitives'
import { useLabelRules } from './labels'
import { useToast } from './Toast'
import { CriteriaEditor } from './CriteriaEditor'
import { isSubmitEnter } from '@/lib/enter'

const LABEL_SEEDS = [
  'phase-1',
  'phase-2',
  'phase-3',
  'phase-4',
  'phase-5',
  'phase-6',
  'layer-schema',
  'layer-service',
  'layer-route',
  'layer-ui',
  'layer-test',
  'review-follow-up',
]

/**
 * Quick create (`c`) — the four bead prose fields, done properly.
 *
 * The one thing this screen must get right is **acceptance criteria**. bd's
 * convention is one criterion per non-blank line, and a plain textarea invites
 * paragraphs that silently become a single unusable criterion. So it is a line
 * editor: each row is one criterion, reorderable, with the convention stated
 * rather than assumed.
 */
export function QuickCreate({
  project,
  issues,
  onClose,
  onCreated,
}: {
  project: string
  /** Existing issues, for the parent picker and label suggestions. */
  issues: Issue[]
  onClose: () => void
  onCreated: (id: string) => void
}) {
  const create = useCreateIssue(project)
  const { types } = useProjectSchema(project)
  const writeBlock = useWriteBlock(project)
  const toast = useToast()

  const [title, setTitle] = useState('')
  const [type, setType] = useState<IssueType>('task')
  const [priority, setPriority] = useState<Priority>(2)
  const [parent, setParent] = useState<Issue | null>(null)
  const [labels, setLabels] = useState<string[]>([])
  const [description, setDescription] = useState('')
  const [criteria, setCriteria] = useState<string[]>([''])
  const [design, setDesign] = useState('')
  const [notes, setNotes] = useState('')
  const [estimate, setEstimate] = useState('')
  const [busy, setBusy] = useState(false)
  const [menu, setMenu] = useState<{ kind: 'type' | 'priority' | 'parent' | 'labels'; anchor: DOMRect } | null>(null)

  const labelRules = useLabelRules()
  const labelOptions = useMemo(
    () =>
      [
        ...new Set([
          ...issues.flatMap((i) => i.labels),
          ...LABEL_SEEDS,
          ...labelRules.filter((r) => r.seed).map((r) => r.name),
        ]),
      ].sort(),
    [issues, labelRules],
  )

  const submit = async () => {
    if (!title.trim() || busy || writeBlock) return
    setBusy(true)
    const body: CreateIssueRequest = {
      title: title.trim(),
      type,
      priority,
      // Blank lines are dropped here as well as on render — the convention is
      // one criterion per non-blank line, so an empty row is simply not one.
      acceptance: criteria.map((c) => c.trim()).filter(Boolean).join('\n'),
      description: description.trim(),
      design: design.trim(),
      notes: notes.trim(),
      labels,
      ...(parent ? { parent: parent.id } : {}),
      ...(estimate.trim() ? { estimate: Number(estimate) } : {}),
    }
    try {
      const created = await create.mutateAsync(body)
      toast.push({ tone: 'ok', title: `Created ${created.id}` })
      onCreated(created.id)
    } catch (err) {
      toast.error('Could not create the issue', err)
    } finally {
      setBusy(false)
    }
  }

  const openMenu = (kind: 'type' | 'priority' | 'parent' | 'labels') =>
    (e: React.MouseEvent<HTMLButtonElement>) =>
      setMenu({ kind, anchor: e.currentTarget.getBoundingClientRect() })

  return (
    <>
      <Dialog
        title="New issue"
        subtitle={`bd create in ${project}`}
        onClose={onClose}
        width={620}
        footer={
          <>
            <span className="mr-auto text-[11px] text-faint">
              <kbd className="kbd">⌘</kbd> <kbd className="kbd">↵</kbd> to create
            </span>
            <button type="button" className="btn btn-quiet" data-secondary onClick={onClose}>
              Cancel
            </button>
            <button
              type="button"
              className="btn"
              disabled={!title.trim() || busy || Boolean(writeBlock)}
              title={writeBlock ?? undefined}
              style={
                title.trim() && !busy && !writeBlock
                  ? { background: 'var(--accent)', color: 'var(--accent-text)', boxShadow: 'none' }
                  : { opacity: 0.5 }
              }
              onClick={() => void submit()}
            >
              {busy ? 'Creating…' : 'Create issue'}
            </button>
          </>
        }
      >
        {writeBlock && <ReadOnlyNote reason={writeBlock} />}
        <div
          className="space-y-4"
          onKeyDown={(e) => {
            if (isSubmitEnter(e) && (e.metaKey || e.ctrlKey)) {
              e.preventDefault()
              void submit()
            }
          }}
        >
          <input
            className="field w-full text-[14px]"
            style={{ height: 34 }}
            value={title}
            placeholder="Title — what needs to happen"
            onChange={(e) => setTitle(e.target.value)}
            aria-label="Title"
          />

          {/* Attribute row: everything that is a single value, in one line. */}
          <div className="flex flex-wrap items-center gap-1.5">
            <button type="button" className="btn btn-quiet h-6 text-[11.5px]" onClick={openMenu('type')}>
              <TypeGlyph type={type} />
              {type}
            </button>
            <button
              type="button"
              className="btn btn-quiet h-6 text-[11.5px]"
              onClick={openMenu('priority')}
              data-priority={priority}
            >
              <span className="mono" style={{ color: 'var(--c)' }}>
                {priorityLabel(priority)}
              </span>
            </button>
            <button type="button" className="btn btn-quiet h-6 text-[11.5px]" onClick={openMenu('parent')}>
              {parent ? (
                <span className="mono max-w-[180px] truncate">↑ {parent.id}</span>
              ) : (
                'No parent'
              )}
            </button>
            <button type="button" className="btn btn-quiet h-6 text-[11.5px]" onClick={openMenu('labels')}>
              Labels{labels.length > 0 ? ` · ${labels.length}` : ''}
            </button>
            <input
              className="field w-[104px]"
              value={estimate}
              inputMode="numeric"
              placeholder="est. min"
              onChange={(e) => setEstimate(e.target.value.replace(/[^\d]/g, ''))}
              aria-label="Estimate in minutes"
            />
          </div>

          {labels.length > 0 && (
            <div className="flex flex-wrap gap-1.5">
              {labels.map((l) => (
                <button key={l} type="button" onClick={() => setLabels((v) => v.filter((x) => x !== l))} title={`Remove ${l}`}>
                  <LabelChip label={l} />
                </button>
              ))}
            </div>
          )}

          {parent && (
            <p className="rounded-md bg-sunken px-3 py-2 text-[11.5px] text-faint ring-1 ring-line">
              Inherits the brief from <span className="mono text-muted">{parent.id}</span> —{' '}
              {parent.title}
            </p>
          )}

          <Field label="Description" hint="What and why. Markdown.">
            <textarea
              className="field w-full resize-y py-1.5"
              style={{ height: 76 }}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </Field>

          <CriteriaEditor value={criteria} onChange={setCriteria} />

          <div className="grid grid-cols-2 gap-4">
            <Field label="Design" hint="Where to look; file paths.">
              <textarea
                className="field w-full resize-y py-1.5"
                style={{ height: 60 }}
                value={design}
                onChange={(e) => setDesign(e.target.value)}
              />
            </Field>
            <Field label="Notes" hint="How it fits the larger plan.">
              <textarea
                className="field w-full resize-y py-1.5"
                style={{ height: 60 }}
                value={notes}
                onChange={(e) => setNotes(e.target.value)}
              />
            </Field>
          </div>
        </div>
      </Dialog>

      {menu?.kind === 'type' && (
        <Menu
          anchor={menu.anchor}
          title="Type"
          items={types.map<MenuItem>((t) => ({
            id: t,
            label: t,
            selected: t === type,
            render: (
              <span className="flex items-center gap-2">
                <TypeGlyph type={t} />
                {t}
              </span>
            ),
          }))}
          onPick={(t) => setType(t as IssueType)}
          onClose={() => setMenu(null)}
        />
      )}

      {menu?.kind === 'priority' && (
        <Menu
          anchor={menu.anchor}
          title="Priority"
          items={([0, 1, 2, 3, 4] as Priority[]).map<MenuItem>((p) => ({
            id: String(p),
            label: priorityLabel(p),
            selected: p === priority,
            render: (
              <span className="flex items-center gap-2" data-priority={p}>
                <span className="mono w-6 text-[11px]" style={{ color: 'var(--c)' }}>
                  {priorityLabel(p)}
                </span>
                <span className="truncate text-faint">
                  {['critical', 'high', 'medium', 'low', 'backlog'][p]}
                </span>
              </span>
            ),
          }))}
          onPick={(p) => setPriority(Number(p) as Priority)}
          onClose={() => setMenu(null)}
        />
      )}

      {menu?.kind === 'parent' && (
        <Menu
          anchor={menu.anchor}
          title="Parent"
          placeholder="Search issues…"
          items={[
            { id: '', label: 'No parent', selected: parent === null },
            // Epics first: the overwhelmingly common parent, and the one that
            // carries the shared brief.
            ...[...issues]
              .sort((a, b) => Number(b.type === 'epic') - Number(a.type === 'epic'))
              .slice(0, 80)
              .map<MenuItem>((i) => ({
                id: i.id,
                label: i.title,
                keywords: `${i.id} ${i.type}`,
                selected: parent?.id === i.id,
                hint: i.id,
                render: (
                  <span className="flex min-w-0 items-center gap-2">
                    <TypeGlyph type={i.type} />
                    <span className="truncate">{i.title}</span>
                  </span>
                ),
              })),
          ]}
          onPick={(id) => setParent(id ? (issues.find((i) => i.id === id) ?? null) : null)}
          onClose={() => setMenu(null)}
        />
      )}

      {menu?.kind === 'labels' && (
        <Menu
          anchor={menu.anchor}
          title="Labels"
          multi
          placeholder="Filter or type a label…"
          items={labelOptions.map<MenuItem>((l) => ({
            id: l,
            label: l,
            render: <LabelChip label={l} />,
            selected: labels.includes(l),
          }))}
          onCreate={(l) => setLabels((v) => (v.includes(l) ? v : [...v, l]))}
          createLabel={(v) => `Add "${v}"`}
          onPick={(l) => setLabels((v) => (v.includes(l) ? v.filter((x) => x !== l) : [...v, l]))}
          onClose={() => setMenu(null)}
        />
      )}
    </>
  )
}
