import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import clsx from 'clsx'
import { api } from '@/api/client'
import type { RunSource } from '@/api/types'
import { appendLog, emptyLog, isAtBottom, loadLog, LOG_CAP, type LogBuffer } from './runs'

/**
 * The live log tail.
 *
 * ANSI is already stripped server-side, so this is a plain monospace pane — but
 * two behaviours are load-bearing:
 *
 *   · **Autoscroll pauses when you scroll up.** A tail that yanks you back to
 *     the bottom mid-sentence is worse than one that does not follow at all.
 *     Returning to the bottom re-arms it, as does the resume button.
 *   · **The buffer is capped.** A long run emits tens of thousands of lines and
 *     an unbounded array in React state will stall the tab. Older lines are
 *     dropped from the head and the pane says so rather than pretending;
 *     scrollback is one click away via the offset endpoint.
 */
export function RunLog({
  project,
  runId,
  source,
  /** Lines arriving on the SSE stream for this run. */
  incoming,
  live,
}: {
  project: string
  runId: string
  /** Loom captures only the runs it started; an external run has no scrollback to ask for. */
  source: RunSource
  incoming: readonly string[]
  live: boolean
}) {
  const [buffer, setBuffer] = useState<LogBuffer>(emptyLog)
  const [follow, setFollow] = useState(true)
  const [loading, setLoading] = useState(false)
  const [noCapture, setNoCapture] = useState(source === 'external')
  const paneRef = useRef<HTMLDivElement>(null)
  const consumed = useRef(0)

  /** Pull the whole scrollback the backend has captured. */
  const loadScrollback = useCallback(async () => {
    if (source === 'external') {
      setNoCapture(true)
      return
    }
    setLoading(true)
    try {
      const chunk = await api.runLog(project, runId, 0)
      setBuffer(loadLog(chunk.text))
      setNoCapture(false)
    } catch {
      // An external run has no Loom-side capture; that is expected, not an error.
      setNoCapture(true)
    } finally {
      setLoading(false)
    }
  }, [project, runId, source])

  // Load once per run, then let SSE append.
  useEffect(() => {
    consumed.current = 0
    setBuffer(emptyLog)
    void loadScrollback()
  }, [loadScrollback])

  // Append only what we have not already taken from the shared stream buffer.
  useEffect(() => {
    if (incoming.length <= consumed.current) {
      // The parent reset its buffer (new run); resync rather than replay.
      consumed.current = Math.min(consumed.current, incoming.length)
      return
    }
    const fresh = incoming.slice(consumed.current)
    consumed.current = incoming.length
    setBuffer((b) => appendLog(b, fresh))
  }, [incoming])

  useLayoutEffect(() => {
    if (!follow) return
    const pane = paneRef.current
    if (pane) pane.scrollTop = pane.scrollHeight
  }, [buffer, follow])

  const onScroll = () => {
    const pane = paneRef.current
    if (pane) setFollow(isAtBottom(pane))
  }

  return (
    <section className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-md bg-sunken ring-1 ring-line">
      <header className="flex shrink-0 items-center gap-2 border-b border-line px-2.5 py-1.5">
        <span className="legend">Console</span>
        {live && (
          <span className="mono flex items-center gap-1 text-[10px]" style={{ color: 'var(--status-in-progress)' }}>
            <span className="dot animate-pulse" data-status="in_progress" data-filled="true" />
            tailing
          </span>
        )}
        <span className="mono text-[10px] text-faint">
          {buffer.lines.length} line{buffer.lines.length === 1 ? '' : 's'}
          {buffer.dropped > 0 && ` · ${buffer.dropped} older dropped`}
        </span>

        <span className="ml-auto flex items-center gap-1.5">
          {!follow && (
            <button
              type="button"
              className="btn btn-quiet h-5 px-2 text-[10.5px]"
              onClick={() => {
                setFollow(true)
                const pane = paneRef.current
                if (pane) pane.scrollTop = pane.scrollHeight
              }}
              title="Resume following the tail"
            >
              ↓ resume
            </button>
          )}
          <button
            type="button"
            className="btn btn-quiet h-5 px-2 text-[10.5px]"
            onClick={() => void loadScrollback()}
            disabled={loading}
            title="Reload the full captured log from the server"
          >
            {loading ? 'loading…' : 'open raw log'}
          </button>
        </span>
      </header>

      <div
        ref={paneRef}
        onScroll={onScroll}
        className="mono min-h-[160px] flex-1 overflow-auto px-2.5 py-2 text-[11px] leading-[1.55] text-muted"
        role="log"
        aria-live={follow ? 'polite' : 'off'}
        aria-label="Run console output"
      >
        {noCapture && buffer.lines.length === 0 && (
          <p className="py-6 text-center text-faint">
            No console capture for this run. Loom only captures runs it started —
            an external run's output went to whoever's terminal launched it.
          </p>
        )}

        {buffer.dropped > 0 && (
          <p className="mb-1 text-faint">
            … {buffer.dropped} earlier line{buffer.dropped === 1 ? '' : 's'} dropped (buffer
            holds {LOG_CAP}); reload for the full capture
          </p>
        )}

        {buffer.lines.map((line, i) => (
          <div key={i} className={clsx('whitespace-pre-wrap', lineTone(line))}>
            {line || ' '}
          </div>
        ))}

        {!noCapture && buffer.lines.length === 0 && !loading && (
          <p className="py-6 text-center text-faint">Waiting for output…</p>
        )}
      </div>
    </section>
  )
}

/**
 * Minimal colour, on the two things worth spotting while scanning: a finished
 * step and a failure. Everything else stays muted so the pane reads as a log
 * and not a Christmas tree.
 */
function lineTone(line: string): string | undefined {
  if (/^\s*✅/.test(line)) return 'text-ink'
  if (/\b(FAILED|CRASHED|error|halt)\b/i.test(line)) return 'text-danger'
  if (/^═|Session Summary/.test(line)) return 'text-ink'
  return undefined
}
