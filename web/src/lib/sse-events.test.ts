import { describe, expect, it } from 'vitest'
import {
  actionsFor,
  actionsForConnect,
  coalesce,
  parseEvent,
  type CacheAction,
  type LoomEvent,
} from './sse-events'

const HEALTH = { ready: 5, inProgress: 3, blocked: 8, open: 13, total: 24 }

describe('parseEvent', () => {
  it('parses db-invalidated', () => {
    expect(parseEvent('db-invalidated', '{"project":"loom"}')).toEqual({
      type: 'db-invalidated',
      project: 'loom',
    })
  })

  it('parses a healthy project-health frame', () => {
    expect(
      parseEvent(
        'project-health',
        JSON.stringify({ project: 'loom', reachable: true, health: HEALTH }),
      ),
    ).toEqual({
      type: 'project-health',
      project: 'loom',
      reachable: true,
      reason: '',
      health: HEALTH,
      writable: true,
      writeReason: '',
      pending: false,
    })
  })

  it('parses a pending frame: up, with counts not known yet', () => {
    expect(
      parseEvent('project-health', JSON.stringify({ project: 'loom', reachable: true, health: null, pending: true })),
    ).toMatchObject({ reachable: true, health: null, pending: true })
  })

  it('parses a read-only frame with its reason', () => {
    expect(
      parseEvent(
        'project-health',
        JSON.stringify({ project: 'loom', reachable: true, health: HEALTH, writable: false, writeReason: 'split' }),
      ),
    ).toMatchObject({ writable: false, writeReason: 'split' })
  })

  it('parses an unreachable frame, where health is null and reason is set', () => {
    const event = parseEvent(
      'project-health',
      JSON.stringify({
        project: 'ghost',
        reachable: false,
        reason: 'connect: connection refused',
        health: null,
      }),
    )
    expect(event).toEqual({
      type: 'project-health',
      project: 'ghost',
      reachable: false,
      reason: 'connect: connection refused',
      health: null,
      writable: true,
      writeReason: '',
      pending: false,
    })
  })

  it('treats a malformed health block as absent rather than trusting it', () => {
    const event = parseEvent(
      'project-health',
      JSON.stringify({ project: 'loom', reachable: true, health: { ready: 'lots' } }),
    )
    expect(event).toMatchObject({ health: null })
  })

  it('reports an unfamiliar event name instead of dropping it', () => {
    // Whatever a later phase puts on this stream, an older client must treat it
    // as a sign of life rather than a broken frame. (run-* used to be the
    // example here; phase 4 made those known, which is the point.)
    expect(parseEvent('worktree-created', '{"project":"loom","path":"/tmp/x"}')).toEqual({
      type: 'unknown',
      name: 'worktree-created',
    })
  })

  it('survives unparseable payloads', () => {
    expect(parseEvent('db-invalidated', 'not json')).toEqual({
      type: 'unknown',
      name: 'db-invalidated',
    })
    expect(parseEvent('', 'not json')).toBeNull()
  })

  it('rejects frames with no project, which cannot be routed anywhere', () => {
    expect(parseEvent('db-invalidated', '{}')).toBeNull()
    expect(parseEvent('project-health', '{"reachable":true}')).toBeNull()
  })
})

describe('actionsFor', () => {
  it('maps db-invalidated to a project invalidation', () => {
    expect(actionsFor({ type: 'db-invalidated', project: 'loom' })).toEqual([
      { kind: 'invalidate-project', project: 'loom' },
    ])
  })

  it('maps project-health to a cache patch and NOT an invalidation', () => {
    // The event carries the counts. Refetching /api/projects here would undo
    // the point of the backend's poller.
    const event: LoomEvent = {
      type: 'project-health',
      project: 'loom',
      reachable: true,
      reason: '',
      health: HEALTH,
      writable: false,
      writeReason: 'split',
      pending: false,
    }
    expect(actionsFor(event)).toEqual([
      {
        kind: 'patch-health',
        project: 'loom',
        reachable: true,
        reason: '',
        health: HEALTH,
        writable: false,
        writeReason: 'split',
        pending: false,
      },
    ])
  })

  it('does not invalidate on recovery — the backend already follows with db-invalidated', () => {
    const recovery: LoomEvent = {
      type: 'project-health',
      project: 'ghost',
      reachable: true,
      reason: '',
      health: HEALTH,
      writable: true,
      writeReason: '',
      pending: false,
    }
    expect(actionsFor(recovery).some((a) => a.kind !== 'patch-health')).toBe(false)
  })

  it('ignores event types it does not know', () => {
    expect(actionsFor({ type: 'unknown', name: 'run-log' })).toEqual([])
  })
})

