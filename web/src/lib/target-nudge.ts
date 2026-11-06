import type { Initiative } from '@/api/types'

/**
 * The bd label an epic carries to say it is deliberately not time-boxed.
 * Mirrors queries.NoTargetLabel — the server decides whether the nudge fires;
 * this constant exists so the panel can name the exact label it will add.
 */
export const NO_TARGET_LABEL = 'no-target'

const STORAGE_KEY = 'loom.targetNudge.'

/**
 * Why this epic is flagged, in the manager's own numbers.
 *
 * Composed on the client rather than server-side (as the health explanation is)
 * because there is one rule and one sentence here, and every number in it —
 * closed and total descendants — is already on the wire and already rendered in
 * the Progress cell of the same row. There is nothing for the two to drift on.
 */
export function targetNudgeReason(initiative: Initiative): string {
  const remaining = initiative.subtree.total - initiative.subtree.closed
  const verb = remaining === 1 ? 'descendant is' : 'descendants are'
  return (
    `${remaining} of ${initiative.subtree.total} ${verb} still open and this epic has ` +
    'no due date, so health cannot say whether it will land. Set a target, or mark it untimed.'
  )
}

/** The dismissed epic ids stored for a project. Never throws. */
export function loadTargetDismissals(project: string): string[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY + project)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter((id): id is string => typeof id === 'string')
  } catch {
    return []
  }
}

export function saveTargetDismissals(project: string, ids: readonly string[]): void {
  try {
    localStorage.setItem(STORAGE_KEY + project, JSON.stringify([...ids].sort()))
  } catch {
    // Private mode can reject writes; an advisory nudge must never take the
    // Initiatives table down over its own dismissal bookkeeping.
  }
}

/**
 * The dismissals worth keeping: those whose epic still needs a target.
 *
 * Dismissal is an answer to a specific question ("this epic has no date"), so it
 * expires with the question. Pruning is what makes a *cleared* due date nag
 * again instead of staying silent forever on a stale dismissal.
 *
 * Returned sorted so a caller can compare it to the stored value and skip a
 * redundant write.
 */
export function storedTargetDismissals(
  dismissed: readonly string[],
  needingTarget: readonly string[],
): string[] {
  const needing = new Set(needingTarget)
  return dismissed.filter((id) => needing.has(id)).sort()
}

export function isTargetNudgeDismissed(dismissed: readonly string[], epic: string): boolean {
  return dismissed.includes(epic)
}
