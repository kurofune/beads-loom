import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from '@/api/client'
import { djinnKeys } from './live'
import type { Project, Run, RunBead, RunDetail } from '@/api/types'
import { TopBar } from '@/components/TopBar'
import { RunLog } from './RunLog'
import { Forensics } from './Forensics'
import { BacklogSkeleton, EmptyState, ErrorState, UnreachableState } from '@/components/states'
import { useToast } from '@/components/Toast'
import { useInteractionLock } from '@/components/Live'
import { useRunEvents } from './live'
import { Dialog } from '@/components/Dialog'
import { relativeTime, absoluteTime } from '@/lib/time'
import { useShortcuts } from '@/keyboard/useShortcuts'
import {
  costGuard,
  formatUsd,
  isLive,
  runDuration,
  runTotals,
  RUN_STATE_LABEL,
  RUN_STATE_TOKEN,
  stepLabel,
  verdictTone,
} from './runs'

/**
 * Runs.
 *
 * Mission control, not analytics: the questions are "what is running right
 * now", "how much has it spent", and "what happened to that bead" — so the
 * layout puts worker slots and the cost guardrail above the fold and keeps the
 * log tailing beside them. There are no charts, because nothing here is a
 * trend; it is all current state.
 */
export function RunsView({ projects }: { projects: Project[] }) {
  const params = useParams<{ project: string; runId?: string }>()
  const navigate = useNavigate()
  const projectName = params.project ?? ''
  const project = projects.find((p) => p.name === projectName)

  const reachable = project?.reachable ?? true

  const runs = useQuery<{ runs: Run[] }, ApiError>({
    queryKey: djinnKeys.runs(projectName),
    queryFn: () => api.runs(projectName),
    enabled: Boolean(projectName) && reachable,
    staleTime: 10_000,
  })

  useShortcuts({
    'view.backlog': () => navigate(`/${encodeURIComponent(projectName)}/backlog`),
    'view.board': () => navigate(`/${encodeURIComponent(projectName)}/board`),
    'view.graph': () => navigate(`/${encodeURIComponent(projectName)}/graph`),
    'view.now': () => navigate('/now'),
    'issue.close': () => params.runId && navigate(`/${encodeURIComponent(projectName)}/runs`),
  })

  const body = () => {
    if (project && !project.reachable) {
      return <UnreachableState project={project.name} reason={project.reason} workdir={project.workdir} />
    }
    if (runs.isLoading) return <BacklogSkeleton />
    if (runs.error) return <ErrorState error={runs.error} onRetry={() => void runs.refetch()} />

    if (params.runId) {
      return (
        <RunDetailPane
          project={projectName}
          runId={params.runId}
          onBack={() => navigate(`/${encodeURIComponent(projectName)}/runs`)}
          onOpenIssue={(id) => navigate(`/${encodeURIComponent(projectName)}/issue/${encodeURIComponent(id)}`)}
        />
      )
    }

    const list = runs.data?.runs ?? []
    if (list.length === 0) {
      return (
        <EmptyState
          title="No runs yet"
          detail={
            <>
              Loom shows runs it started <em>and</em> runs reconstructed from djinn's own logs, so
              this fills in as soon as anyone summons in{' '}
              <span className="mono">{projectName}</span>.
            </>
          }
        />
      )
    }

    return (
      <div className="h-full overflow-y-auto px-4 py-3">
        <div className="space-y-1.5">
          {list.map((run) => (
            <RunCard
              key={run.id}
              run={run}
              onOpen={() =>
                navigate(`/${encodeURIComponent(projectName)}/runs/${encodeURIComponent(run.id)}`)
              }
            />
          ))}
        </div>
      </div>
    )
  }

  return (
    <div className="flex h-full min-w-0 flex-1 flex-col">
      <TopBar project={project} current="runs" />
      <div className="min-h-0 flex-1">{body()}</div>
    </div>
  )
}

