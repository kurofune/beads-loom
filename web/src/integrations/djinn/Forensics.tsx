import { useState } from 'react'
import clsx from 'clsx'
import { useQuery } from '@tanstack/react-query'
import { api, ApiError } from '@/api/client'
import { djinnKeys } from './live'
import type { Forensics as ForensicsData } from '@/api/types'
import { stepProgress } from './runs'
import { relativeTime, absoluteTime } from '@/lib/time'

/**
 * A bead's forensics: what djinn actually did to it, read from
 * `.djinn/state/<bead>/`.
 *
 * The chips are the formula — ac-quality → implement → reviews → verify →
 * commit-push — laid out in full even where a bead never reached a step. A
 * missing chip would read as "this step does not exist"; a greyed one reads as
 * "it never got there", which is the fact the manager is after.
 */
export function Forensics({
  project,
  bead,
  onOpenIssue,
}: {
  project: string
  bead: string
  onOpenIssue?: (id: string) => void
}) {
  const { data, isLoading, error } = useQuery<ForensicsData, ApiError>({
    queryKey: djinnKeys.forensics(project, bead),
    queryFn: () => api.forensics(project, bead),
    staleTime: 10_000,
  })

  if (isLoading) {
    return <p className="px-3 py-2 text-[11.5px] text-faint">Reading .djinn/state/{bead}…</p>
  }

  if (error) {
    return (
      <p className="px-3 py-2 text-[11.5px] text-faint">
        No state directory for <span className="mono">{bead}</span> — djinn writes one when a
        worker claims the bead.
      </p>
    )
  }

  if (!data) return null
  const steps = stepProgress(data.timeline)
  const latest = data.timeline.at(-1)

  return (
    <div className="space-y-3 px-3 py-2.5">
      <div className="flex flex-wrap items-center gap-1">
        {steps.map((step, i) => (
          <span key={step.step} className="flex items-center gap-1">
            {i > 0 && (
              <span className="text-faint" aria-hidden="true">
                ›
              </span>
            )}
            <span
              className={clsx('chip')}
              data-tone={step.reached ? 'accent' : 'neutral'}
              data-outline={step.reached ? undefined : 'true'}
              title={
                step.reached
                  ? `${step.label} — ${step.iter} iteration${step.iter === 1 ? '' : 's'}`
                  : `${step.label} — never reached`
              }
              style={step.reached ? undefined : { opacity: 0.55 }}
            >
              {step.label}
              {step.reached && step.iter > 1 && (
                <span className="mono ml-0.5">×{step.iter}</span>
              )}
            </span>
          </span>
        ))}
      </div>

      <dl className="grid grid-cols-[86px_1fr] gap-x-3 gap-y-1 text-[11px]">
        {data.claimSha && (
          <>
            <dt className="text-faint">claim</dt>
            <dd className="mono truncate text-muted" title={data.claimSha}>
              {data.claimSha.slice(0, 12)}
            </dd>
          </>
        )}
        {latest && (
          <>
            <dt className="text-faint">last step</dt>
            <dd className="text-muted">
              {latest.step} iter {latest.iter}
              <span className="mono ml-2 text-faint" title={absoluteTime(latest.mtime)}>
                {relativeTime(latest.mtime)}
              </span>
            </dd>
          </>
        )}
        <dt className="text-faint">issue</dt>
        <dd>
          <button
            type="button"
            className="mono text-muted underline-offset-2 hover:text-ink hover:underline"
            onClick={() => onOpenIssue?.(bead)}
          >
            {bead} →
          </button>
        </dd>
      </dl>

      {data.verdicts.length > 0 && <VerdictList verdicts={data.verdicts} />}
    </div>
  )
}

function VerdictList({ verdicts }: { verdicts: ForensicsData['verdicts'] }) {
  const [open, setOpen] = useState<string | null>(null)

  return (
    <div>
      <p className="legend mb-1">Procedure output · {verdicts.length}</p>
      <ul className="space-y-0.5">
        {verdicts.map((v) => {
          const key = `${v.loop}-${v.iter}-${v.procedure}`
          const expanded = open === key
          // Real state dirs contain both JSON and free text, so both are
          // rendered rather than one being assumed.
          const body = v.json !== undefined ? JSON.stringify(v.json, null, 2) : (v.text ?? '')
          return (
            <li key={key}>
              <button
                type="button"
                onClick={() => setOpen(expanded ? null : key)}
                className="flex w-full items-center gap-2 rounded-sm px-1 py-1 text-left hover:bg-hover"
              >
                <span className="mono text-[10.5px] text-faint">
                  {v.loop} · iter {v.iter}
                </span>
                <span className="min-w-0 flex-1 truncate text-[11.5px] text-muted">
                  {v.procedure}
                </span>
                {v.truncated && (
                  <span className="chip" data-tone="warn" data-outline="true">
                    truncated
                  </span>
                )}
                <span className="text-[10px] text-faint">{expanded ? 'hide' : 'show'}</span>
              </button>
              {expanded && (
                <pre className="mono mt-1 max-h-52 overflow-auto rounded-sm bg-sunken px-2 py-1.5 text-[10.5px] leading-[1.5] whitespace-pre-wrap text-muted">
                  {body || '(empty)'}
                </pre>
              )}
            </li>
          )
        })}
      </ul>
    </div>
  )
}
