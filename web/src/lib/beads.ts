import type { Issue, IssueStatus, Priority } from '@/api/types'

/**
 * Acceptance criteria arrive as one blob of text. bd's convention (see
 * ARCHITECTURE.md §Bead field conventions) is **one criterion per non-blank
 * line**, which is why this is a split and not a markdown render: the detail
 * panel shows them as a checklist, and a markdown renderer would silently
 * merge adjacent lines into a single paragraph.
 *
 * Authors are inconsistent about prefixes, so bullets, numbering and GFM
 * task-list boxes are all stripped down to the bare criterion.
 */
export function splitCriteria(raw: string | null | undefined): string[] {
  if (!raw) return []
  return raw
    .split('\n')
    .map((line) =>
      line
        .trim()
        // "- ", "* ", "• ", "1. ", "1) "
        .replace(/^(?:[-*•]|\d+[.)])\s+/, '')
        // "[ ] " / "[x] " left over from a GFM task list
        .replace(/^\[[ xX]\]\s*/, '')
        .trim(),
    )
    .filter((line) => line.length > 0)
}

/** bd statuses, in the order the backlog stacks its groups. */
export const STATUS_ORDER: IssueStatus[] = [
  'in_progress',
  'blocked',
  'open',
  'deferred',
  'pinned',
  'hooked',
  'closed',
]

export const STATUS_LABEL: Record<string, string> = {
  open: 'Open',
  in_progress: 'In progress',
  blocked: 'Blocked',
  deferred: 'Deferred',
  closed: 'Closed',
  pinned: 'Pinned',
  hooked: 'Hooked',
}

export const statusLabel = (s: IssueStatus): string =>
  STATUS_LABEL[s] ?? s.replace(/_/g, ' ').replace(/^./, (c) => c.toUpperCase())

/** Statuses where work is under way — the dot renders solid. */
export const isActive = (s: IssueStatus): boolean => s === 'in_progress' || s === 'hooked'

export const priorityLabel = (p: Priority | number): string => `P${p}`

/**
 * The single most important derived fact in the backlog.
 *
 * A dependency-blocked bead keeps stored status `open` — bd's `blocked` status
 * is a *manual* flag. So "can this be picked up?" is answered by the `ready`
 * field (the `ready_issues` view) and never by the status string.
 *
 * `deferred` is a distinct outcome from `dep-blocked`: `ready_issues` also
 * excludes rows whose `defer_until` is in the future (or whose parent is
 * defer-scheduled), and `bd update --defer` sets that **without changing
 * status**. Open-with-future-defer is a normal, deliberate state — treating it
 * as blocked is wrong in both directions: it inflates the blocked count and it
 * shows a red "blocked by ?" chip on an issue nothing is blocking. (I2.)
 */
export type Readiness = 'ready' | 'dep-blocked' | 'deferred' | 'n/a'

export function readiness(issue: Issue, now: number = Date.now()): Readiness {
  if (issue.status !== 'open') return 'n/a'
  if (issue.ready) return 'ready'
  return notReadyReason(issue, now) === 'deferred' ? 'deferred' : 'dep-blocked'
}

/**
 * Why an issue is not ready.
 *
 * Prefers the backend's `notReady`, which knows the view's exact semantics and
 * — since `internal/queries/readiness.go` shipped — covers parent-deferral,
 * which the local fallback cannot see. That field is now the normal answer, not
 * an aspiration: across the three real projects on this machine, all 371 open
 * issues carry either `ready: true` or a non-null `notReady`.
 *
 * The null-guard is still load-bearing rather than historical. `notReadyExpr`
 * ends in `ELSE NULL`, so "not ready, and none of the enumerated reasons" stays
 * representable on the wire; and null is also the correct value for a *ready*
 * issue. Reading a null as "deferred" would be the I2 bug again, and reading it
 * as authoritative would print "blocked by ?" on an issue nothing blocks — so it
 * is read as *no answer* and the local fallback takes over.
 *
 * The fallback only sees the issue's own `deferUntil`. bd moves a directly
 * deferred issue to status `deferred` (`readiness.go` documents this, and
 * `loom-0zj.2` is one live), and `readiness()` short-circuits any non-open
 * status to `n/a`, so in practice the fallback answers only for open rows.
 */
export function notReadyReason(issue: Issue, now: number = Date.now()): 'deps' | 'deferred' | null {
  if (issue.ready) return null
  if (issue.notReady) return issue.notReady
  if (issue.deferUntil && Date.parse(issue.deferUntil) > now) return 'deferred'
  return 'deps'
}

/** Semantic label buckets from ARCHITECTURE.md §Bead field conventions. */
export type LabelTone = 'operator' | 'review' | 'phase' | 'layer' | 'state' | 'plain'

/**
 * A label an integration gives meaning to. Core knows no such label: without
 * a rule, `needs-operator` is a plain label like any other.
 */
