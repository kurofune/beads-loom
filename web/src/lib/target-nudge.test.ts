import { beforeEach, describe, expect, it } from 'vitest'
import type { Initiative } from '@/api/types'
import {
  isTargetNudgeDismissed,
  loadTargetDismissals,
  saveTargetDismissals,
  storedTargetDismissals,
  targetNudgeReason,
} from './target-nudge'

function initiative(overrides: Partial<Initiative> = {}): Initiative {
  return {
    id: 'epic-1',
    title: 'Epic one',
    status: 'open',
    priority: 2,
    assignee: '',
    due: null,
    updated: '2026-08-10T00:00:00Z',
    lastActivity: '2026-08-10T00:00:00Z',
    subtree: { total: 9, closed: 2, started: 1, ready: 3, depBlocked: 0, deferScheduled: 0 },
    closeEligible: false,
    health: { state: 'no-target', explanation: 'No due date — no target to assess' },
    needsTarget: true,
    noReceipt: { total: 0, noAcceptance: 0, noCloseReason: 0 },
    ...overrides,
  }
}

describe('targetNudgeReason', () => {
  it('states why the epic is flagged using its own subtree numbers', () => {
    const reason = targetNudgeReason(initiative())

    expect(reason).toContain('7 of 9')
    expect(reason).toContain('no due date')
  })

  it('says one descendant in the singular', () => {
    const reason = targetNudgeReason(
      initiative({
        subtree: { total: 1, closed: 0, started: 0, ready: 1, depBlocked: 0, deferScheduled: 0 },
      }),
    )

    expect(reason).toContain('1 of 1 descendant is')
    expect(reason).not.toContain('descendants are')
  })
})

describe('target nudge dismissals', () => {
  beforeEach(() => localStorage.clear())

  it('persists a dismissal per epic, scoped to its project', () => {
    saveTargetDismissals('loom', ['epic-1'])

    expect(isTargetNudgeDismissed(loadTargetDismissals('loom'), 'epic-1')).toBe(true)
    expect(isTargetNudgeDismissed(loadTargetDismissals('loom'), 'epic-2')).toBe(false)
    expect(isTargetNudgeDismissed(loadTargetDismissals('djinn'), 'epic-1')).toBe(false)
  })

  it('survives a reload — a dismissal is not session state', () => {
    saveTargetDismissals('loom', ['epic-1'])

    // A fresh read is exactly what a page reload does.
    expect(loadTargetDismissals('loom')).toEqual(['epic-1'])
  })

  it('drops a dismissal once the epic no longer needs a target, so a cleared date re-nags', () => {
    // dated-again lost its nudge because someone set a due date; still-blank
    // did not. Only the first dismissal is stale.
    const kept = storedTargetDismissals(['epic-1', 'dated-again'], ['epic-1', 'still-blank'])

    expect(kept).toEqual(['epic-1'])
  })

  it('keeps the stored set stable when nothing changed', () => {
    expect(storedTargetDismissals(['epic-2', 'epic-1'], ['epic-1', 'epic-2'])).toEqual([
      'epic-1',
      'epic-2',
    ])
  })

  it('reads a corrupt or foreign stored value as no dismissals', () => {
    localStorage.setItem('loom.targetNudge.loom', '{"not":"an array"}')
    expect(loadTargetDismissals('loom')).toEqual([])

    localStorage.setItem('loom.targetNudge.loom', 'not json at all')
    expect(loadTargetDismissals('loom')).toEqual([])

    localStorage.setItem('loom.targetNudge.loom', '["epic-1", 7, null]')
    expect(loadTargetDismissals('loom')).toEqual(['epic-1'])
  })
})
