import { describe, expect, it } from 'vitest'
import type { Issue } from '@/api/types'
import {
  describeEvent,
  formatEstimate,
  initials,
  labelTone,
  type LabelRule,
  notReadyReason,
  readiness,
  sortLabels,
  splitCriteria,
  stateVerdict,
  statusLabel,
} from './beads'

describe('splitCriteria', () => {
  it('splits one criterion per non-blank line', () => {
    expect(splitCriteria('first\nsecond\nthird')).toEqual(['first', 'second', 'third'])
  })

  it('drops blank and whitespace-only lines', () => {
    expect(splitCriteria('first\n\n   \nsecond\n\n')).toEqual(['first', 'second'])
  })

  it('strips bullet, numbering and task-list prefixes authors add by habit', () => {
    expect(
      splitCriteria(['- bullet', '* star', '• dot', '1. numbered', '2) parened'].join('\n')),
    ).toEqual(['bullet', 'star', 'dot', 'numbered', 'parened'])
    expect(splitCriteria('- [ ] unchecked\n- [x] checked')).toEqual(['unchecked', 'checked'])
  })

  it('keeps prose that merely starts with a digit or a dash mid-word', () => {
    expect(splitCriteria('3 retries max\nnon-blocking writes')).toEqual([
      '3 retries max',
      'non-blocking writes',
    ])
  })

  it('is empty for empty, null and undefined', () => {
    expect(splitCriteria('')).toEqual([])
    expect(splitCriteria(null)).toEqual([])
    expect(splitCriteria(undefined)).toEqual([])
    expect(splitCriteria('\n\n  \n')).toEqual([])
  })
})

describe('readiness', () => {
  const issue = (over: Partial<Issue>): Issue =>
    ({
      id: 'x',
      title: 't',
      type: 'task',
      status: 'open',
      priority: 2,
      assignee: '',
      labels: [],
      estimate: 0,
      created: '',
      updated: '',
      closed: null,
      due: null,
      deferUntil: null,
      ready: false,
      pinned: false,
      depCounts: { blockedBy: 0, blocks: 0 },
      parent: null,
      childStats: null,
      ...over,
    }) as Issue

  it('trusts the ready flag, never the status string', () => {
    // The load-bearing bd semantic: a dependency-blocked bead is still `open`.
    expect(readiness(issue({ status: 'open', ready: false }))).toBe('dep-blocked')
    expect(readiness(issue({ status: 'open', ready: true }))).toBe('ready')
  })

  it('says nothing about issues that are not open', () => {
    expect(readiness(issue({ status: 'in_progress', ready: false }))).toBe('n/a')
    expect(readiness(issue({ status: 'blocked', ready: false }))).toBe('n/a')
    expect(readiness(issue({ status: 'closed', ready: false }))).toBe('n/a')
  })
})

const OPERATOR: LabelRule[] = [{ name: 'needs-operator', tone: 'operator', hint: 'cannot finish unattended' }]

describe('labelTone', () => {
  it('recognises bd label conventions', () => {
    expect(labelTone('review-follow-up')).toBe('review')
    expect(labelTone('phase-1')).toBe('phase')
    expect(labelTone('layer-ui')).toBe('layer')
    expect(labelTone('evidence_status:fail')).toBe('state')
    expect(labelTone('spike')).toBe('plain')
  })

  it('gives needs-operator meaning only through an integration rule', () => {
    expect(labelTone('needs-operator')).toBe('plain')
    expect(labelTone('needs-operator', OPERATOR)).toBe('operator')
  })

  it('sorts the labels a manager must not miss to the front', () => {
    expect(sortLabels(['layer-ui', 'phase-1', 'needs-operator', 'review-follow-up'], OPERATOR)).toEqual([
      'needs-operator',
      'review-follow-up',
      'phase-1',
      'layer-ui',
    ])
  })
})

describe('initials', () => {
  it('numbers agent workers', () => {
    expect(initials('djinn-worker-1')).toBe('W1')
    expect(initials('worker-12')).toBe('W12')
  })

  it('abbreviates humans', () => {
    expect(initials('Nate Gross')).toBe('NG')
    expect(initials('nate')).toBe('N')
    expect(initials('')).toBe('')
  })
})

describe('formatEstimate', () => {
  it('scales bd minutes into manager units', () => {
    expect(formatEstimate(0)).toBe('')
    expect(formatEstimate(45)).toBe('45m')
    expect(formatEstimate(120)).toBe('2h')
    expect(formatEstimate(150)).toBe('2.5h')
    expect(formatEstimate(480)).toBe('1d')
    expect(formatEstimate(720)).toBe('1.5d')
  })
})

describe('statusLabel', () => {
  it('humanises known statuses and survives project-custom ones', () => {
    expect(statusLabel('in_progress')).toBe('In progress')
    expect(statusLabel('hooked')).toBe('Hooked')
    expect(statusLabel('awaiting_review')).toBe('Awaiting review')
  })
})