/* --- history ------------------------------------------------------------- */

function RunCard({ run, onOpen }: { run: Run; onOpen: () => void }) {
  const totals = runTotals(run.beads)
  const live = isLive(run.state)

  return (
    <button
      type="button"
      onClick={onOpen}
      className="group flex w-full items-center gap-3 rounded-md bg-raised px-3 py-2.5 text-left ring-1 ring-line transition-colors duration-[--dur-fast] hover:bg-hover"
    >
      <span className="dot" data-status={RUN_STATE_TOKEN[run.state]} data-filled={live} />

      <span className="min-w-0 flex-1">
        <span className="flex items-baseline gap-2">
          <span className="mono truncate text-[12px] text-ink">{run.id}</span>
          {/* Source is the first thing to know: Loom cannot stop, re-run or
              show the console of something it never started. */}
          <span
            className="chip"
            data-tone={run.source === 'loom' ? 'accent' : 'neutral'}
            data-outline={run.source === 'external' ? 'true' : undefined}
            title={
              run.source === 'loom'
                ? 'Started by Loom — full control and captured console output'
                : "Started at a terminal; reconstructed from djinn's own logs"
            }
          >
            {run.source}
          </span>
          <span className="text-[11.5px] text-muted">{RUN_STATE_LABEL[run.state]}</span>
        </span>

        <span className="mono mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[10.5px] text-faint">
          <span title={absoluteTime(run.startedAt)}>{relativeTime(run.startedAt)}</span>
          <span>{runDuration(run)}</span>
          <span style={{ color: totals.done > 0 ? 'var(--ok)' : undefined }}>
            {totals.done} done
          </span>
          {totals.failed > 0 && <span style={{ color: 'var(--danger)' }}>{totals.failed} failed</span>}
          {totals.active > 0 && (
            <span style={{ color: 'var(--status-in-progress)' }}>{totals.active} in flight</span>
          )}
          {totals.retried > 0 && <span>{totals.retried} retried</span>}
          <span className="text-muted">{formatUsd(run.summary?.totalCostUsd ?? run.costSoFar)}</span>
        </span>

        {run.summary?.halt && (
          <span
            className="mt-1 block truncate text-[11px]"
            style={{ color: 'var(--warn)' }}
            title={run.summary.halt}
          >
            halt: {run.summary.halt}
          </span>
        )}
        {run.exitError && (
          <span className="mt-1 block truncate text-[11px]" style={{ color: 'var(--danger)' }}>
            {run.exitError}
          </span>
        )}
      </span>

      <span className="shrink-0 text-[11px] text-faint group-hover:text-muted">open →</span>
    </button>
  )
}

/* --- live dashboard ------------------------------------------------------ */

