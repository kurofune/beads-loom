import { describe, expect, it } from 'vitest'
import { epicNudgeSignature, isEpicNudgeDismissed } from './epic-nudge'

describe('epic nudge dismissal signature', () => {
  it('ignores query order but changes when the eligible ids change', () => {
    const signature = epicNudgeSignature([{ id: 'epic-b' }, { id: 'epic-a' }])

    expect(signature).toBe('epic-a,epic-b')
    expect(isEpicNudgeDismissed('epic-a,epic-b', signature)).toBe(true)
    expect(isEpicNudgeDismissed('epic-a,epic-c', signature)).toBe(false)
  })
})
