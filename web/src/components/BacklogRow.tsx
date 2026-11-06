import clsx from 'clsx'
import type { Issue } from '@/api/types'
import { formatEstimate, sortLabels, statusLabel } from '@/lib/beads'
import { relativeTime, absoluteTime } from '@/lib/time'
import type { IssueGroup } from '@/lib/grouping'
import {
  Assignee,
  ChildProgress,
  DepBadges,
  LabelChip,
  PriorityBadge,
  ReadinessMark,
  StaleBadge,
  StatusDot,
} from './primitives'
import { useLabelRules } from './labels'
import { TypeGlyph } from './TypeGlyph'

/**
 * One backlog row — 30px, thirteen columns, no wrapping anywhere.
 *
 * The 2px status rail on the left edge is doing real work: read down the
 * gutter and you see the shape of the project (how much cyan is moving, how
 * much red is stuck) before you read a single word. It also means status
 * costs zero horizontal space in a row that is already fighting for it.
 */
export function BacklogRow({
  issue,
  focused,
  open: isOpen,
  selected,
  staged,
  onOpen,
  onFocus,
  onToggleSelect,
  onOpenMenu,
  staleDays,
}: {
  issue: Issue
  focused: boolean
  /** The row whose detail sheet is showing. */
  open: boolean
  selected: boolean
  /** True when the issue is in the summon queue — client-local, not a status. */
  staged?: boolean
  onOpen: () => void
  onFocus: () => void
  onToggleSelect: (shift: boolean) => void
  onOpenMenu: (
    kind: 'status' | 'priority' | 'assignee' | 'labels',
    anchor: DOMRect,
  ) => void
  /** Days since last touch, when the backend has flagged this row as stale. */
  staleDays?: number
}) {
  const labelRules = useLabelRules()
  const closed = issue.status === 'closed'
  const estimate = formatEstimate(issue.estimate)

  return (
    <div
      role="row"
      tabIndex={-1}
      data-status={issue.status}
      data-row={issue.id}
      data-focused={focused || undefined}
      aria-selected={selected}
      onClick={(e) => {
        // Shift / cmd click selects instead of opening — the universal
        // convention, and the only way to build a range without the keyboard.
        if (e.shiftKey || e.metaKey) {
          e.preventDefault()
          onToggleSelect(e.shiftKey)
          return
        }
        onOpen()
      }}
      onMouseEnter={onFocus}
      className={clsx(
        'group relative flex w-full cursor-pointer items-center gap-2 pr-3 pl-3.5 text-left',
        'transition-colors duration-[--dur-fast]',
        selected || isOpen ? 'bg-selected' : focused ? 'bg-hover' : 'hover:bg-hover',
      )}
      style={{ height: 'var(--row-h)' }}
    >
      <span className="rail" aria-hidden="true" />
      {focused && (
        <span
          className="pointer-events-none absolute inset-0 rounded-xs"
          style={{ boxShadow: 'inset 0 0 0 1px var(--accent)' }}
          aria-hidden="true"
        />
      )}

      <RowCheckbox
        checked={selected}
        onToggle={onToggleSelect}
        label={issue.id}
      />

      <button
        type="button"
        className="shrink-0"
        onClick={(e) => {
          e.stopPropagation()
          onOpenMenu('priority', e.currentTarget.getBoundingClientRect())
        }}
        title="Change priority (p)"
      >
        <PriorityBadge priority={issue.priority} />
      </button>

      <button
        type="button"
        className="flex shrink-0 items-center"
        onClick={(e) => {
          e.stopPropagation()
          onOpenMenu('status', e.currentTarget.getBoundingClientRect())
        }}
        title={`${statusLabel(issue.status)} — change status (s)`}
      >
        <StatusDot status={issue.status} title={statusLabel(issue.status)} />
      </button>

      <span
        className="mono w-[var(--id-col)] shrink-0 truncate text-[11px] text-faint"
        title={issue.id}
      >
        {issue.id}
      </span>

      <span className="text-faint" title={issue.type}>
        <TypeGlyph type={issue.type} />
      </span>

      <span
        className={clsx(
          'min-w-0 flex-1 truncate text-[12.5px]',
          closed ? 'text-faint line-through decoration-1' : 'text-ink',
        )}
        title={issue.title}
      >
        {issue.title}
      </span>

      <ReadinessMark issue={issue} />

      {staged && (
        <span
          className="chip"
          data-tone="review"
          title="In the summon queue — client-local, the bead's status is untouched"
        >
          queued
        </span>
      )}

      {staleDays !== undefined && <StaleBadge days={staleDays} />}

      {issue.missingAcceptance && (
        <span className="chip" data-tone="warn" data-outline="true" title="Missing acceptance criteria">
          no acceptance
        </span>
      )}
      {issue.missingCloseReason && (
        <span className="chip" data-tone="warn" data-outline="true" title="Missing close reason">
          no close reason
        </span>
      )}

      {issue.childStats && (
        <ChildProgress total={issue.childStats.total} closed={issue.childStats.closed} started={issue.childStats.started} />
      )}

      <span className="hidden shrink-0 items-center gap-1 lg:flex">
        {sortLabels(issue.labels, labelRules).slice(0, 3).map((label) => (
          <button
            key={label}
            type="button"
            onClick={(e) => {
              e.stopPropagation()
              onOpenMenu('labels', e.currentTarget.getBoundingClientRect())
            }}
          >
            <LabelChip label={label} />
          </button>
        ))}
        {issue.labels.length > 3 && (
          <span className="mono text-[10px] text-faint" title={issue.labels.join(', ')}>
            +{issue.labels.length - 3}
          </span>
        )}
      </span>

      <DepBadges blockedBy={issue.depCounts.blockedBy} blocks={issue.depCounts.blocks} />

      {estimate && (
        <span
          className="mono hidden w-7 shrink-0 text-right text-[10.5px] text-faint xl:inline"
          title={`Estimate ${issue.estimate} minutes`}
        >
          {estimate}
        </span>
      )}

      <button
        type="button"
        className="shrink-0"
        onClick={(e) => {
          e.stopPropagation()
          onOpenMenu('assignee', e.currentTarget.getBoundingClientRect())
        }}
        title="Change assignee (a)"
      >
        <Assignee name={issue.assignee} />
      </button>

      <span
        className="mono w-9 shrink-0 text-right text-[10.5px] whitespace-nowrap text-faint"
        title={`Updated ${absoluteTime(issue.updated)}`}
      >
        {relativeTime(issue.updated)}
      </span>
    </div>
  )
}