describe('describeEvent', () => {
  const event = (over: Partial<{ oldValue: string; newValue: string; comment: string }>) => ({
    oldValue: '',
    newValue: '',
    comment: '',
    ...over,
  })

  it('prefers the human-written comment bd puts on label events', () => {
    expect(describeEvent(event({ comment: 'Added label: layer-ui' }))).toBe('Added label: layer-ui')
  })

  it('decodes the JSON patch bd stores in newValue', () => {
    // Real shape from the seeded db: oldValue is a whole-issue snapshot and
    // newValue is a patch. Rendering `old → new` would print 400 chars of JSON.
    expect(
      describeEvent(
        event({
          oldValue: '{"id":"loom-323","title":"[Board] Status dropdown loses selection"}',
          newValue: '{"assignee":"djinn-worker-2","status":"in_progress"}',
        }),
      ),
    ).toBe('assignee → djinn-worker-2 · status → in_progress')
  })

  it('truncates a long patch value rather than blowing out the row', () => {
    const out = describeEvent(event({ newValue: JSON.stringify({ notes: 'x'.repeat(80) }) }))
    expect(out.length).toBeLessThan(50)
    expect(out).toContain('…')
  })

  it('still renders plain scalar transitions', () => {
    expect(describeEvent(event({ oldValue: 'open', newValue: 'in_progress' }))).toBe(
      'open → in_progress',
    )
    expect(describeEvent(event({ newValue: 'nate' }))).toBe('∅ → nate')
  })

  it('renders nothing when there is nothing legible to say', () => {
    expect(describeEvent(event({}))).toBe('')
    expect(describeEvent(event({ oldValue: '{"id":"loom-1"}' }))).toBe('')
  })
})

describe('stateVerdict', () => {
  it('reads the verdict half of a bd set-state label', () => {
    expect(stateVerdict('evidence_status:fail')).toBe('fail')
    expect(stateVerdict('evidence_status:pass')).toBe('pass')
    expect(stateVerdict('review:OK')).toBe('pass')
    expect(stateVerdict('rollout:canary')).toBe('neutral')
  })
})

describe('notReadyReason / readiness — deferred is not blocked (review finding I2)', () => {
  const SOON = '2099-01-01T00:00:00Z'
  const PAST = '2020-01-01T00:00:00Z'

  const open = (over: Partial<Issue>): Issue =>
    ({
      id: 'x',
      title: 't',
      type: 'task',
      status: 'open',
      priority: 2,
      assignee: '',
      labels: [],
      estimate: 0,
      created: '',
      updated: '',
      closed: null,
      due: null,
      deferUntil: null,
      ready: false,
      pinned: false,
      depCounts: { blockedBy: 0, blocks: 0 },
      parent: null,
      childStats: null,
      ...over,
    }) as Issue

  it('prefers the backend field when it is present', () => {
    expect(notReadyReason(open({ notReady: 'deferred' }))).toBe('deferred')
    expect(notReadyReason(open({ notReady: 'deps' }))).toBe('deps')
  })

  it('trusts the backend over a stale local defer date', () => {
    // A parent-deferred issue has no defer date of its own; the field knows.
    expect(notReadyReason(open({ notReady: 'deferred', deferUntil: null }))).toBe('deferred')
    expect(notReadyReason(open({ notReady: 'deps', deferUntil: SOON }))).toBe('deps')
  })

  it('ignores a null field on a not-ready issue — that is a contradiction', () => {
    // Observed live: the backend emits notReady:null for a defer-scheduled
    // issue while its half of this fix is in flight. null means "ready", so on
    // a ready:false row it is no answer at all, and trusting it would put the
    // red "blocked by ?" chip straight back.
    expect(notReadyReason(open({ notReady: null, deferUntil: SOON }))).toBe('deferred')
    expect(notReadyReason(open({ notReady: null, deferUntil: null }))).toBe('deps')
  })

  it('falls back to the issue own defer date when the field is absent', () => {
    // The field is landing in parallel; until it does, this is the best signal
    // available and it is right for the common case.
    expect(notReadyReason(open({ deferUntil: SOON }))).toBe('deferred')
    expect(notReadyReason(open({ deferUntil: PAST }))).toBe('deps')
    expect(notReadyReason(open({ deferUntil: null }))).toBe('deps')
  })

  it('reports nothing for a ready issue', () => {
    expect(notReadyReason(open({ ready: true, deferUntil: SOON }))).toBeNull()
  })

  it('classifies readiness three ways, not two', () => {
    expect(readiness(open({ ready: true }))).toBe('ready')
    expect(readiness(open({ notReady: 'deps', depCounts: { blockedBy: 2, blocks: 0 } }))).toBe('dep-blocked')
    expect(readiness(open({ notReady: 'deferred' }))).toBe('deferred')
  })

  it('never calls a defer-scheduled issue blocked, which is the actual bug', () => {
    // A manager defers three issues to next sprint; nothing blocks them, and
    // depCounts.blockedBy is 0 — which is what produced the "blocked by ?" chip.
    const deferred = open({ notReady: 'deferred', depCounts: { blockedBy: 0, blocks: 0 } })
    expect(readiness(deferred)).not.toBe('dep-blocked')
    expect(readiness(deferred)).toBe('deferred')
  })

  it('says nothing about issues that are not open', () => {
    expect(readiness(open({ status: 'closed', notReady: 'deferred' }))).toBe('n/a')
  })
})