export interface LabelRule {
  name: string
  tone: 'operator'
  /** The chip's tooltip. */
  hint: string
  /** Offer the label in the create and edit pickers. */
  seed?: boolean
}

export function labelTone(label: string, rules: readonly LabelRule[] = []): LabelTone {
  const rule = rules.find((r) => r.name === label)
  if (rule) return rule.tone
  if (label === 'review-follow-up') return 'review'
  if (label.startsWith('phase-')) return 'phase'
  if (label.startsWith('layer-')) return 'layer'
  // `<dim>:<val>` state labels written by `bd set-state`, e.g. evidence_status:fail
  if (label.includes(':')) return 'state'
  return 'plain'
}

/** Labels sort by how much the manager needs to see them. */
const TONE_WEIGHT: Record<LabelTone, number> = {
  operator: 0,
  review: 1,
  state: 2,
  phase: 3,
  layer: 4,
  plain: 5,
}

export const sortLabels = (labels: string[], rules: readonly LabelRule[] = []): string[] =>
  [...labels].sort(
    (a, b) => TONE_WEIGHT[labelTone(a, rules)] - TONE_WEIGHT[labelTone(b, rules)] || a.localeCompare(b),
  )

/** Two-letter monogram for an assignee; agents get their worker number. */
export function initials(name: string): string {
  if (!name) return ''
  const worker = /^(?:djinn-)?worker-?(\d+)$/i.exec(name)
  if (worker) return `W${worker[1]}`
  const parts = name.split(/[\s._-]+/).filter(Boolean)
  if (parts.length === 0) return ''
  // A single-token name gets one letter: 'nate' → 'N'. Two letters reads as
  // an abbreviation ('NA') and gets mistaken for N/A.
  if (parts.length === 1) return parts[0]![0]!.toUpperCase()
  return (parts[0]![0]! + parts[1]![0]!).toUpperCase()
}

/**
 * Turn one `events` row into a line a human can read.
 *
 * bd does not store a tidy old→new pair. Observed on real seeded data:
 *   · `label_added`   → the prose lives in `comment`, old/new are empty
 *   · `status_changed` / `updated` → `oldValue` is a full JSON snapshot of the
 *     issue and `newValue` is a JSON *patch* of just what changed
 *   · simple fields   → plain scalars in old/new
 * Printing `old → new` naively dumps a 400-character JSON blob into the
 * history feed, so decode the patch and describe the keys that actually moved.
 */
export function describeEvent(event: {
  oldValue: string
  newValue: string
  comment: string
}): string {
  if (event.comment.trim()) return event.comment.trim()

  const patch = parseObject(event.newValue)
  if (patch) {
    return Object.entries(patch)
      .map(([key, value]) => `${key} → ${formatValue(value)}`)
      .join(' · ')
  }

  const from = event.oldValue.trim()
  const to = event.newValue.trim()
  // A snapshot with no decodable patch beside it tells the reader nothing.
  if (parseObject(from) && !to) return ''
  if (!from && !to) return ''
  return `${from || '∅'} → ${to || '∅'}`
}

function parseObject(raw: string): Record<string, unknown> | null {
  const text = raw.trim()
  if (!text.startsWith('{')) return null
  try {
    const parsed: unknown = JSON.parse(text)
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)
      : null
  } catch {
    return null
  }
}

function formatValue(value: unknown): string {
  if (value === null || value === undefined || value === '') return '∅'
  const text = typeof value === 'string' ? value : JSON.stringify(value)
  return text.length > 32 ? `${text.slice(0, 31)}…` : text
}

/**
 * `<dim>:<val>` state labels written by `bd set-state`. The dimension is
 * arbitrary, but the *verdict* half is conventional enough to colour: an
 * `evidence_status:fail` must not render in the same green as a pass.
 */
export type Verdict = 'pass' | 'fail' | 'neutral'

const PASS = new Set(['pass', 'passed', 'ok', 'green', 'done', 'complete', 'yes', 'true'])
const FAIL = new Set(['fail', 'failed', 'error', 'red', 'blocked', 'no', 'false'])

export function stateVerdict(label: string): Verdict {
  const value = label.slice(label.indexOf(':') + 1).trim().toLowerCase()
  if (PASS.has(value)) return 'pass'
  if (FAIL.has(value)) return 'fail'
  return 'neutral'
}

/** bd stores estimates in minutes. Managers think in hours and days. */
export function formatEstimate(minutes: number | null | undefined): string {
  if (!minutes) return ''
  if (minutes < 60) return `${minutes}m`
  const hours = minutes / 60
  if (hours < 8) return `${Number.isInteger(hours) ? hours : hours.toFixed(1)}h`
  const days = hours / 8
  return `${Number.isInteger(days) ? days : days.toFixed(1)}d`
}
