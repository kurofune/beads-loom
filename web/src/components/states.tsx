import type { ReactNode } from 'react'
import { ApiError } from '@/api/client'

/* ---------------------------------------------------------------------------
 * Loading / empty / error / unreachable.
 *
 * These get the same care as the happy path, because in an ops console they
 * ARE the interesting states: a dead dolt server is information, not an
 * apology. Copy is terse and specific — it says what happened and what to
 * type, never "Oops! Something went wrong".
 * ------------------------------------------------------------------------- */

/** A single loom motif, reused across every empty surface. Warp threads with
 *  one weft crossing — the app's only piece of ornament. */
function LoomMark({ size = 40 }: { size?: number }) {
  return (
    <svg viewBox="0 0 40 40" width={size} height={size} aria-hidden="true" className="text-line">
      {[6, 13, 20, 27, 34].map((x) => (
        <line key={x} x1={x} y1="3" x2={x} y2="37" stroke="currentColor" strokeWidth="1.25" />
      ))}
      <path
        d="M2 15c6 0 6 5 12 5s6-5 12-5 6 5 12 5"
        fill="none"
        stroke="var(--accent)"
        strokeWidth="1.5"
        strokeLinecap="round"
        opacity="0.85"
      />
      <path
        d="M2 25c6 0 6 5 12 5s6-5 12-5 6 5 12 5"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.25"
        strokeLinecap="round"
      />
    </svg>
  )
}

function Frame({ children }: { children: ReactNode }) {
  return (
    <div className="flex h-full min-h-[280px] flex-col items-center justify-center gap-4 px-8 text-center">
      {children}
    </div>
  )
}

export function EmptyState({
  title,
  detail,
  action,
}: {
  title: string
  detail?: ReactNode
  action?: ReactNode
}) {
  return (
    <Frame>
      <LoomMark />
      <div className="max-w-[38ch] space-y-1.5">
        <p className="text-[13px] font-medium text-ink">{title}</p>
        {detail && <p className="text-[12px] leading-relaxed text-faint">{detail}</p>}
      </div>
      {action}
    </Frame>
  )
}

/**
 * A failed request.
 *
 * `action` is the escape hatch for a *scoped* failure. When a view drops its
 * scope chip because the request failed (the chip would otherwise claim a
 * filter the data never got — loom-9st), the way out of that scope has to
 * reappear here, or the user is stranded on an error with no affordance but a
 * Retry that cannot succeed (SCARS: invisible affordances).
 */
export function ErrorState({
  error,
  onRetry,
  action,
}: {
  error: ApiError | Error
  onRetry?: () => void
  action?: ReactNode
}) {
  const api = error instanceof ApiError ? error : null
  const offline = api?.isOffline ?? false

  return (
    <Frame>
      <span className="chip" data-tone="danger" data-solid="true">
        {offline ? 'no connection' : `HTTP ${api?.status ?? 'error'}`}
      </span>
      <div className="max-w-[46ch] space-y-2">
        <p className="text-[13px] font-medium text-ink">
          {offline ? 'The Loom server is not answering' : 'The request failed'}
        </p>
        <p className="text-[12px] leading-relaxed text-faint">{error.message}</p>
        {offline && (
          <p className="mono text-[11px] leading-relaxed text-faint">
            start it with <span className="text-muted">make dev</span> — it listens on
            127.0.0.1:7333
          </p>
        )}
      </div>
      <div className="flex items-center gap-2">
        {onRetry && (
          <button type="button" className="btn" onClick={onRetry}>
            Retry
          </button>
        )}
        {action}
      </div>
    </Frame>
  )
}

/**
 * A project whose dolt server we could not dial. Per ARCHITECTURE.md this is a
 * first-class per-project degraded state — it must never look like a crash,
 * and it must never take the other projects down with it.
 */
