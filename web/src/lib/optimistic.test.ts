import { describe, expect, it } from 'vitest'
import type { Issue, IssueDetail } from '@/api/types'
import { inClosedWindow } from './closed-window'
import {
  applyDetailPatch,
  applyPatch,
  boardDropIntent,
  dragSource,
  dropIntent,
  patchList,
} from './optimistic'

const base = (over: Partial<Issue> = {}): Issue =>
  ({
    id: 'loom-a',
    title: 'title',
    type: 'task',
    status: 'open',
    priority: 2,
    assignee: 'nate',
    labels: ['layer-ui'],
    estimate: 30,
    created: '2026-08-01T00:00:00Z',
    updated: '2026-08-01T00:00:00Z',
    closed: null,
    due: null,
    deferUntil: null,
    ready: true,
    pinned: false,
    depCounts: { blockedBy: 0, blocks: 0 },
    parent: null,
    childStats: null,
    ...over,
  }) as Issue

const detail = (over: Partial<IssueDetail> = {}): IssueDetail =>
  ({
    ...base(),
    health: null,
    description: 'old description',
    acceptanceCriteria: 'old criterion',
    design: 'old design',
    notes: 'old notes',
    closeReason: '',
    owner: '',
    deps: { blocks: [], blockedBy: [], children: [], discoveredFrom: [], parent: null },
    comments: [],
    events: [],
    ...over,
  }) as IssueDetail

describe('applyDetailPatch', () => {
  it('copies a changed description without mutating the cached detail', () => {
    const issue = detail()

    const next = applyDetailPatch(issue, { description: 'new **raw** description' })

    expect(next.description).toBe('new **raw** description')
    expect(issue.description).toBe('old description')
  })

  it('maps write-side acceptance to the cached acceptance criteria', () => {
    const next = applyDetailPatch(detail(), { acceptance: 'first\nsecond' })

    expect(next.acceptanceCriteria).toBe('first\nsecond')
  })

  it('copies changed design and notes into the cached detail', () => {
    const next = applyDetailPatch(detail(), { design: 'new design', notes: 'new notes' })

    expect(next.design).toBe('new design')
    expect(next.notes).toBe('new notes')
  })

  it('clears with empty strings and leaves omitted prose fields unchanged', () => {
    const next = applyDetailPatch(detail(), { description: '', acceptance: '' })

    expect(next.description).toBe('')
    expect(next.acceptanceCriteria).toBe('')
    expect(next.design).toBe('old design')
    expect(next.notes).toBe('old notes')
  })

  it('keeps shared fields on the same optimistic path as list issues', () => {
    const next = applyDetailPatch(detail({ priority: 2, labels: ['old'] }), {
      priority: 0,
      labelsAdd: ['new'],
    })

    expect(next.priority).toBe(0)
    expect(next.labels).toEqual(['old', 'new'])
  })
})

describe('applyPatch', () => {
  it('moves only the fields the patch names', () => {
    const next = applyPatch(base(), { status: 'in_progress' })
    expect(next.status).toBe('in_progress')
    expect(next.priority).toBe(2)
    expect(next.assignee).toBe('nate')
    expect(next.labels).toEqual(['layer-ui'])
  })

  it('does not guess readiness, which only the ready_issues view knows', () => {
    // Moving to in_progress cannot make an issue ready or unready; guessing
    // here would flash the wrong badge before the refetch corrects it.
    expect(applyPatch(base({ ready: true }), { status: 'in_progress' }).ready).toBe(true)
    expect(applyPatch(base({ ready: false }), { status: 'blocked' }).ready).toBe(false)
  })

  it('does guess readiness for a close, where the answer is never in doubt', () => {
    expect(applyPatch(base({ ready: true }), { status: 'closed' }).ready).toBe(false)
  })

  it('stamps the close time, without which the card falls out of the default Closed window', () => {
    // The Closed column is windowed (Today / 7d / All) and a closed row with no
    // timestamp is in none of the bounded ones. Guessing `status` but not
    // `closed` therefore made an optimistically-closed card vanish from the
    // whole board until the refetch landed — the "where did that bead go?"
    // scar, arriving through the optimistic path.
    const before = Date.now()
    const next = applyPatch(base(), { status: 'closed' })

    expect(next.closed).not.toBeNull()
    expect(Date.parse(next.closed as string)).toBeGreaterThanOrEqual(before)
    expect(inClosedWindow(next.closed, 'today')).toBe(true)
    expect(inClosedWindow(next.closed, '7d')).toBe(true)
  })

  it('clears the close time on the way out of closed, so no ghost sits in Today', () => {
    const closed = base({ status: 'closed', ready: false, closed: '2026-08-01T00:00:00Z' })

    expect(applyPatch(closed, { status: 'open' }).closed).toBeNull()
    expect(applyPatch(closed, { status: 'in_progress' }).closed).toBeNull()
  })

  it('leaves the close time alone when the patch says nothing about status', () => {
    const closed = base({ status: 'closed', ready: false, closed: '2026-08-01T00:00:00Z' })

    expect(applyPatch(closed, { priority: 1 }).closed).toBe('2026-08-01T00:00:00Z')
  })

  it('accepts priority 0, which a falsy check would drop', () => {
    expect(applyPatch(base({ priority: 3 }), { priority: 0 }).priority).toBe(0)
  })

  it('accepts an empty assignee, which is how you unassign', () => {
    expect(applyPatch(base(), { assignee: '' }).assignee).toBe('')
  })

  it('adds and removes labels without duplicating', () => {
    const next = applyPatch(base({ labels: ['a', 'b'] }), {
      labelsAdd: ['b', 'c'],
      labelsRemove: ['a'],
    })
    expect(next.labels).toEqual(['b', 'c'])
  })

  it('leaves labels untouched when the patch does not mention them', () => {
    const issue = base({ labels: ['a'] })
    expect(applyPatch(issue, { priority: 1 }).labels).toBe(issue.labels)
  })

  it('bumps updated so the relative timestamp does not look stale', () => {
    expect(applyPatch(base(), { priority: 1 }).updated).not.toBe(base().updated)
  })

  it('never mutates the input', () => {
    const issue = base()
    applyPatch(issue, { status: 'closed', labelsAdd: ['x'] })
    expect(issue.status).toBe('open')
    expect(issue.labels).toEqual(['layer-ui'])
  })
})

