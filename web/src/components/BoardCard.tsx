import clsx from 'clsx'
import { useDraggable } from '@dnd-kit/core'
import type { Issue } from '@/api/types'
import { notReadyReason, sortLabels } from '@/lib/beads'
import { relativeTime, absoluteTime } from '@/lib/time'
import {
  Assignee,
  ChildProgress,
  DeferredChip,
  DepBadges,
  LabelChip,
  PriorityBadge,
} from './primitives'
import { useLabelRules } from './labels'
import { TypeGlyph, typeColor } from './TypeGlyph'

/**
 * A board card.
 *
 * Status is already encoded by the column, so the left edge and the type
 * glyph carry the issue type in its own hue (`--type-*`): an epic reads as
 * violet before its title is read.
 *
 * Readiness stays distinct: a dependency-blocked `open` card gets a red wash
 * over the whole card and a red "blocked by" chip; a ready one keeps its
 * chartreuse `ready` chip.
 */
export function BoardCard({
  issue,
  focused,
  selected,
  staged,
  dragging,
  onOpen,
  onFocus,
  onToggleSelect,
  onOpenMenu,
}: {
  issue: Issue
  focused?: boolean
  selected?: boolean
  /** True when the issue is in the summon queue — client-local, not a status. */
  staged?: boolean
  /** True for the card left in place while its clone rides the cursor. */
  dragging?: boolean
  onOpen?: () => void
  onFocus?: () => void
  onToggleSelect?: (shift: boolean) => void
  onOpenMenu?: (kind: 'status' | 'priority' | 'assignee' | 'labels', anchor: DOMRect) => void
}) {
  const labelRules = useLabelRules()
  const { attributes, listeners, setNodeRef } = useDraggable({
    id: issue.id,
    data: { status: issue.status },
  })

  const open = issue.status === 'open'
  const ready = open && issue.ready
  // A defer-scheduled issue is not blocked — it is parked. It gets the deferred
  // token and the deferred colour, never the amber blocked treatment. (I2.)
  const reason = open && !issue.ready ? notReadyReason(issue) : null
  const blocked = reason === 'deps'
  const deferred = reason === 'deferred'

  return (
    <article
      ref={setNodeRef}
      {...attributes}
      {...listeners}
      data-status={issue.status}
      data-blocked={blocked ? 'true' : undefined}
      data-card={issue.id}
      // Which card the keyboard is on, readable from the DOM rather than only
      // from an inline box-shadow: the board's scroll-into-view is asserted
      // against it (loom-reg).
      data-focused={focused ? 'true' : undefined}
      aria-selected={selected}
      onClick={(e) => {
        if (e.shiftKey || e.metaKey) {
          e.preventDefault()
          onToggleSelect?.(e.shiftKey)
          return
        }
        onOpen?.()
      }}
      onMouseEnter={onFocus}
      className={clsx(
        // shrink-0 matters: the column is a flex container, and without it a
        // full column squeezes its cards until the titles clip.
        'group relative shrink-0 cursor-grab touch-none overflow-hidden rounded-md text-left transition-[background,opacity] duration-[--dur-fast] active:cursor-grabbing',
        selected ? 'bg-selected' : 'bg-raised hover:bg-hover',
        // Parked work recedes. Blocked work does not: the red wash must read.
        deferred && 'opacity-[0.72]',
        dragging && 'opacity-40',
      )}
      style={{ boxShadow: focused ? 'inset 0 0 0 1px var(--accent)' : 'inset 0 0 0 1px var(--border)' }}
    >
      {blocked && (
        <span
          aria-hidden="true"
          className="pointer-events-none absolute inset-0"
          style={{ background: 'color-mix(in oklab, var(--danger) 11%, transparent)' }}
        />
      )}
      <span
        aria-hidden="true"
        className="absolute inset-y-0 left-0 w-[2px]"
        style={{ background: `color-mix(in oklab, ${typeColor(issue.type)} 50%, transparent)` }}
      />

      <div className="relative flex flex-col gap-1.5 py-2 pr-2 pl-3">
        <div className="flex items-center gap-1.5">
          <button
            type="button"
            className="shrink-0"
            onClick={(e) => {
              e.stopPropagation()
              onOpenMenu?.('priority', e.currentTarget.getBoundingClientRect())
            }}
            title="Change priority"
          >
            <PriorityBadge priority={issue.priority} />
          </button>

          <span className="mono truncate text-[10.5px] text-faint">{issue.id}</span>

          <span className="ml-auto flex shrink-0 items-center gap-1.5">
            {/* The blocked-by count is the red chip below; the corner carries only what this unblocks. */}
            <DepBadges blocks={issue.depCounts.blocks} />
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation()
                onOpenMenu?.('assignee', e.currentTarget.getBoundingClientRect())
              }}
              title="Change assignee"
            >
              <Assignee name={issue.assignee} size={16} />
            </button>
          </span>
        </div>

        <p className="line-clamp-2 text-[12.5px] leading-[1.35] text-ink">{issue.title}</p>

        {blocked && (
          <span
            className="chip self-start"
            data-tone="danger"
            data-solid="true"
            title="Dependencies are unmet — stored status is still open"
          >
            blocked by {issue.depCounts.blockedBy || '?'}
          </span>
        )}

        {deferred && (
          <span className="self-start">
            <DeferredChip issue={issue} />
          </span>
        )}

        {(issue.labels.length > 0 || issue.childStats) && (
          <div className="flex flex-wrap items-center gap-1">
            {issue.childStats && (
              <ChildProgress total={issue.childStats.total} closed={issue.childStats.closed} started={issue.childStats.started} />
            )}
            {sortLabels(issue.labels, labelRules)
              .slice(0, 2)
              .map((label) => (
                <button
                  key={label}
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation()
                    onOpenMenu?.('labels', e.currentTarget.getBoundingClientRect())
                  }}
                >
                  <LabelChip label={label} />
                </button>
              ))}
            {issue.labels.length > 2 && (
              <span className="mono text-[10px] text-faint" title={issue.labels.join(', ')}>
                +{issue.labels.length - 2}
              </span>
            )}
          </div>
        )}

        <div className="flex items-center gap-1.5 text-faint">
          <span className="flex items-center gap-1.5" style={{ color: typeColor(issue.type) }}>
            <TypeGlyph type={issue.type} />
            <span className="text-[10.5px] font-medium">{issue.type}</span>
          </span>
          {staged && (
            <span
              className="chip ml-auto"
              data-tone="review"
              title="In the summon queue — client-local, the bead's status is untouched"
            >
              queued
            </span>
          )}
          {ready && (
            <span
              className={clsx('chip', !staged && 'ml-auto')}
              data-tone="accent"
              title="Ready — no unmet dependencies"
            >
              ready
            </span>
          )}
          <span
            className={clsx('mono text-[10px]', ready || staged ? '' : 'ml-auto')}
            title={`Updated ${absoluteTime(issue.updated)}`}
          >
            {relativeTime(issue.updated)}
          </span>
        </div>
      </div>
    </article>
  )
}