export function UnreachableState({
  project,
  reason,
  workdir,
}: {
  project: string
  reason: string
  workdir?: string
}) {
  return (
    <Frame>
      <span className="chip" data-tone="warn" data-solid="true">
        server unreachable
      </span>
      <div className="max-w-[52ch] space-y-2">
        <p className="text-[13px] font-medium text-ink">
          <span className="mono">{project}</span>'s dolt server is not answering
        </p>
        {reason && (
          <p className="mono rounded-sm bg-sunken px-2.5 py-1.5 text-[11px] leading-relaxed text-muted">
            {reason}
          </p>
        )}
        <p className="text-[12px] leading-relaxed text-faint">
          Ports are dynamic and change whenever a server restarts, so this usually just
          means nothing has started it yet.
        </p>
        <p className="mono text-[11px] leading-relaxed text-faint">
          bd dolt start -C {workdir ?? '<workdir>'}
        </p>
      </div>
    </Frame>
  )
}

/**
 * A small part of a screen still loading — a section or a tile, not a whole
 * view. `label` names what is coming, for screen readers and the eye.
 */
export function InlineLoading({ label, children }: { label: string; children?: ReactNode }) {
  return (
    <span aria-busy="true" aria-label={label} className="text-[11.5px] text-faint" data-state="loading">
      {children ?? <span className="shimmer inline-block h-2.5 w-24 rounded-xs align-middle" />}
    </span>
  )
}

/**
 * A small part of a screen whose request failed. It names the status code so
 * a 503 and a 422 do not look alike, and offers a retry when one can help. A
 * query timeout says so: the server is up and the same read can succeed.
 */
export function InlineError({
  error,
  what,
  onRetry,
}: {
  error: ApiError | Error
  /** What failed to load, e.g. "evidence". */
  what: string
  onRetry?: () => void
}) {
  const api = error instanceof ApiError ? error : null
  const code = api?.isOffline ? 'no connection' : `HTTP ${api?.status ?? 'error'}`
  return (
    <span role="alert" className="inline-flex items-center gap-1.5 text-[11.5px] text-muted" data-state="error">
      <span className="chip" data-tone="danger" title={error.message}>
        {code}
      </span>
      <span>{api?.isQueryTimeout ? `${what} query timed out` : `could not load ${what}`}</span>
      {onRetry && (
        <button type="button" className="btn btn-quiet h-5 text-[11px]" onClick={onRetry}>
          Retry
        </button>
      )}
    </span>
  )
}

/** Backlog skeleton — real row geometry, so the layout does not jump on load. */
export function BacklogSkeleton() {
  const widths = [46, 62, 38, 55, 70, 44, 60, 35, 52, 66, 40, 58]
  return (
    <div aria-busy="true" aria-label="Loading issues" className="px-3 pt-2">
      <div className="shimmer mb-2 h-3 w-24 rounded-xs" />
      {widths.map((w, i) => (
        <div
          key={i}
          className="flex items-center gap-3"
          style={{ height: 'var(--row-h)', opacity: 1 - i * 0.06 }}
        >
          <span className="shimmer h-[7px] w-[7px] rounded-full" />
          <span className="shimmer h-2.5 w-[54px] rounded-xs" />
          <span className="shimmer h-2.5 rounded-xs" style={{ width: `${w}%` }} />
        </div>
      ))}
    </div>
  )
}

export function DetailSkeleton() {
  return (
    <div aria-busy="true" aria-label="Loading issue" className="space-y-6 p-6">
      <div className="space-y-2">
        <div className="shimmer h-2.5 w-20 rounded-xs" />
        <div className="shimmer h-4 w-3/4 rounded-xs" />
      </div>
      {[3, 5, 2].map((lines, i) => (
        <div key={i} className="space-y-2">
          <div className="shimmer h-2 w-16 rounded-xs" />
          {Array.from({ length: lines }).map((_, j) => (
            <div
              key={j}
              className="shimmer h-2.5 rounded-xs"
              style={{ width: `${100 - j * 11}%` }}
            />
          ))}
        </div>
      ))}
    </div>
  )
}
