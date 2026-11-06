import { useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import clsx from 'clsx'
import { api, ApiError } from '@/api/client'
import { qk, useDoltStatus } from '@/api/hooks'
import type { DoltStatus, DoltSyncResult } from '@/api/types'
import { relativeTime, absoluteTime } from '@/lib/time'
import { useInteractionLock } from './Live'
import { CloseGlyph } from './CheatSheet'

/**
 * Dolt sync, in the top bar.
 *
 * A manager's question here is narrow: "is my issue history safe, and is it the
 * same as everyone else's?" So the resting state is three glyphs — a dirty dot,
 * ahead/behind arrows, and how long since the last commit — and the popover is
 * where push and pull live.
 *
 * Two rules the backend imposes and this component honours literally:
 *   · `ahead`/`behind` are *omitted*, not zero, when there is no remote or the
 *     tracking branch has never been fetched. Rendering "0↑ 0↓" for that would
 *     be a lie, so the arrows simply do not appear.
 *   · a conflict is a **409 that is never auto-resolved**. It renders loudly,
 *     with bd's own words, and offers no "resolve for me" button.
 */
export function DoltSync({ project, enabled }: { project: string; enabled: boolean }) {
  const [open, setOpen] = useState(false)
  const anchorRef = useRef<HTMLButtonElement>(null)
  const status = useDoltStatus(project, enabled)

  // Hidden entirely when the project is unreachable — there is no dolt server
  // to report on, and a greyed-out widget would just be clutter.
  if (!enabled || !status.data) return null

  const s = status.data
  const hasCounts = s.ahead !== undefined || s.behind !== undefined

  return (
    <>
      <button
        ref={anchorRef}
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="btn btn-quiet h-[26px] gap-1.5 px-2 text-[11px]"
        title={`dolt · ${s.branch}${s.remote ? ` · ${s.remote}` : ' · no remote'}`}
      >
        <span
          className="dot"
          data-status={s.dirty ? 'pinned' : 'closed'}
          data-filled={s.dirty}
          title={s.dirty ? 'Uncommitted changes in the working set' : 'Working set clean'}
        />
        <span className="mono">{s.branch}</span>

        {hasCounts && (
          <span className="mono flex items-center gap-1">
            {s.ahead !== undefined && s.ahead > 0 && (
              <span style={{ color: 'var(--accent)' }} title={`${s.ahead} commits to push`}>
                ↑{s.ahead}
              </span>
            )}
            {s.behind !== undefined && s.behind > 0 && (
              <span style={{ color: 'var(--warn)' }} title={`${s.behind} commits to pull`}>
                ↓{s.behind}
              </span>
            )}
            {s.ahead === 0 && s.behind === 0 && (
              <span className="text-faint" title="In sync with the remote">
                ⇅
              </span>
            )}
          </span>
        )}

        {s.lastCommit && (
          <span className="mono text-faint" title={absoluteTime(s.lastCommit.when)}>
            {relativeTime(s.lastCommit.when)}
          </span>
        )}
      </button>

      {open && (
        <SyncPopover
          project={project}
          status={s}
          anchor={anchorRef.current?.getBoundingClientRect()}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  )
}

type Outcome =
  | { kind: 'ok'; verb: string; output: string }
  | { kind: 'conflict'; verb: string; message: string; stderr: string }
  | { kind: 'error'; verb: string; message: string; stderr?: string }

function SyncPopover({
  project,
  status,
  anchor,
  onClose,
}: {
  project: string
  status: DoltStatus
  anchor: DOMRect | undefined
  onClose: () => void
}) {
  const qc = useQueryClient()
  const [outcome, setOutcome] = useState<Outcome | null>(null)
  const [expanded, setExpanded] = useState(false)

  // A push or pull rewrites the whole database; nothing should refetch under it.
  useInteractionLock(true)

  const sync = useMutation<DoltSyncResult, ApiError, 'push' | 'pull'>({
    mutationFn: (verb) => api.doltSync(project, verb),
    onSuccess: (result, verb) => {
      setOutcome({ kind: 'ok', verb, output: result.output })
      setExpanded(false)
      void qc.invalidateQueries({ queryKey: qk.dolt(project) })
      void qc.invalidateQueries({ queryKey: ['issues', project] })
      void qc.invalidateQueries({ queryKey: qk.projects })
    },
    onError: (err, verb) => {
      // 409 is its own thing: bd found a conflict and deliberately did not
      // resolve it. That deserves the loud treatment, not a generic failure.
      setOutcome(
        err.status === 409
          ? { kind: 'conflict', verb, message: err.message, stderr: err.stderr ?? '' }
          : { kind: 'error', verb, message: err.message, ...(err.stderr ? { stderr: err.stderr } : {}) },
      )
      setExpanded(true)
    },
  })

  const top = (anchor?.bottom ?? 48) + 6
  const left = Math.min(Math.max(8, (anchor?.right ?? 320) - 340), window.innerWidth - 348)

  return (
    <>
      <div
        className="fixed inset-0"
        style={{ zIndex: 'var(--z-modal)' }}
        onClick={onClose}
        role="presentation"
      />
      <div
        role="dialog"
        aria-label="Dolt sync"
        className="panel-in fixed w-[340px] overflow-hidden rounded-md bg-overlay"
        style={{ top, left, zIndex: 'var(--z-modal)', boxShadow: 'var(--shadow-overlay)' }}
      >
        <header className="flex items-center gap-2 border-b border-line px-3 py-2">
          <span className="legend">Dolt sync</span>
          <button type="button" className="icon-btn ml-auto" onClick={onClose} aria-label="Close">
            <CloseGlyph />
          </button>
        </header>

        <dl className="grid grid-cols-[64px_1fr] gap-x-3 gap-y-1.5 px-3 py-2.5 text-[11.5px]">
          <dt className="text-faint">branch</dt>
          <dd className="mono text-muted">{status.branch}</dd>

          <dt className="text-faint">remote</dt>
          <dd className={clsx('mono', status.remote ? 'text-muted' : 'text-faint')}>
            {status.remote ?? 'none configured'}
          </dd>

          {status.ahead !== undefined && (
            <>
              <dt className="text-faint">ahead</dt>
              <dd className="mono text-muted">{status.ahead}</dd>
            </>
          )}
          {status.behind !== undefined && (
            <>
              <dt className="text-faint">behind</dt>
              <dd className="mono text-muted">{status.behind}</dd>
            </>
          )}

          <dt className="text-faint">working set</dt>
          <dd className={status.dirty ? 'text-muted' : 'text-faint'}>
            {status.dirty ? 'uncommitted changes' : 'clean'}
          </dd>

          {status.lastCommit && (
            <>
              <dt className="text-faint">last commit</dt>
              <dd className="min-w-0">
                <p className="truncate text-muted" title={status.lastCommit.message}>
                  {status.lastCommit.message}
                </p>
                <p className="mono mt-0.5 text-[10.5px] text-faint">
                  {status.lastCommit.hash.slice(0, 8)} · {relativeTime(status.lastCommit.when)}
                </p>
              </dd>
            </>
          )}
        </dl>

        {!status.remote && (
          <p className="border-t border-line px-3 py-2 text-[11px] leading-relaxed text-faint">
            No remote configured, so push and pull have nothing to talk to. bd will say the
            same thing if you try.
          </p>
        )}

        <div className="flex items-center gap-2 border-t border-line px-3 py-2">
          {(['pull', 'push'] as const).map((verb) => (
            <button
              key={verb}
              type="button"
              className="btn h-6 text-[11.5px]"
              disabled={sync.isPending}
              onClick={() => sync.mutate(verb)}
            >
              {sync.isPending && sync.variables === verb ? (
                <>
                  <Spinner /> {verb}ing…
                </>
              ) : (
                verb
              )}
            </button>
          ))}
        </div>

        {outcome && (
          <div
            className="border-t border-line px-3 py-2"
            data-tone={outcome.kind === 'ok' ? 'accent' : outcome.kind === 'conflict' ? 'danger' : 'warn'}
            style={
              outcome.kind === 'conflict'
                ? { background: 'color-mix(in oklab, var(--danger) 10%, transparent)' }
                : undefined
            }
          >
            <p className="flex items-center gap-2">
              <span className="chip" data-solid="true">
                {outcome.kind === 'ok' ? outcome.verb : outcome.kind}
              </span>
              <span className="min-w-0 flex-1 truncate text-[11.5px] text-ink">
                {outcome.kind === 'ok' ? `dolt ${outcome.verb} finished` : outcome.message}
              </span>
              <button
                type="button"
                className="shrink-0 text-[10.5px] text-faint hover:text-muted"
                onClick={() => setExpanded((v) => !v)}
              >
                {expanded ? 'hide' : 'output'}
              </button>
            </p>

            {outcome.kind === 'conflict' && (
              <p className="mt-1.5 text-[11px] leading-relaxed text-muted">
                Loom will not merge someone else's issue history for you. Resolve it in the
                workdir, then sync again.
              </p>
            )}

            {expanded && (
              <pre className="mono mt-2 max-h-40 overflow-auto rounded-sm bg-sunken px-2 py-1.5 text-[10.5px] leading-[1.5] whitespace-pre-wrap text-muted">
                {outcome.kind === 'ok'
                  ? outcome.output || '(no output)'
                  : (outcome.stderr ?? outcome.message)}
              </pre>
            )}
          </div>
        )}
      </div>
    </>
  )
}

function Spinner() {
  return (
    <svg viewBox="0 0 12 12" width="11" height="11" aria-hidden="true" className="animate-spin">
      <circle cx="6" cy="6" r="4.2" fill="none" stroke="currentColor" strokeWidth="1.4" opacity="0.25" />
      <path d="M6 1.8a4.2 4.2 0 0 1 4.2 4.2" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  )
}
