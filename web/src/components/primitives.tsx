import clsx from 'clsx'
import type { Issue, IssueStatus, Priority } from '@/api/types'
import {
  initials,
  isActive,
  labelTone,
  notReadyReason,
  priorityLabel,
  stateVerdict,
  statusLabel,
} from '@/lib/beads'
import { dateOnly } from '@/lib/time'
import { useLabelRules } from './labels'

/* ---------------------------------------------------------------------------
 * Every component here declares meaning via data-* and lets index.css route it
 * to a colour token. That indirection is the reason there is not a single hex
 * literal below this line.
 * ------------------------------------------------------------------------- */

export function StatusDot({ status, title }: { status: IssueStatus; title?: string }) {
  return (
    <span
      className="dot"
      data-status={status}
      data-filled={isActive(status)}
      title={title ?? statusLabel(status)}
      aria-label={statusLabel(status)}
      role="img"
    />
  )
}

/**
 * P0…P4. P0/P1 get a tinted ground because they are meant to interrupt you;
 * P2 and below are bare text, so a backlog of P3s reads as calm rather than as
 * a wall of badges.
 */
export function PriorityBadge({ priority }: { priority: Priority | number }) {
  const urgent = priority <= 1
  return (
    <span
      className={clsx('mono inline-flex h-4 w-[22px] items-center justify-center rounded-xs text-[10.5px] font-medium')}
      data-priority={priority}
      style={{
        color: 'var(--c)',
        background: urgent ? 'color-mix(in oklab, var(--c) var(--tint), transparent)' : 'transparent',
      }}
      title={`Priority ${priority}`}
    >
      {priorityLabel(priority)}
    </span>
  )
}

const TONE_BY_LABEL = {
  operator: 'warn',
  review: 'review',
  phase: 'neutral',
  layer: 'neutral',
  state: 'neutral',
  plain: 'neutral',
} as const

const TONE_BY_VERDICT = { pass: 'accent', fail: 'danger', neutral: 'neutral' } as const

/**
 * Label chips carry bd's label semantics (ARCHITECTURE.md §Bead field
 * conventions): `review-follow-up` gets its own hue; `phase-*` / `layer-*` are
 * organisational and stay quiet. A label a mounted integration gives meaning
 * to (djinn's `needs-operator`) follows that integration's rule.
 */
export function LabelChip({ label }: { label: string }) {
  const rules = useLabelRules()
  const rule = rules.find((r) => r.name === label)
  const tone = labelTone(label, rules)
  const operator = tone === 'operator'
  // A state label carries a verdict; `evidence_status:fail` must never wear
  // the same colour as a pass just because both came from `bd set-state`.
  const chipTone = tone === 'state' ? TONE_BY_VERDICT[stateVerdict(label)] : TONE_BY_LABEL[tone]
  return (
    <span
      className="chip"
      data-tone={chipTone}
      data-outline={tone === 'phase' || tone === 'layer' ? 'true' : undefined}
      data-solid={operator || tone === 'state' ? 'true' : undefined}
      title={rule?.hint ?? label}
    >
      {operator && <WarnMark />}
      {label}
    </span>
  )
}

function WarnMark() {
  return (
    <svg viewBox="0 0 10 10" width="9" height="9" aria-hidden="true">
      <path
        d="M5 1 9.3 8.6H0.7z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.2"
        strokeLinejoin="round"
      />
      <path d="M5 4v2" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
    </svg>
  )
}

export function Assignee({ name, size = 18 }: { name: string; size?: number }) {
  if (!name) {
    return (
      <span
        className="rounded-full border border-dashed border-line"
        style={{ width: size, height: size, flex: 'none' }}
        title="Unassigned"
        aria-label="Unassigned"
      />
    )
  }
  return (
    <span
      className="mono inline-flex items-center justify-center rounded-full bg-raised text-[9.5px] font-medium text-muted"
      style={{ width: size, height: size, flex: 'none' }}
      title={name}
      aria-label={`Assigned to ${name}`}
    >
      {initials(name)}
    </span>
  )
}

/**
 * Readiness, which is the one thing a status column cannot tell you: a
 * dependency-blocked bead is still stored as `open`.
 *
 * Three outcomes, not two (review finding I2):
 *   · ready              — quiet accent tick
 *   · dependency-blocked — red (--danger), with the blocker count
 *   · defer-scheduled    — the *deferred* token, with the date. Nothing is
 *                          blocking it; someone parked it on purpose, and
 *                          calling that "blocked by ?" is simply false.
 */
export function ReadinessMark({ issue }: { issue: Issue }) {
  const { status, ready } = issue
  if (status !== 'open') return null

  if (!ready && notReadyReason(issue) === 'deferred') return <DeferredChip issue={issue} />

  const blockedBy = issue.depCounts.blockedBy

  if (ready) {
    return (
      <span
        className="inline-flex items-center text-accent"
        title="Ready — no unmet dependencies"
        aria-label="Ready"
      >
        <svg viewBox="0 0 10 10" width="10" height="10" aria-hidden="true">
          <path
            d="M1.5 5.2 3.9 7.6 8.6 2.8"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </span>
    )
  }

  return (
    // Outlined, not filled: a backlog can be mostly dep-blocked, and a column
    // of solid red blocks would drown out `needs-operator`, which genuinely
    // needs to shout.
    <span
      className="chip"
      data-tone="danger"
      data-outline="true"
      title={`Blocked by ${blockedBy} unmet ${blockedBy === 1 ? 'dependency' : 'dependencies'} — stored status is still open`}
    >
      blocked by {blockedBy || '?'}
    </span>
  )
}

