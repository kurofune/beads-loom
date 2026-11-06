import { act, createElement } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { registeredIntegrations, streamSlots, type SseSlot } from '@/integrations'
import { DJINN, testProject } from '@/test/projects'
import { qk } from './hooks'
import { applyActions, CORE_EVENT_NAMES, projectsChangedActions, streamEventNames, useLoomStream } from './sse'
import { ROLLUP_QUIET_MS, initiativesRefetch } from '@/lib/rollup-refetch'
import { actionsFor, parseEvent } from '@/lib/sse-events'
import type { Project } from './types'

describe('projects-changed', () => {
  it('invalidates projects, discovery status, and cross-project now work', () => {
    const qc = new QueryClient()
    const invalidate = vi.spyOn(qc, 'invalidateQueries')

    const actions = projectsChangedActions('{"added":["alpha"],"removed":["beta"]}')
    applyActions(qc, actions)

    expect(invalidate).toHaveBeenCalledWith({ queryKey: qk.projects })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: qk.discovery })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: qk.now })
  })

  it('drops malformed frames instead of refreshing unrelated views', () => {
    expect(projectsChangedActions('not json')).toEqual([])
    expect(projectsChangedActions('{"added":"alpha","removed":[]}')).toEqual([])
    expect(projectsChangedActions('{"added":[],"removed":[3]}')).toEqual([])
  })
})

describe('project-health write state', () => {
  // useProjects never polls, so a project-health frame is the only way a
  // registry refresh that splits or repairs a project reaches the controls.
  const deliver = (qc: QueryClient, frame: object) => {
    const event = parseEvent('project-health', JSON.stringify(frame))
    if (!event) throw new Error('frame did not parse')
    applyActions(qc, actionsFor(event))
  }
  const health = { ready: 1, inProgress: 0, blocked: 0, open: 1, total: 1 }
  const loom = (qc: QueryClient) => qc.getQueryData<Project[]>(qk.projects)?.find((p) => p.name === 'loom')

  it('disables a writable project and re-enables it when repaired', () => {
    const qc = new QueryClient()
    qc.setQueryData<Project[]>(qk.projects, [testProject('loom'), testProject('other')])

    deliver(qc, { project: 'loom', reachable: true, health, writable: false, writeReason: 'split' })
    expect(loom(qc)).toMatchObject({ writable: false, writeReason: 'split', reachable: true })
    expect(qc.getQueryData<Project[]>(qk.projects)?.[1]).toMatchObject({ name: 'other', writable: true })

    deliver(qc, { project: 'loom', reachable: true, health, writable: true })
    expect(loom(qc)).toMatchObject({ writable: true, writeReason: '' })
  })

  it('carries the write state on an unreachable frame too', () => {
    const qc = new QueryClient()
    qc.setQueryData<Project[]>(qk.projects, [testProject('loom')])

    deliver(qc, { project: 'loom', reachable: false, reason: 'down', health: null, writable: false, writeReason: 'split' })
    expect(loom(qc)).toMatchObject({ reachable: false, writable: false, writeReason: 'split' })
  })
})

describe('project-health recovery', () => {
  const deliver = (qc: QueryClient, frame: object) => {
    const event = parseEvent('project-health', JSON.stringify(frame))
    if (!event) throw new Error('frame did not parse')
    applyActions(qc, actionsFor(event))
  }
  const loom = (qc: QueryClient) => qc.getQueryData<Project[]>(qk.projects)?.find((p) => p.name === 'loom')

  it('a reachable pending frame lifts an outage without a refetch of the project list', () => {
    const qc = new QueryClient()
    qc.setQueryData<Project[]>(qk.projects, [
      { ...testProject('loom'), reachable: false, reason: 'connection refused', health: null },
    ])
    const invalidate = vi.spyOn(qc, 'invalidateQueries')

    deliver(qc, { project: 'loom', reachable: true, health: null, writable: true, pending: true })
    expect(loom(qc)).toMatchObject({ reachable: true, reason: '', health: null })
    expect(invalidate).not.toHaveBeenCalledWith({ queryKey: qk.projects })
  })

  it('a pending frame keeps counts the cache already holds', () => {
    const qc = new QueryClient()
    const health = { ready: 2, inProgress: 1, blocked: 0, open: 3, total: 9 }
    qc.setQueryData<Project[]>(qk.projects, [{ ...testProject('loom'), health }])

    deliver(qc, { project: 'loom', reachable: true, health: null, writable: true, pending: true })
    expect(loom(qc)).toMatchObject({ reachable: true, health })
  })
})