/** Selection affordance: invisible until hover or selection, so 500 rows do
 *  not read as 500 checkboxes — but always present and always clickable. */
function RowCheckbox({
  checked,
  onToggle,
  label,
}: {
  checked: boolean
  onToggle: (shift: boolean) => void
  label: string
}) {
  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={checked}
      aria-label={`Select ${label}`}
      onClick={(e) => {
        e.stopPropagation()
        onToggle(e.shiftKey)
      }}
      className={clsx(
        'flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-xs transition-opacity duration-[--dur-fast]',
        checked ? 'opacity-100' : 'opacity-0 group-hover:opacity-60 focus-visible:opacity-100',
      )}
      style={{
        background: checked ? 'var(--accent)' : 'transparent',
        boxShadow: checked ? 'none' : 'inset 0 0 0 1px var(--border-strong)',
      }}
    >
      {checked && (
        <svg viewBox="0 0 10 10" width="9" height="9" aria-hidden="true" style={{ color: 'var(--accent-text)' }}>
          <path
            d="M1.6 5.2 3.9 7.5 8.4 3"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.8"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      )}
    </button>
  )
}

/**
 * Group header. Sticky, 26px, with the count in mono and — for groups that
 * contain open work — how many of those are actually *ready*, which is the
 * number a manager is really asking for.
 */
export function GroupHeader({
  group,
  collapsed,
  onToggle,
}: {
  group: IssueGroup
  collapsed: boolean
  onToggle: () => void
}) {
  const showReady = group.status !== 'closed' && group.readyCount > 0

  return (
    <button
      type="button"
      onClick={onToggle}
      aria-expanded={!collapsed}
      data-status={group.status}
      data-priority={group.priority}
      className="sticky top-0 flex w-full items-center gap-2 bg-surface/95 px-3 text-left backdrop-blur-[2px]"
      style={{ height: 'var(--group-h)', zIndex: 'var(--z-sticky)' }}
    >
      <Chevron collapsed={collapsed} />

      {/* An epic is named by id *and* title: two epics can share a title, and the
          id is what a manager types into the palette or the epic scope picker. */}
      {group.epicId && (
        <span className="mono shrink-0 text-[10.5px] text-faint">{group.epicId}</span>
      )}

      <span
        className="min-w-0 truncate text-[11px] font-semibold tracking-[0.04em]"
        style={{ color: group.status || group.priority !== undefined ? 'var(--c)' : 'var(--text-muted)' }}
      >
        {group.label}
      </span>

      <span className="mono shrink-0 text-[10.5px] text-faint">{group.issues.length}</span>

      {showReady && (
        <span className="mono shrink-0 text-[10.5px]" style={{ color: 'var(--accent)' }}>
          {group.readyCount} ready
        </span>
      )}

      <span className="hairline ml-1 flex-1 opacity-60" aria-hidden="true" />
    </button>
  )
}

function Chevron({ collapsed }: { collapsed: boolean }) {
  return (
    <svg
      viewBox="0 0 10 10"
      width="9"
      height="9"
      aria-hidden="true"
      className="text-faint transition-transform duration-[--dur-fast]"
      style={{ transform: collapsed ? 'rotate(-90deg)' : 'none', flex: 'none' }}
    >
      <path
        d="M2.2 3.6 5 6.6l2.8-3"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.3"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}
