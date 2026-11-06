import { useSyncExternalStore } from 'react'
import type { CacheAction } from '@/lib/sse-events'
import type { SseHost, SseSlot } from '../types'
import { runLogs } from './run-log-store'
import { formatUsd } from './runs'

/** djinn's query keys. Core never reads them; stream frames name them. */
export const djinnKeys = {
  runs: (project: string) => ['runs', project] as const,
  run: (project: string, runId: string) => ['run', project, runId] as const,
  forensics: (project: string, bead: string) => ['forensics', project, bead] as const,
  /** Every bead's evidence in one project: a run can write any of them. */
  evidenceIn: (project: string) => ['djinn-evidence', project] as const,
  evidence: (project: string, bead: string) => ['djinn-evidence', project, bead] as const,
  spend: (project: string) => ['djinn-spend', project] as const,
}

/** The event names djinn adds to the stream (internal/integration/djinn EventNames). */
export const DJINN_EVENT_NAMES = [
  'run-started',
  'run-updated',
  'run-finished',
  'run-log',
  'bead-state',
] as const

export type DjinnEvent =
  /** A run's console output. Never queued behind the interaction guard. */
  | { type: 'run-log'; runId: string; lines: string[] }
  /** A run started, changed or ended. */
  | {
      type: 'run-changed'
      name: 'run-started' | 'run-updated' | 'run-finished'
      runId: string
      project: string
      payload: Record<string, unknown>
    }
  /** One bead's lifecycle transition inside a run. */
  | { type: 'bead-state'; project: string; runId: string; bead: string; kind: string }

/** Parse one djinn frame. Null for a name djinn does not own or a frame it cannot use. */
export function parseDjinnEvent(name: string, data: string): DjinnEvent | null {
  let payload: unknown
  try {
    payload = JSON.parse(data)
  } catch {
    return null
  }
  if (!payload || typeof payload !== 'object') return null
  const body = payload as Record<string, unknown>
  const project = typeof body.project === 'string' ? body.project : ''
  const runId = typeof body.runId === 'string' ? body.runId : ''

  if (name === 'run-log') {
    const lines = Array.isArray(body.lines) ? body.lines.filter((l) => typeof l === 'string') : []
    return runId ? { type: 'run-log', runId, lines: lines as string[] } : null
  }
  if (name === 'run-started' || name === 'run-updated' || name === 'run-finished') {
    return runId ? { type: 'run-changed', name, runId, project, payload: body } : null
  }
  if (name === 'bead-state') {
    const bead = typeof body.bead === 'string' ? body.bead : ''
    return bead ? { type: 'bead-state', project, runId, bead, kind: String(body.kind ?? '') } : null
  }
  return null
}

const invalidateRun = (project: string, runId: string): CacheAction[] => [
  { kind: 'invalidate-query', key: djinnKeys.run(project, runId) },
  { kind: 'invalidate-query', key: djinnKeys.runs(project) },
]

/** The cache actions one djinn event implies. */
export function djinnActionsFor(event: DjinnEvent): CacheAction[] {
  switch (event.type) {
    case 'run-changed':
      // A finished run also changes the issues it closed, the spend total, and
      // the evidence its workers wrote.
      return event.name === 'run-finished'
        ? [
            ...invalidateRun(event.project, event.runId),
            { kind: 'invalidate-query', key: djinnKeys.spend(event.project) },
            { kind: 'invalidate-query', key: djinnKeys.evidenceIn(event.project) },
            { kind: 'invalidate-project', project: event.project },
          ]
        : invalidateRun(event.project, event.runId)
    case 'bead-state':
      // A bead changing state inside a run means its bd row changed too, and a
      // worker may have written its evidence.
      return [
        ...invalidateRun(event.project, event.runId),
        { kind: 'invalidate-query', key: djinnKeys.evidence(event.project, event.bead) },
        { kind: 'invalidate-project', project: event.project },
      ]
    case 'run-log':
      return []
  }
}

/**
 * Runs live right now, by project, held outside React like the log store: the
 * sidebar badge and the Now view read it, and nothing else should re-render
 * when it moves.
 */
class ActiveRuns {
  #byProject: Readonly<Record<string, string>> = {}
  #subscribers = new Set<() => void>()

  get = (): Readonly<Record<string, string>> => this.#byProject

  started(project: string, runId: string): void {
    this.#byProject = { ...this.#byProject, [project]: runId }
    this.#emit()
  }

  finished(project: string, runId: string): void {
    if (this.#byProject[project] !== runId) return
    const next = { ...this.#byProject }
    delete next[project]
    this.#byProject = next
    this.#emit()
  }

  subscribe = (fn: () => void): (() => void) => {
    this.#subscribers.add(fn)
    return () => {
      this.#subscribers.delete(fn)
    }
  }

  #emit(): void {
    for (const fn of this.#subscribers) fn()
  }
}

export const activeRuns = new ActiveRuns()

/** Which run, if any, is live in a project. */
export function useActiveRun(project: string): string | undefined {
  return useSyncExternalStore(activeRuns.subscribe, activeRuns.get, activeRuns.get)[project]
}

/** Console lines streamed for one run. */
export function useRunEvents(runId: string): { logLines: readonly string[] } {
  const logLines = useSyncExternalStore(
    runLogs.subscribe,
    () => runLogs.get(runId),
    () => runLogs.get(runId),
  )
  return { logLines }
}

function onRunChanged(event: Extract<DjinnEvent, { type: 'run-changed' }>, host: SseHost): void {
  const { name, project, runId, payload } = event
  if (name === 'run-started') {
    activeRuns.started(project, runId)
    host.toast({ tone: 'ok', title: `Run started in ${project}`, detail: runId })
    return
  }
  if (name !== 'run-finished') return
  activeRuns.finished(project, runId)
  const summary = payload.summary as
    | { closed?: number; failures?: number; totalCostUsd?: number }
    | null
    | undefined
  const state = String(payload.state ?? 'finished')
  host.toast({
    tone: state === 'failed' ? 'error' : 'ok',
    title: `Run ${state} in ${project}`,
    detail: summary
      ? `${summary.closed ?? 0} closed · ${summary.failures ?? 0} failed · ${formatUsd(summary.totalCostUsd ?? 0)}`
      : runId,
  })
}

export const djinnSse: SseSlot = {
  eventNames: [...DJINN_EVENT_NAMES],
  onEvent: (name, data, host) => {
    const event = parseDjinnEvent(name, data)
    if (!event) return
    // Log lines skip the guard: a live tail that freezes while a menu is open
    // is the opposite of what a tail is for, and appending to a log pane
    // cannot clobber an optimistic edit.
    if (event.type === 'run-log') {
      runLogs.append(event.runId, event.lines)
      return
    }
    if (event.type === 'run-changed') onRunChanged(event, host)
    host.dispatch(djinnActionsFor(event))
  },
}
