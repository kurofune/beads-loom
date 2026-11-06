import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import clsx from 'clsx'
import { useLoomStream, type StreamStatus } from '@/api/sse'
import { MOCK_MODE } from '@/api/client'
import { useProjects } from '@/api/hooks'
import { streamSlots } from '@/integrations'
import { useToast } from './Toast'

/**
 * Live-update context.
 *
 * Two responsibilities, deliberately together: it owns the SSE connection, and
 * it owns the *interaction lock* that decides when that connection is allowed
 * to disturb the UI. Anything that must not be interrupted — an open popover, a
 * modal, a drag in flight — holds a lock, and invalidations queue until the
 * last one is released.
 */
interface LiveApi {
  status: StreamStatus
  /** Take a lock. Returns the release function; safe to call twice. */
  acquire: () => () => void
}

const LiveContext = createContext<LiveApi | null>(null)

export function LiveProvider({ children }: { children: ReactNode }) {
  const [locks, setLocks] = useState(0)
  const toast = useToast()
  const projects = useProjects()
  // Only integrations some project has add listeners to the stream.
  const slots = useMemo(() => streamSlots(projects.data ?? []), [projects.data])
  const status = useLoomStream(locks, slots, toast.push)

  const acquire = useCallback(() => {
    setLocks((n) => n + 1)
    let released = false
    return () => {
      if (released) return
      released = true
      setLocks((n) => Math.max(0, n - 1))
    }
  }, [])

  const value = useMemo<LiveApi>(
    () => ({ status, acquire }),
    [status, acquire],
  )
  return <LiveContext.Provider value={value}>{children}</LiveContext.Provider>
}

export function useLive(): LiveApi {
  const ctx = useContext(LiveContext)
  if (!ctx) throw new Error('useLive must be used inside <LiveProvider>')
  return ctx
}

/**
 * Hold an interaction lock for as long as `active` is true.
 *
 * This is the whole fix for `loom-323`: while a menu is open, or a dialog, or a
 * drag is in flight, SSE invalidations are parked instead of refetching under
 * the user's hands.
 */
export function useInteractionLock(active: boolean): void {
  const { acquire } = useLive()
  useEffect(() => {
    if (!active) return
    return acquire()
  }, [active, acquire])
}

/* ---------------------------------------------------------------------------
 * The indicator
 * ------------------------------------------------------------------------- */

const LABEL: Record<StreamStatus['state'], string> = {
  connecting: 'connecting…',
  live: 'live',
  reconnecting: 'reconnecting…',
  offline: 'offline',
}

const TONE: Record<StreamStatus['state'], string> = {
  connecting: 'open',
  live: 'in_progress',
  reconnecting: 'pinned',
  offline: 'blocked',
}

/**
 * Connection state, in the sidebar footer.
 *
 * Quiet by design: `live` is the normal condition and should not draw the eye.
 * The states that cost the user something — reconnecting, offline — are the
 * ones that get colour and a count.
 */
export function ConnectionIndicator({ bdVersion }: { bdVersion?: string }) {
  const { status } = useLive()

  if (MOCK_MODE) {
    return (
      <p className="mono flex items-center gap-1.5 px-1 text-[10px] text-faint">
        <span className="dot" data-status="deferred" data-filled="true" />
        mock fixtures
      </p>
    )
  }

  const label = LABEL[status.state]
  return (
    <p
      className="mono flex items-center gap-1.5 px-1 text-[10px] text-faint"
      title={
        status.state === 'reconnecting'
          ? `Lost the event stream — retry ${status.attempts}`
          : status.state === 'live'
            ? 'Receiving live updates'
            : undefined
      }
    >
      <span
        className={clsx('dot', status.state === 'live' && 'animate-pulse')}
        data-status={TONE[status.state]}
        data-filled="true"
      />
      <span>{bdVersion ?? label}</span>
      {bdVersion && status.state !== 'live' && (
        <span style={{ color: 'var(--status-pinned)' }}>· {label}</span>
      )}
      {status.deferred > 0 && (
        // Honest about the guard: updates are waiting, not lost.
        <span title={`${status.deferred} update(s) waiting for you to finish`}>
          · {status.deferred} queued
        </span>
      )}
    </p>
  )
}