describe('actionsForConnect', () => {
  it('sweeps everything on a reconnect, because we cannot know what we missed', () => {
    expect(actionsForConnect(true)).toEqual([{ kind: 'invalidate-all' }])
  })

  it('does nothing on the first connect, which would double every startup request', () => {
    expect(actionsForConnect(false)).toEqual([])
  })
})

describe('coalesce', () => {
  const invalidate = (project: string): CacheAction => ({ kind: 'invalidate-project', project })
  const health = (project: string, ready: number): CacheAction => ({
    kind: 'patch-health',
    project,
    reachable: true,
    reason: '',
    health: { ...HEALTH, ready },
    writable: true,
    writeReason: '',
    pending: false,
  })

  it('collapses repeated invalidations of one project', () => {
    expect(coalesce([invalidate('loom'), invalidate('loom'), invalidate('loom')])).toEqual([
      invalidate('loom'),
    ])
  })

  it('keeps invalidations for distinct projects', () => {
    const out = coalesce([invalidate('loom'), invalidate('djinn')])
    expect(out).toHaveLength(2)
  })

  it('keeps only the last health patch per project', () => {
    const out = coalesce([health('loom', 1), health('loom', 9), health('djinn', 2)])
    expect(out).toHaveLength(2)
    expect(out.find((a) => a.kind === 'patch-health' && a.project === 'loom')).toMatchObject({
      health: { ready: 9 },
    })
  })

  it('lets invalidate-all subsume per-project invalidations', () => {
    const out = coalesce([invalidate('loom'), { kind: 'invalidate-all' }, invalidate('djinn')])
    expect(out).toEqual([{ kind: 'invalidate-all' }])
  })

  it('keeps health patches alongside invalidate-all — they never refetch', () => {
    const out = coalesce([health('loom', 4), { kind: 'invalidate-all' }])
    expect(out).toHaveLength(2)
    expect(out.at(-1)).toEqual({ kind: 'invalidate-all' })
  })

  it('is empty for an empty queue', () => {
    expect(coalesce([])).toEqual([])
  })

  it('collapses a realistic burst of a hundred events into two actions', () => {
    // What a busy summoner run looks like: the guard queues these while a menu
    // is open, and the flush must not fire a hundred refetches.
    const burst: CacheAction[] = []
    for (let i = 0; i < 50; i++) {
      burst.push(invalidate('loom'), health('loom', i))
    }
    expect(coalesce(burst)).toHaveLength(2)
  })
})

describe('integration query keys', () => {
  it('coalesces repeated key invalidations, keeping distinct keys apart', () => {
    const a: CacheAction = { kind: 'invalidate-query', key: ['run', 'djinn', 'run-1'] }
    const b: CacheAction = { kind: 'invalidate-query', key: ['run', 'djinn', 'run-2'] }
    expect(coalesce([a, a, a])).toEqual([a])
    expect(coalesce([a, b])).toHaveLength(2)
  })

  it('lets invalidate-all subsume key invalidations too', () => {
    expect(
      coalesce([{ kind: 'invalidate-query', key: ['run', 'djinn', 'run-1'] }, { kind: 'invalidate-all' }]),
    ).toEqual([{ kind: 'invalidate-all' }])
  })

  it('parses a run event name as unknown: core does not own it', () => {
    expect(parseEvent('run-updated', '{"project":"djinn","runId":"run-1"}')).toEqual({
      type: 'unknown',
      name: 'run-updated',
    })
  })
})