describe('invalidate-project', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    initiativesRefetch.reset()
  })
  afterEach(() => {
    initiativesRefetch.reset()
    vi.useRealTimers()
  })

  it('invalidates initiatives for the affected project once the burst goes quiet', () => {
    const qc = new QueryClient()
    const invalidate = vi.spyOn(qc, 'invalidateQueries')

    applyActions(qc, [{ kind: 'invalidate-project', project: 'loom' }])
    // Deferred, not dropped: the board's eligible count still refreshes without
    // a manual reload (loom-g1v.5 AC7), just not on the event's own frame.
    expect(invalidate).not.toHaveBeenCalledWith({ queryKey: ['initiatives', 'loom'] })

    vi.advanceTimersByTime(ROLLUP_QUIET_MS)
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['initiatives', 'loom'] })
  })

  it('charges a streamed burst one rollup, not one per event — loom-zx1', () => {
    const qc = new QueryClient()
    const invalidate = vi.spyOn(qc, 'invalidateQueries')

    // Twenty bead-state events, the shape a djinn run puts on the wire.
    for (let i = 0; i < 20; i++) {
      applyActions(qc, [{ kind: 'invalidate-project', project: 'loom' }])
      vi.advanceTimersByTime(40)
    }
    vi.advanceTimersByTime(ROLLUP_QUIET_MS)

    const rollups = invalidate.mock.calls.filter(
      ([arg]) => (arg?.queryKey as unknown[] | undefined)?.[0] === 'initiatives',
    )
    expect(rollups).toHaveLength(1)
    // The cheap keys are unrationed — the board must not lag the stream.
    const lists = invalidate.mock.calls.filter(
      ([arg]) => (arg?.queryKey as unknown[] | undefined)?.[0] === 'issues',
    )
    expect(lists).toHaveLength(20)
  })
})

/* --- event names come from the mounted integrations (loom-wvr) ------------ */

const DJINN_ONLY = ['run-started', 'run-updated', 'run-finished', 'run-log', 'bead-state']

/** djinn's stream slot, read from the registry the way the app reads it. */
const djinnSse = (() => {
  const slot = registeredIntegrations().find((i) => i.id === 'djinn')?.sse
  if (!slot) throw new Error('the registry carries no djinn stream slot')
  return slot
})()

/** An EventSource that records which event names get a listener. */
class RecordingSource {
  static last: RecordingSource | undefined
  listened: string[] = []
  handlers = new Map<string, (e: MessageEvent<string>) => void>()
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  constructor(readonly url: string) {
    RecordingSource.last = this
  }
  addEventListener(name: string, fn: (e: MessageEvent<string>) => void) {
    this.listened.push(name)
    this.handlers.set(name, fn)
  }
  removeEventListener(name: string, fn: (e: MessageEvent<string>) => void) {
    if (this.handlers.get(name) === fn) this.handlers.delete(name)
  }
  /** The names with a listener attached right now. */
  live() {
    return [...this.handlers.keys()]
  }
  close() {}
}