/**
 * The defer token. Shared by rows, cards and the detail sheet so all three
 * describe a defer-scheduled issue the same way.
 */
export function DeferredChip({ issue }: { issue: Issue }) {
  const until = issue.deferUntil ? dateOnly(issue.deferUntil) : null
  return (
    <span
      className="chip"
      data-status="deferred"
      title={
        until
          ? `Deferred until ${until} — nothing is blocking it`
          : 'Deferred by its parent — nothing is blocking it'
      }
    >
      {until ? `deferred until ${until}` : 'deferred'}
    </span>
  )
}

/**
 * Untouched for longer than the staleness window.
 *
 * A clock rather than a warning triangle: nothing is *wrong* with a stale
 * issue, it has simply been sitting. The amber is the same one `needs-operator`
 * uses because both mean "a human should look at this", but the glyph keeps
 * them apart at a glance.
 */
export function StaleBadge({ days }: { days?: number }) {
  return (
    <span
      className="chip"
      data-tone="warn"
      data-outline="true"
      title={days ? `Untouched for ${days} days` : 'Untouched for a while'}
    >
      <svg viewBox="0 0 10 10" width="9" height="9" aria-hidden="true">
        <circle cx="5" cy="5" r="3.8" fill="none" stroke="currentColor" strokeWidth="1.1" />
        <path d="M5 2.8V5l1.6 1.1" stroke="currentColor" strokeWidth="1.1" strokeLinecap="round" fill="none" />
      </svg>
      {days ? `${days}d` : 'stale'}
    </span>
  )
}

/** Dependency fan-in / fan-out, only rendered when non-zero. */
export function DepBadges({ blockedBy = 0, blocks }: { blockedBy?: number; blocks: number }) {
  if (!blockedBy && !blocks) return null
  return (
    <span className="mono flex items-center gap-1.5 text-[10.5px] text-faint">
      {blockedBy > 0 && (
        <span
          className="inline-flex items-center gap-0.5"
          title={`${blockedBy} blocking ${blockedBy === 1 ? 'dependency' : 'dependencies'}`}
        >
          <ArrowIn />
          {blockedBy}
        </span>
      )}
      {blocks > 0 && (
        <span
          className="inline-flex items-center gap-0.5 text-muted"
          title={`Blocks ${blocks} ${blocks === 1 ? 'issue' : 'issues'}`}
        >
          <ArrowOut />
          {blocks}
        </span>
      )}
    </span>
  )
}

const arrowProps = {
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.2,
  strokeLinecap: 'round',
  strokeLinejoin: 'round',
} as const

const ArrowIn = () => (
  <svg viewBox="0 0 10 10" width="9" height="9" aria-hidden="true">
    <path d="M8.5 5H2M4.5 2.5 2 5l2.5 2.5" {...arrowProps} />
  </svg>
)

const ArrowOut = () => (
  <svg viewBox="0 0 10 10" width="9" height="9" aria-hidden="true">
    <path d="M1.5 5H8M5.5 2.5 8 5 5.5 7.5" {...arrowProps} />
  </svg>
)

/**
 * Closed-vs-total progress for an epic, with in-flight work as its own segment.
 *
 * `started` is never summed into `closed` — the mono ratio stays closed/total,
 * because "how much is done" and "how much is moving" are different questions
 * and folding them is how a rollup starts lying.
 */
export function ChildProgress({
  total,
  closed,
  started,
}: {
  total: number
  closed: number
  started: number
}) {
  const closedPct = total > 0 ? Math.round((closed / total) * 100) : 0
  const startedPct = total > 0 ? Math.round((started / total) * 100) : 0
  return (
    <span
      className="flex items-center gap-1.5"
      title={`${closed} of ${total} children closed, ${started} started`}
    >
      <span className="flex h-[3px] w-8 overflow-hidden rounded-full bg-active">
        <span
          className="block h-full bg-accent-muted transition-[width] duration-[--dur-base]"
          style={{ width: `${closedPct}%` }}
          data-segment="closed"
        />
        <span
          className="block h-full transition-[width] duration-[--dur-base]"
          style={{ width: `${startedPct}%`, background: 'var(--status-in-progress)' }}
          data-segment="started"
        />
      </span>
      <span className="mono text-[10.5px] text-faint">
        {closed}/{total}
      </span>
    </span>
  )
}

export function StatusChip({ status }: { status: IssueStatus }) {
  return (
    <span className="chip" data-status={status} data-solid="true">
      <span className="dot" data-status={status} data-filled={isActive(status)} />
      {statusLabel(status)}
    </span>
  )
}

/** Monospace bead id — the thing you paste into `bd show`. */
export function BeadId({ id, className }: { id: string; className?: string }) {
  return <span className={clsx('mono text-[11px] tracking-tight', className)}>{id}</span>
}
