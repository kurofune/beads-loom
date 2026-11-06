import { QueryClient, QueryObserver } from '@tanstack/react-query'
import { afterEach, describe, expect, it } from 'vitest'
import { applyActions } from '@/api/sse'
import { initiativesRefetch } from '@/lib/rollup-refetch'
import type { CacheAction } from '@/lib/sse-events'
import { djinnActionsFor, djinnKeys, parseDjinnEvent } from './live'

const runKeys = (project: string, runId: string): CacheAction[] => [
  { kind: 'invalidate-query', key: djinnKeys.run(project, runId) },
  { kind: 'invalidate-query', key: djinnKeys.runs(project) },
]

describe('run events (phase 4)', () => {
  it('parses run-log with its lines', () => {
    expect(
      parseDjinnEvent('run-log', JSON.stringify({ runId: 'run-1', lines: ['a', 'b'] })),
    ).toEqual({ type: 'run-log', runId: 'run-1', lines: ['a', 'b'] })
  })

  it('drops non-string entries rather than rendering [object Object]', () => {
    const event = parseDjinnEvent('run-log', JSON.stringify({ runId: 'run-1', lines: ['a', 3, null] }))
    expect(event).toEqual({ type: 'run-log', runId: 'run-1', lines: ['a'] })
  })

  it('produces NO cache action for a log line', () => {
    // Log lines go straight to the pane. If they became a cache action they
    // would queue behind the interaction guard, and a tail that freezes under
    // an open menu is the opposite of what a tail is for.
    expect(djinnActionsFor({ type: 'run-log', runId: 'run-1', lines: ['x'] })).toEqual([])
  })

  it('invalidates the run and the history list when a run changes', () => {
    expect(
      djinnActionsFor({
        type: 'run-changed',
        name: 'run-updated',
        runId: 'run-1',
        project: 'djinn',
        payload: {},
      }),
    ).toEqual(runKeys('djinn', 'run-1'))
  })

  it('carries project all the way from a real run-updated frame to the cache key', () => {
    // Round-2 I3 was a backend omission: `run-updated` shipped without
    // `project`, so this chain produced `invalidate-run` keyed on `""` and
    // every mid-run update was a silent no-op while `run-started` and
    // `run-finished` worked. The parse was already right; what was missing was
    // a test that would have failed.
    //
    // This is a verbatim frame captured off `/api/events` during a live managed
    // run, with the ids shortened.
    const frame = JSON.stringify({
      project: 'i3probe',
      runId: 'run-1785613848876567000',
      beads: [{ id: 'probe-aaa1', spawnedAt: '2026-08-01T19:50:51Z', retries: 0 }],
      costSoFar: 0,
    })
    const event = parseDjinnEvent('run-updated', frame)
    expect(event).toMatchObject({ type: 'run-changed', name: 'run-updated', project: 'i3probe' })
    expect(djinnActionsFor(event!)).toEqual(runKeys('i3probe', 'run-1785613848876567000'))
  })

  it('makes the pre-fix payload visibly produce an empty key rather than passing quietly', () => {
    // The regression's shape: no `project` on the wire. The parse still yields
    // an event (runId is what identifies a run), but the key it invalidates
    // matches nothing. Asserting the empty string keeps that failure legible
    // instead of leaving it to be rediscovered by a reviewer.
    const event = parseDjinnEvent('run-updated', JSON.stringify({ runId: 'run-1', costSoFar: 0 }))
    expect(djinnActionsFor(event!)).toEqual(runKeys('', 'run-1'))
  })

  it('also invalidates the project when a run finishes, since it closed beads', () => {
    const actions = djinnActionsFor({
      type: 'run-changed',
      name: 'run-finished',
      runId: 'run-1',
      project: 'djinn',
      payload: { state: 'finished' },
    })
    expect(actions).toContainEqual({ kind: 'invalidate-project', project: 'djinn' })
    for (const action of runKeys('djinn', 'run-1')) expect(actions).toContainEqual(action)
  })

  it('treats a bead-state transition as both a run and a project change', () => {
    const actions = djinnActionsFor({
      type: 'bead-state',
      project: 'djinn',
      runId: 'run-1',
      bead: 'djinn-a',
      kind: 'exit',
    })
    expect(actions).toEqual([
      ...runKeys('djinn', 'run-1'),
      { kind: 'invalidate-query', key: djinnKeys.evidence('djinn', 'djinn-a') },
      { kind: 'invalidate-project', project: 'djinn' },
    ])
  })

  it('rejects a run event with no run id', () => {
    expect(parseDjinnEvent('run-log', '{"lines":["a"]}')).toBeNull()
    expect(parseDjinnEvent('run-updated', '{"project":"djinn"}')).toBeNull()
  })
})

describe('evidence follows the run (loom-wvr)', () => {
  afterEach(() => initiativesRefetch.reset())

  /**
   * An evidence query with an observer, as an open issue sheet holds it. The
   * first answer has no report; every later one has the report a worker wrote.
   */
  async function openSheet(bead: string) {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    let calls = 0
    const observer = new QueryObserver(qc, {
      queryKey: djinnKeys.evidence('loom', bead),
      queryFn: async () => (calls++ === 0 ? { evidence: null } : { evidence: { criteria: ['written'] } }),
      staleTime: 30_000,
    })
    const stop = observer.subscribe(() => undefined)
    const shown = () => observer.getCurrentResult().data
    await until(() => shown() !== undefined)
    expect(shown()).toEqual({ evidence: null })
    return { qc, shown, calls: () => calls, stop }
  }

  async function until(ready: () => boolean) {
    for (let i = 0; i < 50 && !ready(); i++) await new Promise((r) => setTimeout(r, 1))
    expect(ready()).toBe(true)
  }

  const written = { evidence: { criteria: ['written'] } }

  it('refetches an open sheet\'s evidence when its bead changes state, inside the stale window', async () => {
    const sheet = await openSheet('loom-a')
    const event = parseDjinnEvent('bead-state', JSON.stringify({ project: 'loom', runId: 'run-1', bead: 'loom-a', kind: 'exit' }))
    applyActions(sheet.qc, djinnActionsFor(event!))
    await until(() => sheet.calls() === 2 && !sheet.qc.isFetching())
    expect(sheet.shown()).toEqual(written)
    sheet.stop()
  })

  it('refetches every open evidence query in the project when a run finishes', async () => {
    const sheet = await openSheet('loom-b')
    const event = parseDjinnEvent('run-finished', JSON.stringify({ project: 'loom', runId: 'run-1', state: 'finished' }))
    applyActions(sheet.qc, djinnActionsFor(event!))
    await until(() => sheet.calls() === 2 && !sheet.qc.isFetching())
    expect(sheet.shown()).toEqual(written)
    sheet.stop()
  })

  it('leaves another project\'s evidence alone', async () => {
    const sheet = await openSheet('loom-c')
    const event = parseDjinnEvent('run-finished', JSON.stringify({ project: 'other', runId: 'run-1', state: 'finished' }))
    applyActions(sheet.qc, djinnActionsFor(event!))
    await new Promise((r) => setTimeout(r, 10))
    expect(sheet.calls()).toBe(1)
    expect(sheet.shown()).toEqual({ evidence: null })
    sheet.stop()
  })
})
