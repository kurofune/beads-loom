import { useMemo } from 'react'
import type { Issue, IssuePatch, IssueStatus, Priority } from '@/api/types'
import { useProjectSchema, useWriteBlock } from '@/api/hooks'
import { useBulkPatch, usePatchIssue } from '@/api/mutations'
import { isActive, priorityLabel, statusLabel } from '@/lib/beads'
import { Menu, type MenuItem, type MenuRequest } from './Menu'
import { useToast } from './Toast'
import { useLabelRules } from './labels'
import { LabelChip } from './primitives'

/**
 * The inline editors: status (`s`), priority (`p`), assignee (`a`), labels (`l`).
 *
 * One component for all four, and one component whether the target is a single
 * row or a multi-selection — a bulk edit is the same menu with more ids, which
 * is why there is no separate "bulk status" UI to keep in sync.
 */

/** Seeds from ARCHITECTURE.md §Bead field conventions, plus whatever the
 *  project actually uses. Free text is always available. */
const SEED_LABELS = [
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
  'layer-api',
  'review-follow-up',
]

export function EditMenu({
  project,
  request,
  issues,
  onClose,
  onRequestIssueClose,
}: {
  project: string
  request: MenuRequest
  /** The rows currently on screen — the source of current values and options. */
  issues: Issue[]
  onClose: () => void
  /** Picking "closed" needs a reason, so it is escalated to the dialog. */
  onRequestIssueClose: (ids: string[]) => void
}) {
  const toast = useToast()
  const patch = usePatchIssue(project)
  const bulk = useBulkPatch(project)
  const labelRules = useLabelRules()
  const { statuses } = useProjectSchema(project)
  const writeBlock = useWriteBlock(project)

  const targets = useMemo(
    () => issues.filter((i) => request.ids.includes(i.id)),
    [issues, request.ids],
  )

  /** Single id → PATCH; several → one bulk invocation, which is one `bd update
   *  id1 id2 …` on the server rather than N round trips. */
  const submit = (body: IssuePatch, describe: string) => {
    if (writeBlock) return
    const ids = request.ids
    const done = (err: unknown) => {
      if (err) toast.error(`Could not ${describe}`, err)
    }
    if (ids.length === 1) {
      patch.mutate({ id: ids[0]!, patch: body }, { onError: done })
    } else {
      bulk.mutate(
        { ids, patch: body },
        {
          onError: done,
          onSuccess: (res) => toast.push({ tone: 'ok', title: `Updated ${res.updated} issues` }),
        },
      )
    }
  }

  /** With several issues selected, a value is "current" only if they agree. */
  const shared = <T,>(pick: (i: Issue) => T): T | undefined => {
    if (targets.length === 0) return undefined
    const first = pick(targets[0]!)
    return targets.every((i) => pick(i) === first) ? first : undefined
  }

  if (request.kind === 'status') {
    const current = shared((i) => i.status)
    const items: MenuItem[] = statuses.map(({ name: status }) => ({
      id: status,
      label: statusLabel(status),
      selected: status === current,
      render: (
        <span className="flex items-center gap-2 truncate">
          <span className="dot" data-status={status} data-filled={isActive(status)} />
          {statusLabel(status)}
        </span>
      ),
      hint: status === 'closed' ? 'needs reason' : undefined,
    }))
    return (
      <Menu
        anchor={request.anchor}
        disabledReason={writeBlock}
        title={label('Status', request.ids.length)}
        items={items}
        onClose={onClose}
        onPick={(status) => {
          // `close` is a distinct bd verb that records a reason — never a
          // status patch. Hand off to the dialog instead of faking it.
          if (writeBlock) return
          if (status === 'closed') {
            onClose()
            onRequestIssueClose(request.ids)
            return
          }
          submit({ status: status as IssueStatus }, 'change status')
        }}
      />
    )
  }

  if (request.kind === 'priority') {
    const current = shared((i) => i.priority)
    const items: MenuItem[] = ([0, 1, 2, 3, 4] as Priority[]).map((p) => ({
      id: String(p),
      label: priorityLabel(p),
      keywords: p === 0 ? 'urgent critical' : p === 4 ? 'backlog someday' : '',
      selected: p === current,
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
    }))
    return (
      <Menu
        anchor={request.anchor}
        disabledReason={writeBlock}
        title={label('Priority', request.ids.length)}
        items={items}
        onClose={onClose}
        onPick={(v) => submit({ priority: Number(v) as Priority }, 'change priority')}
      />
    )
  }

  if (request.kind === 'assignee') {
    const current = shared((i) => i.assignee)
    const known = [...new Set(issues.map((i) => i.assignee).filter(Boolean))].sort()
    const items: MenuItem[] = [
      { id: '', label: 'Unassigned', selected: current === '' },
      ...known.map((name) => ({ id: name, label: name, selected: name === current })),
    ]
    return (
      <Menu
        anchor={request.anchor}
        disabledReason={writeBlock}
        title={label('Assignee', request.ids.length)}
        items={items}
        onClose={onClose}
        placeholder="Filter or type a name…"
        onCreate={(name) => submit({ assignee: name }, 'change assignee')}
        createLabel={(v) => `Assign to "${v}"`}
        onPick={(name) => submit({ assignee: name }, 'change assignee')}
      />
    )
  }

  // Labels — multi-select, and the only menu that stays open between picks.
  const inUse = [...new Set(issues.flatMap((i) => i.labels))]
  const seeds = [...SEED_LABELS, ...labelRules.filter((r) => r.seed).map((r) => r.name)]
  const options = [...new Set([...inUse, ...seeds])].sort()
  // With several issues selected, a label is "on" only when all of them carry
  // it; picking it then adds it to the ones missing it.
  const onAll = (label_: string) => targets.length > 0 && targets.every((i) => i.labels.includes(label_))

  const items: MenuItem[] = options.map((l) => ({
    id: l,
    label: l,
    render: <LabelChip label={l} />,
    selected: onAll(l),
    hint: targets.some((i) => i.labels.includes(l)) && !onAll(l) ? 'some' : undefined,
  }))

  return (
    <Menu
      anchor={request.anchor}
      disabledReason={writeBlock}
      title={label('Labels', request.ids.length)}
      items={items}
      multi
      onClose={onClose}
      placeholder="Filter or type a label…"
      onCreate={(l) => submit({ labelsAdd: [l] }, 'add label')}
      createLabel={(v) => `Add "${v}"`}
      onPick={(l) =>
        onAll(l) ? submit({ labelsRemove: [l] }, 'remove label') : submit({ labelsAdd: [l] }, 'add label')
      }
    />
  )
}

const label = (kind: string, count: number) => (count > 1 ? `${kind} · ${count} issues` : kind)