function RunDetailPane({
  project,
  runId,
  onBack,
  onOpenIssue,
}: {
  project: string
  runId: string
  onBack: () => void
  onOpenIssue: (id: string) => void
}) {
  const qc = useQueryClient()
  const toast = useToast()
  const [confirmStop, setConfirmStop] = useState(false)
  const { logLines } = useRunEvents(runId)

  const run = useQuery<RunDetail, ApiError>({
    queryKey: djinnKeys.run(project, runId),
    queryFn: () => api.run(project, runId),
    staleTime: 2_000,
  })

  const stop = useMutation<void, ApiError, void>({
    mutationFn: () => api.stopRun(project, runId),
    onSuccess: () => {
      toast.push({ tone: 'ok', title: 'Stop signal sent', detail: 'Workers finish their current step, then exit.' })
      void qc.invalidateQueries({ queryKey: djinnKeys.run(project, runId) })
    },
    onError: (err) => toast.error('Could not stop the run', err),
  })

  useInteractionLock(confirmStop)

  const data = run.data
  const live = data ? isLive(data.state) : false

  // While a run is live the record moves constantly; SSE run-updated events
  // invalidate it, and this is the belt to that braces for external runs, whose
  // changes arrive only through the log watcher.
  useEffect(() => {
    if (!live) return
    const t = window.setInterval(
      () => void qc.invalidateQueries({ queryKey: djinnKeys.run(project, runId) }),
      5_000,
    )
    return () => window.clearInterval(t)
  }, [live, project, qc, runId])

  if (run.isLoading) return <BacklogSkeleton />
  if (run.error) return <ErrorState error={run.error} onRetry={() => void run.refetch()} />
  if (!data) return null

  const totals = runTotals(data.beads)
  const guard = costGuard(data.summary?.totalCostUsd ?? data.costSoFar, data.caps?.maxCostUsdPerSession)

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex shrink-0 items-center gap-3 border-b border-line px-4 py-2.5">
        <button type="button" className="btn btn-quiet h-6 text-[11.5px]" onClick={onBack}>
          ← Runs
        </button>
        <span className="dot" data-status={RUN_STATE_TOKEN[data.state]} data-filled={live} />
        <span className="mono text-[12.5px] text-ink">{data.id}</span>
        <span className="chip" data-tone={data.source === 'loom' ? 'accent' : 'neutral'} data-outline={data.source === 'external' ? 'true' : undefined}>
          {data.source}
        </span>
        <span className="text-[11.5px] text-muted">{RUN_STATE_LABEL[data.state]}</span>
        <span className="mono text-[10.5px] text-faint" title={absoluteTime(data.startedAt)}>
          {runDuration(data)}
        </span>

        {live && data.source === 'loom' && (
          <button
            type="button"
            className="btn btn-quiet ml-auto h-6 text-[11.5px]"
            onClick={() => setConfirmStop(true)}
            disabled={stop.isPending || data.state === 'stopping'}
            style={{ color: 'var(--danger)' }}
          >
            {data.state === 'stopping' ? 'Stopping…' : 'Stop run'}
          </button>
        )}
        {live && data.source === 'external' && (
          <span className="ml-auto text-[11px] text-faint" title="Loom did not start this run">
            external — Loom cannot stop it
          </span>
        )}
      </header>

      <div className="grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)_minmax(0,420px)] gap-3 overflow-hidden p-3">
        <div className="flex min-h-0 flex-col gap-3 overflow-y-auto">
          <CostBar guard={guard} caps={data.caps} />

          <section>
            <p className="legend mb-1.5">
              Worker slots · {data.activeSlots.length} active of {totals.total} bead
              {totals.total === 1 ? '' : 's'}
            </p>
            {data.activeSlots.length === 0 ? (
              <p className="rounded-md bg-sunken px-3 py-3 text-[11.5px] text-faint ring-1 ring-line">
                No workers in flight.
              </p>
            ) : (
              <div className="grid gap-1.5 md:grid-cols-2">
                {data.activeSlots.map((slot) => (
                  <div key={slot.bead} className="rounded-md bg-raised px-2.5 py-2 ring-1 ring-line">
                    <p className="flex items-center gap-2">
                      <span className="dot animate-pulse" data-status="in_progress" data-filled="true" />
                      <span className="mono truncate text-[11.5px] text-ink">{slot.bead}</span>
                      <span className="mono ml-auto text-[10px] text-faint">
                        {relativeTime(slot.spawnedAt)}
                      </span>
                    </p>
                    <p className="mt-1.5 flex items-center gap-1.5">
                      {slot.currentStep ? (
                        <>
                          <span className="chip" data-tone="accent">
                            {stepLabel(slot.currentStep.step)}
                          </span>
                          <span className="mono text-[10px] text-faint">
                            iter {slot.currentStep.iter}
                            {slot.currentStep.procedure && ` · ${slot.currentStep.procedure}`}
                          </span>
                        </>
                      ) : (
                        <span className="text-[10.5px] text-faint">starting — no state written yet</span>
                      )}
                    </p>
                  </div>
                ))}
              </div>
            )}
          </section>

          <section>
            <p className="legend mb-1.5">
              Beads · {totals.done} done · {totals.failed} failed
              {totals.retried > 0 && ` · ${totals.retried} retried`}
            </p>
            <ul className="space-y-0.5">
              {data.beads.map((bead) => (
                <BeadRow key={bead.id} project={project} bead={bead} onOpenIssue={onOpenIssue} />
              ))}
            </ul>
          </section>

          {data.summary && <SummaryCard summary={data.summary} />}
        </div>

        <RunLog project={project} runId={runId} source={data.source} incoming={logLines} live={live} />
      </div>

      {confirmStop && (
        <Dialog
          title="Stop this run?"
          subtitle="Workers finish the step they are on, then exit."
          onClose={() => setConfirmStop(false)}
          footer={
            <>
              <button type="button" className="btn" data-secondary onClick={() => setConfirmStop(false)}>
                Keep running
              </button>
              <button
                type="button"
                className="btn"
                style={{ background: 'var(--danger)', color: 'var(--text-inverted)', boxShadow: 'none' }}
                onClick={() => {
                  setConfirmStop(false)
                  stop.mutate()
                }}
              >
                Stop run
              </button>
            </>
          }
        >
          <p className="text-[12px] leading-relaxed text-muted">
            This sends SIGTERM and opens a grace period. Work already committed stays committed;
            a bead mid-step will be left wherever the worker got to, which is what the forensics
            timeline is for.
          </p>
        </Dialog>
      )}
    </div>
  )
}