describe('patchList', () => {
  const list = [base({ id: 'a' }), base({ id: 'b' }), base({ id: 'c' })]

  it('touches only the named issues', () => {
    const next = patchList(list, ['b'], { priority: 0 })
    expect(next.map((i) => i.priority)).toEqual([2, 0, 2])
  })

  it('returns the list unchanged when nothing is named', () => {
    expect(patchList(list, [], { priority: 0 })).toBe(list)
  })

  it('ignores ids that are not in this list', () => {
    expect(patchList(list, ['zzz'], { priority: 0 }).map((i) => i.priority)).toEqual([2, 2, 2])
  })
})

describe('dropIntent', () => {
  it('maps a plain column move to a status patch', () => {
    expect(dropIntent('open', 'in_progress')).toEqual({
      kind: 'patch',
      patch: { status: 'in_progress' },
    })
  })

  it('routes a drop into closed to the close verb, which needs a reason', () => {
    // bd close stamps closed_at and records close_reason; a status patch would
    // do neither, so this must never degrade to { kind: "patch" }.
    expect(dropIntent('in_progress', 'closed')).toEqual({ kind: 'close' })
  })

  it('routes a drag from closed to open as a bare reopen', () => {
    expect(dropIntent('closed', 'open')).toEqual({ kind: 'reopen', then: null })
  })

  describe('closed → a column other than open (review finding I1)', () => {
    // `bd reopen` takes no target status and always produces `open`. Collapsing
    // this to a bare reopen dropped the user's target: the card animated onto
    // In progress and then appeared in Open, reading as a lost write.
    it('returns a compound intent carrying the target status', () => {
      expect(dropIntent('closed', 'in_progress')).toEqual({
        kind: 'reopen',
        then: { status: 'in_progress' },
      })
    })

    it('does the same for every non-open target, not just in_progress', () => {
      for (const target of ['in_progress', 'blocked', 'deferred', 'pinned', 'hooked']) {
        expect(dropIntent('closed', target)).toEqual({
          kind: 'reopen',
          then: { status: target },
        })
      }
    })

    it('never silently discards the target', () => {
      const intent = dropIntent('closed', 'blocked')
      expect(intent.kind).toBe('reopen')
      expect(intent.kind === 'reopen' && intent.then?.status).toBe('blocked')
    })
  })

  it('does nothing when the card lands where it started', () => {
    expect(dropIntent('open', 'open')).toEqual({ kind: 'none' })
    expect(dropIntent('closed', 'closed')).toEqual({ kind: 'none' })
  })
})

describe('boardDropIntent', () => {
  it('delegates a column drop to the existing verb decision', () => {
    expect(boardDropIntent('all::in_progress', 'open')).toEqual({
      kind: 'patch',
      patch: { status: 'in_progress' },
    })
    expect(boardDropIntent('all::closed', 'open')).toEqual({ kind: 'close' })
    expect(boardDropIntent('all::open', 'closed')).toEqual({ kind: 'reopen', then: null })
    expect(boardDropIntent('all::open', 'open')).toEqual({ kind: 'none' })
  })

  it('does nothing for a drop that lands nowhere actionable', () => {
    expect(boardDropIntent('', 'open')).toEqual({ kind: 'none' })
    expect(boardDropIntent('not-a-column', 'open')).toEqual({ kind: 'none' })
    expect(boardDropIntent('all::open', '')).toEqual({ kind: 'none' })
  })
})

describe('dragSource', () => {
  it('reads a board card as a card drag, taking the bead from the active id', () => {
    expect(dragSource('loom-a', false)).toEqual({ kind: 'card', beadId: 'loom-a' })
  })

  it('refuses a card drag while the project is read-only', () => {
    // Animating a move that cannot be written would be a lie about the board.
    expect(dragSource('loom-a', true)).toBeNull()
  })
})
