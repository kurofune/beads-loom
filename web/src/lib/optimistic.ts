import type { Issue, IssueDetail, IssuePatch } from '@/api/types'

/**
 * Optimistic write support.
 *
 * The rule this module encodes: **guess the fields the user set, never the
 * fields bd derives.** A status change can flip `ready`, change `depCounts` on
 * other issues, and add event rows — none of which the client can compute. So
 * `applyPatch` moves exactly what was asked for and leaves everything else
 * alone; the server's re-read response is what makes it true.
 *
 * Keeping this pure (rather than mutating a query cache in place) is what makes
 * rollback trivial and the behaviour testable.
 */
export function applyPatch(issue: Issue, patch: IssuePatch): Issue {
  const next: Issue = { ...issue }

  if (patch.status !== undefined) next.status = patch.status
  if (patch.priority !== undefined) next.priority = patch.priority
  if (patch.assignee !== undefined) next.assignee = patch.assignee
  if (patch.title !== undefined) next.title = patch.title
  if (patch.type !== undefined) next.type = patch.type
  if (patch.estimate !== undefined) next.estimate = patch.estimate

  if (patch.labelsAdd?.length || patch.labelsRemove?.length) {
    const removed = new Set(patch.labelsRemove ?? [])
    const labels = issue.labels.filter((l) => !removed.has(l))
    for (const label of patch.labelsAdd ?? []) {
      if (!labels.includes(label)) labels.push(label)
    }
    next.labels = labels
  }

  // Crossing the closed boundary is where the "never guess derived fields" rule
  // earns its two exceptions — both because leaving the field stale is visibly
  // broken, and both because the guess cannot be wrong.
  //
  // `ready`: a closed card is never ready, so showing the badge while the card
  // slides into the Closed column just looks broken.
  //
  // `closed`: the Closed column is windowed (Today / 7d / All), and a closed row
  // with no close time is in *none* of the bounded windows — so patching status
  // alone made an optimistically-closed card disappear from the entire board
  // until the settled refetch landed. That is the "where did that bead go?"
  // scar, so the timestamp is stamped here rather than special-cased inside the
  // window predicate: `inClosedWindow` treats an unplaceable timestamp as
  // out-of-window on purpose (a real row bd never stamped is genuinely not
  // known to be recent), and weakening that to paper over a missing optimistic
  // field would let unstamped server rows drift into Today too. The value is
  // the client's clock rather than bd's, which differs by seconds and is
  // corrected by the refetch — seconds cannot move a card across a day
  // boundary that matters here. Leaving closed means bd clears closed_at, so
  // the reverse move clears it symmetrically instead of leaving a ghost that
  // reads as closed-today in the reopened card's data.
  if (patch.status !== undefined) {
    if (patch.status === 'closed') {
      next.ready = false
      next.closed = new Date().toISOString()
    } else {
      next.closed = null
    }
  }

  next.updated = new Date().toISOString()
  return next
}

/** Apply a write patch to the richer shape cached by the open detail sheet. */
export function applyDetailPatch(detail: IssueDetail, patch: IssuePatch): IssueDetail {
  const next = applyPatch(detail, patch) as IssueDetail
  if (patch.description !== undefined) next.description = patch.description
  if (patch.acceptance !== undefined) next.acceptanceCriteria = patch.acceptance
  if (patch.design !== undefined) next.design = patch.design
  if (patch.notes !== undefined) next.notes = patch.notes
  return next
}

/** Apply a patch to whichever issues in a list are named by `ids`. */
export function patchList(issues: Issue[], ids: string[], patch: IssuePatch): Issue[] {
  const targets = new Set(ids)
  if (targets.size === 0) return issues
  return issues.map((issue) => (targets.has(issue.id) ? applyPatch(issue, patch) : issue))
}

/**
 * Move an issue to a column, expressed as bd verbs.
 *
 * Dropping onto `closed` is NOT a status patch — bd has a distinct `close`
 * verb that records a reason and stamps closed_at, and dragging out of closed
 * is `reopen`. The board asks this function which verb a drop means so that
 * decision lives in one tested place instead of inside a drag handler.
 *
 * `reopen` carries a `then` patch because **`bd reopen` always lands the issue
 * in `open`** — it takes no target status. Dragging a closed card onto
 * "In progress" is therefore two verbs, not one, and collapsing it to a bare
 * reopen drops the user's target on the floor: the drop animates, the card
 * appears in Open, and it reads as a lost write. (Review finding I1.)
 */
export type DropIntent =
  | { kind: 'patch'; patch: IssuePatch }
  | { kind: 'close' }
  /** `then` is null only when the target genuinely is `open`. */
  | { kind: 'reopen'; then: IssuePatch | null }
  | { kind: 'none' }

export function dropIntent(from: string, to: string): DropIntent {
  if (from === to) return { kind: 'none' }
  if (to === 'closed') return { kind: 'close' }
  if (from === 'closed') {
    return { kind: 'reopen', then: to === 'open' ? null : { status: to } }
  }
  return { kind: 'patch', patch: { status: to } }
}

/**
 * A board drop.
 *
 * Every board drop target is a column, and column droppable ids arrive as
 * `lane::status` so one status can appear in every swimlane. Anything else
 * means the drop landed nowhere actionable.
 */
export function boardDropIntent(overId: string, from: string): DropIntent {
  const to = overId.split('::')[1] ?? ''
  if (!from || !to) return { kind: 'none' }
  return dropIntent(from, to)
}

/**
 * What a drag that just started actually is, and whether it is allowed.
 *
 * Both drag handlers need the same two answers — which bead, permitted or not
 * — so they read them from here rather than each re-deriving them. A drag is
 * refused while the project is read-only because it animates a status move
 * that cannot be written.
 */
export type DragSource = { kind: 'card'; beadId: string } | null

export function dragSource(activeId: string, readOnly: boolean): DragSource {
  return readOnly ? null : { kind: 'card', beadId: activeId }
}