function CostBar({ guard, caps }: { guard: ReturnType<typeof costGuard>; caps?: RunDetail['caps'] }) {
  const tone = guard.level === 'danger' ? 'danger' : guard.level === 'warn' ? 'warn' : 'accent'

  return (
    <section className="rounded-md bg-raised px-3 py-2.5 ring-1 ring-line" data-tone={tone}>
      <p className="flex items-baseline gap-2">
        <span className="legend">Spend</span>
        <span className="mono text-[13px] text-ink">{formatUsd(guard.spent)}</span>
        <span className="mono ml-auto text-[10.5px] text-faint">{guard.label}</span>
      </p>

      {guard.cap === null ? (
        // No cap in .djinn/config.json — the common case. A full bar here would
        // be a fabrication, so there is no bar.
        <p className="mt-1.5 text-[10.5px] leading-relaxed text-faint">
          No session cap configured. Set{' '}
          <span className="mono">djinn.max_cost_usd_per_session</span> in{' '}
          <span className="mono">.djinn/config.json</span> to chart against one.
        </p>
      ) : (
        <>
          <span className="mt-2 block h-1.5 overflow-hidden rounded-full bg-active">
            <span
              className="block h-full transition-[width] duration-[--dur-base]"
              style={{ width: `${guard.fraction * 100}%`, background: 'var(--c)' }}
            />
          </span>
          {guard.level !== 'ok' && (
            <p className="mt-1.5 text-[10.5px]" style={{ color: 'var(--c)' }}>
              {guard.level === 'danger'
                ? 'Past 90% of the session cap — summoner will stop dispatching soon.'
                : 'Past 70% of the session cap.'}
            </p>
          )}
        </>
      )}

      {caps?.maxCostUsdPerBead != null && (
        <p className="mono mt-1 text-[10px] text-faint">
          per-bead cap {formatUsd(caps.maxCostUsdPerBead)}
        </p>
      )}
    </section>
  )
}