describe('stream event names', () => {
  it('keeps run-* and bead-state out of the core list', () => {
    expect([...CORE_EVENT_NAMES]).toEqual(['db-invalidated', 'project-health', 'projects-changed'])
    for (const name of DJINN_ONLY) expect(CORE_EVENT_NAMES as readonly string[]).not.toContain(name)
  })

  it('is core plus the union of the mounted slots, each name once', () => {
    const twin: SseSlot = { eventNames: ['run-log', 'other-event'], onEvent: () => undefined }
    expect(streamEventNames([])).toEqual([...CORE_EVENT_NAMES])
    expect(streamEventNames([djinnSse, twin])).toEqual([...CORE_EVENT_NAMES, ...DJINN_ONLY, 'other-event'])
  })

  it('mounts djinn\'s slot only when some project has djinn', () => {
    expect(streamSlots([])).toEqual([])
    expect(streamSlots([testProject('plain')])).toEqual([])
    expect(streamSlots([testProject('plain'), testProject('loom', DJINN)])).toEqual([djinnSse])
  })
})

describe('the EventSource listeners', () => {
  beforeEach(() => {
    RecordingSource.last = undefined
    vi.stubGlobal('EventSource', RecordingSource)
  })
  afterEach(() => vi.unstubAllGlobals())

  function mountStream(initial: SseSlot[]) {
    const qc = new QueryClient()
    const root = createRoot(document.createElement('div'))
    function Stream({ slots }: { slots: SseSlot[] }) {
      useLoomStream(0, slots, () => undefined)
      return null
    }
    const render = (slots: SseSlot[]) =>
      act(() => root.render(createElement(QueryClientProvider, { client: qc }, createElement(Stream, { slots }))))
    render(initial)
    return { render, unmount: () => act(() => root.unmount()) }
  }

  it('registers no run-* or bead-state listener with no integration mounted', () => {
    const stream = mountStream([])
    const source = RecordingSource.last!
    expect(source.listened).toEqual([...CORE_EVENT_NAMES])
    for (const name of DJINN_ONLY) expect(source.listened).not.toContain(name)
    stream.unmount()
  })

  it('adds djinn\'s listeners once a djinn project appears, on the same connection', () => {
    const stream = mountStream([])
    const source = RecordingSource.last!
    stream.render([djinnSse])
    expect(RecordingSource.last).toBe(source)
    expect(source.listened).toEqual([...CORE_EVENT_NAMES, ...DJINN_ONLY])
    stream.unmount()
  })

  it('removes djinn\'s listeners when the last djinn project leaves, and re-adds them on return', () => {
    const onEvent = vi.fn()
    const slot: SseSlot = { eventNames: [...DJINN_ONLY], onEvent }
    const stream = mountStream([slot])
    const source = RecordingSource.last!
    const firstRunStarted = source.handlers.get('run-started')
    expect(source.live()).toEqual([...CORE_EVENT_NAMES, ...DJINN_ONLY])

    stream.render([])
    expect(RecordingSource.last).toBe(source)
    expect(source.live()).toEqual([...CORE_EVENT_NAMES])

    stream.render([slot])
    expect(RecordingSource.last).toBe(source)
    expect(source.live()).toEqual([...CORE_EVENT_NAMES, ...DJINN_ONLY])
    // A fresh listener, not the removed one, and exactly one per name.
    expect(source.handlers.get('run-started')).not.toBe(firstRunStarted)
    expect(source.listened.filter((n) => n === 'run-started')).toHaveLength(2)
    act(() => source.handlers.get('run-started')!({ data: '{"runId":"r"}' } as MessageEvent<string>))
    expect(onEvent).toHaveBeenCalledTimes(1)
    stream.unmount()
  })

  it('routes a djinn frame to djinn\'s slot', () => {
    const onEvent = vi.fn()
    const stream = mountStream([{ eventNames: ['run-updated'], onEvent }])
    const frame = '{"project":"loom","runId":"run-1"}'
    act(() => RecordingSource.last!.handlers.get('run-updated')!({ data: frame } as MessageEvent<string>))
    expect(onEvent).toHaveBeenCalledWith('run-updated', frame, expect.anything())
    stream.unmount()
  })
})

