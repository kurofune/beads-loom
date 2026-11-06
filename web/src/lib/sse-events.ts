import type { ProjectHealth } from '@/api/types'

/**
 * The SSE protocol, as pure data.
 *
 * Parsing and the event → cache-action mapping live here, apart from the
 * EventSource plumbing and apart from TanStack Query. That split is what makes
 * the interesting behaviour — including the interaction guard that phase 2's
 * seeded bug `loom-323` is about — testable without a browser or a live server.
 *
 * Wire shapes are `internal/sse/hub.go`.
 */

export type LoomEvent =
  | { type: 'db-invalidated'; project: string }
  | {
      type: 'project-health'
      project: string
      reachable: boolean
      reason: string
      /** null when the project is unreachable — the poller drops cached counts. */
      health: ProjectHealth | null
      /** False when bd writes elsewhere than Loom reads; a frame without it counts as writable. */
      writable: boolean
      writeReason: string
      /** True when the project is up but its counts are not known yet; `health` is then null. */
      pending: boolean
    }
  /** Any type the backend adds later. */
  | { type: 'unknown'; name: string }

/**
 * Parse one frame. Returns null only for genuinely unusable input; an unknown
 * event *name* is reported as `unknown` rather than dropped, so the connection
 * logic can still treat it as a sign of life.
 */
export function parseEvent(name: string, data: string): LoomEvent | null {
  let payload: unknown
  try {
    payload = JSON.parse(data)
  } catch {
    return name ? { type: 'unknown', name } : null
  }
  if (!payload || typeof payload !== 'object') return { type: 'unknown', name }
  const body = payload as Record<string, unknown>
  const project = typeof body.project === 'string' ? body.project : ''

  if (name === 'db-invalidated') {
    return project ? { type: 'db-invalidated', project } : null
  }

  if (name === 'project-health') {
    if (!project) return null
    return {
      type: 'project-health',
      project,
      reachable: body.reachable === true,
      reason: typeof body.reason === 'string' ? body.reason : '',
      health: isHealth(body.health) ? body.health : null,
      writable: body.writable !== false,
      writeReason: typeof body.writeReason === 'string' ? body.writeReason : '',
      pending: body.pending === true,
    }
  }

  return { type: 'unknown', name }
}

function isHealth(value: unknown): value is ProjectHealth {
  if (!value || typeof value !== 'object') return false
  const h = value as Record<string, unknown>
  return (
    typeof h.ready === 'number' &&
    typeof h.inProgress === 'number' &&
    typeof h.blocked === 'number' &&
    typeof h.open === 'number' &&
    typeof h.total === 'number'
  )
}

/* ---------------------------------------------------------------------------
 * Cache actions
 * ------------------------------------------------------------------------- */

export type CacheAction =
  /** Mark a project's issues / stats / detail queries stale. Refetches. */
  | { kind: 'invalidate-project'; project: string }
  /**
   * Write health straight into the projects cache. Deliberately NOT an
   * invalidation: the event already carries the counts, and refetching
   * /api/projects once a second per project would undo the whole point of the
   * backend's poller.
   */
  | {
      kind: 'patch-health'
      project: string
      reachable: boolean
      reason: string
      health: ProjectHealth | null
      writable: boolean
      writeReason: string
      /** Up with counts not known yet: keep whatever counts the cache holds. */
      pending: boolean
    }
  /** After a reconnect we do not know what we missed. Refetch everything once. */
  | { kind: 'invalidate-all' }
  /**
   * Mark one query key stale. Integrations name their own keys with this, so
   * core never learns their cache layout.
   */
  | { kind: 'invalidate-query'; key: readonly unknown[] }

export function actionsFor(event: LoomEvent): CacheAction[] {
  switch (event.type) {
    case 'db-invalidated':
      return [{ kind: 'invalidate-project', project: event.project }]

    case 'project-health':
      // Health only — no invalidation. A project recovering does need its data
      // refetched, but the backend already publishes `db-invalidated` right
      // after the recovery `project-health` (internal/sse/poller.go), so adding
      // one here would just double the work.
      return [
        {
          kind: 'patch-health',
          project: event.project,
          reachable: event.reachable,
          reason: event.reason,
          health: event.health,
          writable: event.writable,
          writeReason: event.writeReason,
          pending: event.pending,
        },
      ]

    case 'unknown':
      return []
  }
}

/**
 * Actions produced on (re)connect.
 *
 * The backend sends a full `project-health` snapshot on every connect, so
 * health is self-healing. Issue data is not, hence the sweep — but only on a
 * *re*connect: the first connect happens alongside the initial queries, and
 * invalidating them immediately would double every request at startup.
 */
export function actionsForConnect(isReconnect: boolean): CacheAction[] {
  return isReconnect ? [{ kind: 'invalidate-all' }] : []
}

/**
 * Collapse a queue of deferred actions.
 *
 * While the user is mid-interaction the actions pile up; when they finish we
 * apply the smallest equivalent set. An `invalidate-all` subsumes everything,
 * and repeated invalidations of one project collapse to one. Health patches
 * always survive — they are cheap, they never refetch, and the last one per
 * project is the truth.
 */
export function coalesce(actions: CacheAction[]): CacheAction[] {
  const healthByProject = new Map<string, CacheAction>()
  const keys = new Map<string, CacheAction>()
  const projects = new Set<string>()
  let all = false

  for (const action of actions) {
    switch (action.kind) {
      case 'invalidate-all':
        all = true
        break
      case 'invalidate-project':
        projects.add(action.project)
        break
      case 'invalidate-query':
        keys.set(JSON.stringify(action.key), action)
        break
      case 'patch-health':
        healthByProject.set(action.project, action)
        break
    }
  }

  const out = [...healthByProject.values()]
  if (all) return [...out, { kind: 'invalidate-all' }]
  for (const project of projects) out.push({ kind: 'invalidate-project', project })
  out.push(...keys.values())
  return out
}