function BeadRow({
  project,
  bead,
  onOpenIssue,
}: {
  project: string
  bead: RunBead
  onOpenIssue: (id: string) => void
}) {
  const [open, setOpen] = useState(false)
  const active = bead.exitedAt === null

  return (
    <li className="overflow-hidden rounded-md bg-raised ring-1 ring-line">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-2.5 py-1.5 text-left hover:bg-hover"
        aria-expanded={open}
      >
        <span className="text-[10px] text-faint">{open ? '▾' : '▸'}</span>
        <span className="mono w-[104px] shrink-0 truncate text-[11px] text-muted">{bead.id}</span>

        {active ? (
          <span className="chip" data-tone="accent">
            in flight
          </span>
        ) : (
          <span className="chip" data-tone={verdictTone(bead.verdict)} data-solid="true">
            {bead.verdict ?? 'exited'}
          </span>
        )}

        <span className="min-w-0 flex-1 truncate text-[11.5px] text-faint" title={bead.finalStatus}>
          {bead.finalStatus}
        </span>

        {bead.retries > 0 && (
          <span className="chip shrink-0" data-tone="warn" data-outline="true" title={`${bead.retries} restart(s)`}>
            ×{bead.retries + 1}
          </span>
        )}
        <span className="mono shrink-0 text-[10px] text-faint">
          {relativeTime(bead.exitedAt ?? bead.spawnedAt)}
        </span>
      </button>

      {open && <Forensics project={project} bead={bead.id} onOpenIssue={onOpenIssue} />}
    </li>
  )
}

function SummaryCard({ summary }: { summary: NonNullable<Run['summary']> }) {
  const facts: [string, string][] = [
    ['iterations', String(summary.iterations)],
    ['closed', String(summary.closed)],
    ['failures', String(summary.failures)],
    ['total cost', formatUsd(summary.totalCostUsd)],
    ['peak context', `${summary.peakContext}%`],
    ['ran', summary.ran || '—'],
    ['follow-ups filed', String(summary.filedFollowUps)],
  ]

  return (
    <section className="rounded-md bg-raised px-3 py-2.5 ring-1 ring-line">
      <p className="legend mb-2">Session summary</p>
      <dl className="grid grid-cols-2 gap-x-6 gap-y-1 text-[11.5px] md:grid-cols-3">
        {facts.map(([term, value]) => (
          <div key={term} className="flex items-baseline gap-2">
            <dt className="text-faint">{term}</dt>
            <dd className="mono ml-auto text-muted">{value}</dd>
          </div>
        ))}
      </dl>

      {summary.halt && (
        <p className="mt-2 rounded-sm px-2 py-1.5 text-[11px] leading-relaxed" style={{ background: 'color-mix(in oklab, var(--warn) 12%, transparent)', color: 'var(--warn)' }}>
          halt: <span className="text-muted">{summary.halt}</span>
        </p>
      )}
      {summary.gateFailures.length > 0 && (
        <p className="mt-1.5 text-[11px]" style={{ color: 'var(--danger)' }}>
          gate failures: <span className="mono">{summary.gateFailures.join(', ')}</span>
        </p>
      )}
      {summary.sandbagging && (
        <p className="mono mt-1.5 text-[10.5px] text-faint">sandbagging: {summary.sandbagging}</p>
      )}

      {summary.dispatched.length > 0 && (
        <ul className="mt-2 flex flex-wrap gap-1">
          {summary.dispatched.map((d) => (
            <li key={d.bead}>
              <span className="chip" data-tone={verdictTone(d.verdict)}>
                <span className="mono">{d.bead}</span> {d.verdict}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

/** Used by the sidebar/Now view to know whether a project has a live run. */
export function useHasActiveRun(project: string, enabled: boolean): boolean {
  const runs = useQuery<{ runs: Run[] }, ApiError>({
    queryKey: djinnKeys.runs(project),
    queryFn: () => api.runs(project),
    enabled: enabled && Boolean(project),
    staleTime: 15_000,
  })
  return useMemo(() => (runs.data?.runs ?? []).some((r) => isLive(r.state)), [runs.data])
}

export const runsRouteFor = (project: string, runId?: string): string =>
  runId
    ? `/${encodeURIComponent(project)}/runs/${encodeURIComponent(runId)}`
    : `/${encodeURIComponent(project)}/runs`
