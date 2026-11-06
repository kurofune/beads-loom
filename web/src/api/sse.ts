import { useCallback, useEffect, useRef, useState } from 'react'
import { useIsMutating, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { MOCK_MODE } from './client'
import { qk } from './hooks'
import type { Project, ProjectHealth } from './types'
import type { SseHost, SseSlot } from '@/integrations'
import { backoffDelay } from '@/lib/backoff'
import { initiativesRefetch } from '@/lib/rollup-refetch'
import {
  actionsFor,
  actionsForConnect,
  coalesce,
  parseEvent,
  type CacheAction,
} from '@/lib/sse-events'

export type ConnectionState = 'connecting' | 'live' | 'reconnecting' | 'offline'

/**
 * The stream's busy state, readable from outside React.
 *
 * `useLoomStream` owns it; a rollup refetch that has been deferred onto a timer
 * reads it (loom-zx1). The guard below parks *stream-dispatched* actions while
 * the UI is busy, but a coalesced refetch fires from a `setTimeout` that no
 * longer passes through `dispatch` — so without this mirror it would land under
 * an open menu, which is exactly loom-323. There is one stream per tab, so one
 * mirror is enough.
 */
const streamBusy = { current: false }

/** True while a menu, dialog, drag, or pending write is in progress. */
export function uiIsBusy(): boolean {
  return streamBusy.current
}

/** The event names core handles. Integrations add theirs through `sse` slots. */
export const CORE_EVENT_NAMES = ['db-invalidated', 'project-health', 'projects-changed'] as const

/** Core names plus every given slot's names, each once, core first. */
export function streamEventNames(slots: readonly SseSlot[]): string[] {
  return [...new Set<string>([...CORE_EVENT_NAMES, ...slots.flatMap((slot) => slot.eventNames)])]
}

type StreamCacheAction = CacheAction | { kind: 'invalidate-project-set' }

/** The project-set event carries names for feedback elsewhere, but this client
 * only needs to verify the frame before refreshing the three affected views. */
export function projectsChangedActions(data: string): StreamCacheAction[] {
  let payload: unknown
  try {
    payload = JSON.parse(data)
  } catch {
    return []
  }
  if (!payload || typeof payload !== 'object') return []
  const body = payload as Record<string, unknown>
  if (!Array.isArray(body.added) || !body.added.every((name) => typeof name === 'string')) {
    return []
  }
  if (!Array.isArray(body.removed) || !body.removed.every((name) => typeof name === 'string')) {
    return []
  }
  return [{ kind: 'invalidate-project-set' }]
}

/* ---------------------------------------------------------------------------
 * Applying actions to the query cache
 * ------------------------------------------------------------------------- */

export function applyActions(qc: QueryClient, actions: StreamCacheAction[]): void {
  for (const action of actions) {
    switch (action.kind) {
      case 'invalidate-project-set':
        void qc.invalidateQueries({ queryKey: qk.projects })
        void qc.invalidateQueries({ queryKey: qk.discovery })
        void qc.invalidateQueries({ queryKey: qk.now })
        break

      case 'invalidate-project':
        void qc.invalidateQueries({ queryKey: ['issues', action.project] })
        void qc.invalidateQueries({ queryKey: qk.stats(action.project) })
        // The rollup is the one unbounded read on this list, and since
        // loom-g1v.5 the board mounts it too — so a run that writes fifty
        // times would re-run it fifty times per open board tab. Rationed to
        // one refetch per burst (loom-zx1); everything else stays immediate.
        initiativesRefetch.request(action.project, {
          run: () => void qc.invalidateQueries({ queryKey: ['initiatives', action.project] }),
          hold: uiIsBusy,
        })
        void qc.invalidateQueries({ queryKey: ['issue', action.project] })
        void qc.invalidateQueries({ queryKey: qk.dolt(action.project) })
        break

      case 'patch-health':
        // Written straight into the cache. The event already carries the
        // counts, so refetching /api/projects here would defeat the poller.
        qc.setQueryData<Project[]>(qk.projects, (old) =>
          old?.map((p) =>
            p.name === action.project
              ? {
                  ...p,
                  reachable: action.reachable,
                  reason: action.reason,
                  health: action.pending ? p.health : (action.health as ProjectHealth | null),
                  writable: action.writable,
                  writeReason: action.writeReason,
                }
              : p,
          ),
        )
        break

      case 'invalidate-query':
        void qc.invalidateQueries({ queryKey: action.key })
        break

      case 'invalidate-all':
        // A failed schema query turns retryOnMount off (useProjectSchema), so
        // once its menu closed, a mere invalidation would leave it failed for
        // the next open. Resetting it to pending makes that open fetch; an
        // open menu's failed query refetches through the invalidation below.
        void qc.resetQueries({
          queryKey: ['schema'],
          predicate: (q) => q.state.status === 'error' && q.getObserversCount() === 0,
        })
        void qc.invalidateQueries()
        break
    }
  }
}

function coalesceStreamActions(actions: StreamCacheAction[]): StreamCacheAction[] {
  const projectSetChanged = actions.some((action) => action.kind === 'invalidate-project-set')
  const ordinary = actions.filter((action): action is CacheAction => action.kind !== 'invalidate-project-set')
  const result: StreamCacheAction[] = coalesce(ordinary)
  if (projectSetChanged && !result.some((action) => action.kind === 'invalidate-all')) {
    result.push({ kind: 'invalidate-project-set' })
  }
  return result
}

/* ---------------------------------------------------------------------------
 * The stream
 * ------------------------------------------------------------------------- */

export interface StreamStatus {
  state: ConnectionState
  /** How many reconnect attempts since the last successful connect. */
  attempts: number
  /** Actions held back because the user is mid-interaction. */
  deferred: number
}

/**
 * Live updates.
 *
 * ## The interaction guard
 *
 * This is the load-bearing part, and it is what seeded bug `loom-323` is about:
 * *"when the SSE invalidation lands mid-interaction, the inline status dropdown
 * closes and drops the pending selection."*
 *
 * An invalidation that lands while the user is choosing from a popover, filling
 * in a dialog, dragging a card, or waiting on a write does two bad things: the
 * refetch can overwrite an optimistic edit the server has not applied yet, and
 * the resulting re-render churn can disturb whatever is open. So invalidations
 * are **queued while the UI is busy** and applied — coalesced — the moment it
 * goes quiet.
 *
 * "Busy" is two things:
 *   · an explicit interaction lock (menu / dialog / drag), and
 *   · any pending mutation, via TanStack's own `useIsMutating`.
 *
 * Health patches are queued too. They are cheap and never refetch, but a
 * project's counts changing under an open menu is still visual noise, and
 * coalescing keeps only the last one per project anyway.
 */
/**
 * `slots` are the mounted integrations' stream slots. Their event names are
 * listened for in addition to core's; a slot that arrives or leaves after the
 * connection opened adds or removes its listeners on the live source, without
 * a reconnect.
 */
export function useLoomStream(
  interactionCount: number,
  slots: readonly SseSlot[],
  toast: SseHost['toast'],
): StreamStatus {
  const qc = useQueryClient()
  const pendingMutations = useIsMutating()
  const busy = interactionCount > 0 || pendingMutations > 0

  const [status, setStatus] = useState<StreamStatus>({
    state: MOCK_MODE ? 'offline' : 'connecting',
    attempts: 0,
    deferred: 0,
  })

  const queue = useRef<StreamCacheAction[]>([])
  const busyRef = useRef(busy)
  busyRef.current = busy
  streamBusy.current = busy

  /**
   * Apply now, or park until the interaction ends. Stable identity (it only
   * reads refs) so the EventSource effect never has to re-subscribe.
   */
  const dispatch = useCallback(
    (actions: StreamCacheAction[]) => {
      if (actions.length === 0) return
      if (busyRef.current) {
        queue.current.push(...actions)
        setStatus((s) => ({ ...s, deferred: queue.current.length }))
        return
      }
      applyActions(qc, actions)
    },
    [qc],
  )
  const dispatchRef = useRef(dispatch)
  dispatchRef.current = dispatch
  const slotsRef = useRef(slots)
  slotsRef.current = slots
  const toastRef = useRef(toast)
  toastRef.current = toast
  const sourceRef = useRef<EventSource | null>(null)
  const listening = useRef(new Map<string, (e: Event) => void>())

  /** Route one frame: core names here, the rest to the slot that claims them. */
  const onFrame = useCallback(
    (name: string, data: string) => {
      if (name === 'projects-changed') {
        dispatchRef.current(projectsChangedActions(data))
        return
      }
      if ((CORE_EVENT_NAMES as readonly string[]).includes(name)) {
        const event = parseEvent(name, data)
        if (event) dispatchRef.current(actionsFor(event))
        return
      }
      const host: SseHost = {
        dispatch: (actions) => dispatchRef.current(actions),
        toast: (t) => toastRef.current(t),
      }
      for (const slot of slotsRef.current) {
        if (slot.eventNames.includes(name)) slot.onEvent(name, data, host)
      }
    },
    [],
  )

  /**
   * Make the source hear exactly the wanted names: add a listener for each new
   * one and remove the listener of each name no mounted slot still wants.
   */
  const listen = useCallback(
    (source: EventSource) => {
      const wanted = new Set(streamEventNames(slotsRef.current))
      for (const [name, listener] of listening.current) {
        if (wanted.has(name)) continue
        source.removeEventListener(name, listener)
        listening.current.delete(name)
      }
      for (const name of wanted) {
        if (listening.current.has(name)) continue
        const listener = (e: Event) => onFrame(name, (e as MessageEvent<string>).data)
        listening.current.set(name, listener)
        source.addEventListener(name, listener)
      }
    },
    [onFrame],
  )

  const names = streamEventNames(slots).join(',')
  useEffect(() => {
    if (sourceRef.current) listen(sourceRef.current)
  }, [names, listen])

  // Flush the moment the UI goes quiet.
  useEffect(() => {
    if (busy || queue.current.length === 0) return
    const actions = coalesceStreamActions(queue.current)
    queue.current = []
    setStatus((s) => ({ ...s, deferred: 0 }))
    applyActions(qc, actions)
  }, [busy, qc])

  useEffect(() => {
    // The mock transport has no server to stream from; say so honestly rather
    // than showing a live dot that means nothing.
    if (MOCK_MODE || typeof EventSource === 'undefined') return

    let source: EventSource | null = null
    let retry: number | undefined
    let attempt = 0
    let everConnected = false
    let closed = false

    const connect = () => {
      if (closed) return
      source = new EventSource('/api/events')

      source.onopen = () => {
        // A reconnect means we may have missed events entirely; the backend
        // re-sends a health snapshot, but issue data has to be swept once.
        dispatchRef.current(actionsForConnect(everConnected))
        everConnected = true
        attempt = 0
        setStatus((s) => ({ ...s, state: 'live', attempts: 0 }))
      }

      sourceRef.current = source
      listening.current = new Map()
      listen(source)

      source.onerror = () => {
        // EventSource retries on its own schedule; close it so the backoff
        // here is the only one in play.
        source?.close()
        source = null
        sourceRef.current = null
        if (closed) return

        const delay = backoffDelay(attempt)
        attempt += 1
        setStatus((s) => ({
          ...s,
          state: everConnected ? 'reconnecting' : 'offline',
          attempts: attempt,
        }))
        retry = window.setTimeout(connect, delay)
      }
    }

    connect()

    return () => {
      closed = true
      if (retry) window.clearTimeout(retry)
      source?.close()
      sourceRef.current = null
    }
  }, [listen])

  return status
}
