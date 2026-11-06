import { useMemo, useState } from 'react'
import { useParams } from 'react-router-dom'
import { ApiError } from '@/api/client'
import { useSweep } from '@/api/hooks'
import {
  useApplyFinding,
  useDismissFinding,
  useUndismissFinding,
} from '@/api/mutations'
import type { Project, SweepFinding, SweepPartial } from '@/api/types'
import { Dialog } from '@/components/Dialog'
import { useInteractionLock } from '@/components/Live'
import { TopBar } from '@/components/TopBar'
import { useToast } from '@/components/Toast'
import { EmptyState, UnreachableState } from '@/components/states'

const KIND_LABELS: Record<string, string> = {
  'dangling-in-progress': 'Dangling in progress',
  'untracked-commit': 'Untracked commits',
  'empty-ac-close': 'Closed without acceptance',
  'empty-reason-close': 'Closed without a reason',
}

function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? kind
}

/** JSON array notation preserves every argv boundary, including spaces. */
export function formatSuggestion(finding: SweepFinding): string {
  return finding.suggested.steps.map((step) => JSON.stringify(step)).join('\n')
}

export function SweepView({ projects }: { projects: Project[] }) {
  const { project: projectName = '' } = useParams<{ project: string }>()
  const project = projects.find((candidate) => candidate.name === projectName)
  const query = useSweep(projectName)
  const apply = useApplyFinding(projectName)
  const dismiss = useDismissFinding(projectName)
  const undismiss = useUndismissFinding(projectName)
  const toast = useToast()

  const [kind, setKind] = useState('all')
  const [confirming, setConfirming] = useState<SweepFinding | null>(null)
  const [applyError, setApplyError] = useState<ApiError | null>(null)
  const [copied, setCopied] = useState<string | null>(null)

  useInteractionLock(confirming !== null)

  const active = query.data?.findings.filter((finding) => !finding.dismissed) ?? []
  const availableKinds = useMemo(
    () => [...new Set(active.map((finding) => finding.kind))],
    [active],
  )
  const visible = kind === 'all' ? active : active.filter((finding) => finding.kind === kind)
  const groups = useMemo(() => {
    const grouped = new Map<string, SweepFinding[]>()
    for (const finding of visible) {
      grouped.set(finding.kind, [...(grouped.get(finding.kind) ?? []), finding])
    }
    return [...grouped]
  }, [visible])
  const dismissedFindings = query.data?.findings.filter((finding) => finding.dismissed) ?? []

  const runSweep = () => {
    setKind('all')
    void query.refetch()
  }

  const copy = async (finding: SweepFinding) => {
    try {
      await navigator.clipboard.writeText(formatSuggestion(finding))
      setCopied(finding.fingerprint)
      window.setTimeout(() => setCopied(null), 1400)
    } catch (error) {
      toast.error('Could not copy the argv template', error)
    }
  }

  const dismissFinding = async (finding: SweepFinding) => {
    try {
      await dismiss.mutateAsync({
        fingerprint: finding.fingerprint,
        materialDigest: finding.materialDigest,
        note: '',
        window: query.data?.window,
        branch: query.data?.branch,
      })
      toast.push({ tone: 'info', title: `Dismissed ${finding.title}` })
    } catch (error) {
      toast.error('Could not dismiss the finding', error)
    }
  }

  const restoreFinding = async (finding: SweepFinding) => {
    try {
      await undismiss.mutateAsync({ fingerprint: finding.fingerprint })
      toast.push({ tone: 'ok', title: `Restored ${finding.title}` })
    } catch (error) {
      toast.error('Could not restore the finding', error)
    }
  }

  const applyFinding = async () => {
    if (!confirming || apply.isPending) return
    setApplyError(null)
    try {
      await apply.mutateAsync({
        fingerprint: confirming.fingerprint,
        materialDigest: confirming.materialDigest,
        window: query.data?.window,
        branch: query.data?.branch,
      })
      toast.push({ tone: 'ok', title: `Applied ${confirming.title}` })
      setConfirming(null)
    } catch (error) {
      setApplyError(
        error instanceof ApiError ? error : new ApiError('The finding could not be applied', 0, { cause: error }),
      )
    }
  }

  if (!project) {
    return <EmptyState title="Project not found" detail={<span className="mono">{projectName}</span>} />
  }

  const body = () => {
    if (!project.reachable) {
      return (
        <UnreachableState
          project={project.name}
          reason={project.reason}
          workdir={project.workdir}
        />
      )
    }

    if (!query.isFetched && !query.isFetching) {
      return (
        <EmptyState
          title="Check whether git and the tracker still agree"
          detail="A sweep reads the project’s default-branch history and bead state, then queues mechanical discrepancies for review. It does not write anything."
          action={
            <button type="button" className="btn" onClick={runSweep}>
              Sweep project
            </button>
          }
        />
      )
    }

    if (query.isFetching) {
      return (
        <div
          className="flex h-full min-h-[280px] flex-col items-center justify-center gap-4 px-8 text-center"
          aria-busy="true"
          aria-label="Sweep in progress"
        >
          <span className="relative flex h-10 w-10 items-center justify-center">
            <span className="absolute inset-0 animate-ping rounded-full border border-accent opacity-25" />
            <span className="h-2.5 w-2.5 animate-pulse rounded-full bg-accent" />
          </span>
          <div className="space-y-1.5">
            <p className="text-[13px] font-medium text-ink">Sweeping git and tracker state</p>
            <p className="text-[12px] text-faint">Reading up to 500 non-merge commits. No writes are running.</p>
          </div>
        </div>
      )
    }

    if (query.isError) {
      const error = query.error
      return (
        <div className="flex h-full min-h-[280px] flex-col items-center justify-center gap-4 px-8 text-center">
          <span className="chip" data-tone="danger" data-solid="true">sweep failed</span>
          <div className="w-full max-w-[680px] space-y-2">
            <p className="text-[13px] font-medium text-ink">Git or bd could not complete the sweep</p>
            <pre className="max-h-56 overflow-auto whitespace-pre-wrap rounded-md border border-line bg-sunken p-3 text-left text-[11px] leading-relaxed text-muted">{error.stderr ?? error.message}</pre>
          </div>
          <button type="button" className="btn" onClick={runSweep}>Try again</button>
        </div>
      )
    }

    if (!query.data) return null

    return (
      <div className="h-full overflow-y-auto px-4 py-3">
        {query.data.partial.length > 0 && <PartialStrip partial={query.data.partial} />}

        {active.length === 0 ? (
          <EmptyState
            title="Git and the tracker are reconciled"
            detail={
              <>
                All four mechanical checks were clear at{' '}
                <time className="mono text-muted" dateTime={query.data.sweptAt}>
                  {query.data.sweptAt}
                </time>
                .
              </>
            }
            action={<button type="button" className="btn btn-quiet" onClick={runSweep}>Sweep again</button>}
          />
        ) : (
          <>
            <section className="mb-3 flex flex-wrap items-center gap-1.5" aria-label="Finding filters">
              <button
                type="button"
                className="chip"
                data-tone={kind === 'all' ? 'accent' : 'neutral'}
                data-solid={kind === 'all' ? 'true' : undefined}
                data-outline={kind === 'all' ? undefined : 'true'}
                onClick={() => setKind('all')}
              >
                All · {active.length}
              </button>
              {availableKinds.map((candidate) => (
                <button
                  key={candidate}
                  type="button"
                  className="chip"
                  data-tone={kind === candidate ? 'accent' : 'neutral'}
                  data-solid={kind === candidate ? 'true' : undefined}
                  data-outline={kind === candidate ? undefined : 'true'}
                  onClick={() => setKind(candidate)}
                >
                  {kindLabel(candidate)} · {query.data!.counts[candidate] ?? 0}
                </button>
              ))}
              <span className="mono ml-auto text-[10.5px] text-faint">
                {query.data.branch} · {query.data.commitsScanned}/{query.data.window} commits
              </span>
            </section>

            <div className="space-y-4">
              {groups.map(([groupKind, findings]) => (
                <section key={groupKind} aria-labelledby={`sweep-${groupKind}`}>
                  <header className="sticky top-0 z-10 flex items-center gap-2 border-y border-line bg-surface/95 px-2 py-1.5 backdrop-blur-sm">
                    <h2 id={`sweep-${groupKind}`} className="legend text-muted">{kindLabel(groupKind)}</h2>
                    <span className="mono text-[10px] text-faint">{findings.length}</span>
                  </header>
                  <div className="divide-y divide-line border-b border-line">
                    {findings.map((finding) => (
                      <FindingRow
                        key={finding.fingerprint}
                        finding={finding}
                        copied={copied === finding.fingerprint}
                        busy={dismiss.isPending}
                        onCopy={() => void copy(finding)}
                        onApply={() => {
                          setApplyError(null)
                          setConfirming(finding)
                        }}
                        onDismiss={() => void dismissFinding(finding)}
                        onRestore={() => void restoreFinding(finding)}
                      />
                    ))}
                  </div>
                </section>
              ))}
            </div>
          </>
        )}

        {dismissedFindings.length > 0 && (
          <section className="mt-6 border-t border-line pt-3">
            <h2 className="legend mb-2">Dismissed this report · {dismissedFindings.length}</h2>
            <div className="space-y-1">
              {dismissedFindings.map((finding) => (
                <div key={finding.fingerprint} className="flex items-center gap-3 rounded-md bg-sunken px-3 py-2 opacity-70">
                  <span className="min-w-0 flex-1 truncate text-[11.5px] text-muted">{finding.title}</span>
                  <button type="button" className="btn btn-quiet h-6" onClick={() => void restoreFinding(finding)}>
                    Restore
                  </button>
                </div>
              ))}
            </div>
          </section>
        )}
      </div>
    )
  }

  return (
    <div className="flex h-full min-w-0 flex-1 flex-col">
      <TopBar project={project} current="sweep">
        <button
          type="button"
          className="btn h-7"
          disabled={query.isFetching || !project.reachable}
          onClick={runSweep}
        >
          {query.isFetching ? 'Sweeping…' : query.isFetched ? 'Sweep again' : 'Sweep'}
        </button>
      </TopBar>

      <div className="min-h-0 flex-1">{body()}</div>

      {confirming && (
        <Dialog
          title="Apply this finding?"
          subtitle="Loom re-runs the sweep before writing and refuses the action if the evidence changed."
          onClose={() => {
            if (!apply.isPending) setConfirming(null)
          }}
          width={680}
          footer={
            <>
              <button
                type="button"
                className="btn btn-quiet"
                data-secondary
                disabled={apply.isPending}
                onClick={() => setConfirming(null)}
              >
                Cancel
              </button>
              <button type="button" className="btn" disabled={apply.isPending} onClick={() => void applyFinding()}>
                {apply.isPending ? 'Applying…' : 'Run this argv'}
              </button>
            </>
          }
        >
          <p className="mb-2 text-[11.5px] leading-relaxed text-faint">Exact argv about to run:</p>
          <pre className="overflow-x-auto whitespace-pre rounded-md border border-line bg-sunken p-3 text-[11px] leading-relaxed text-muted">{formatSuggestion(confirming)}</pre>
          {applyError && (
            <div className="mt-3 rounded-md border border-[color:var(--status-blocked)]/35 bg-sunken p-3">
              <p className="mb-1.5 text-[11.5px] font-medium text-ink">bd refused the write</p>
              {applyError.status === 422 && applyError.stderr ? (
                <pre className="overflow-auto whitespace-pre-wrap text-[11px] leading-relaxed text-muted">{applyError.stderr}</pre>
              ) : (
                <p className="text-[11px] leading-relaxed text-muted">{applyError.message}</p>
              )}
            </div>
          )}
        </Dialog>
      )}
    </div>
  )
}

function PartialStrip({ partial }: { partial: SweepPartial[] }) {
  return (
    <aside className="mb-3 rounded-md border border-[color:var(--status-pinned)]/35 bg-raised px-3 py-2" aria-label="Partial sweep results">
      <p className="mb-1 text-[11.5px] font-medium text-ink">Partial sweep — completed detectors are shown below</p>
      {partial.map((entry) => (
        <div key={`${entry.detector}:${entry.reason}`} className="grid grid-cols-[140px_1fr] gap-3 py-1 text-[11px]">
          <span className="mono text-faint">{entry.detector}</span>
          <pre className="min-w-0 whitespace-pre-wrap text-muted">{entry.reason}</pre>
        </div>
      ))}
    </aside>
  )
}

function FindingRow({
  finding,
  copied,
  busy,
  onCopy,
  onApply,
  onDismiss,
  onRestore,
}: {
  finding: SweepFinding
  copied: boolean
  busy: boolean
  onCopy: () => void
  onApply: () => void
  onDismiss: () => void
  onRestore: () => void
}) {
  return (
    <article className="grid grid-cols-[minmax(0,1fr)_minmax(280px,42%)] gap-5 px-3 py-3" data-finding={finding.fingerprint}>
      <div className="min-w-0">
        <div className="flex items-start gap-2">
          <span className="chip mt-px" data-tone={finding.severity === 'medium' ? 'warn' : 'neutral'} data-outline="true">
            {finding.severity}
          </span>
          <h3 className="text-[12.5px] font-medium leading-snug text-ink">{finding.title}</h3>
        </div>
        <ul className="mt-2 space-y-1 pl-1">
          {finding.evidence.map((line, index) => (
            <li key={`${finding.fingerprint}:evidence:${index}`} className="flex gap-2 text-[11.5px] leading-relaxed text-faint">
              <span className="mt-[0.65em] h-px w-2 shrink-0 bg-line" />
              <span>{line}</span>
            </li>
          ))}
        </ul>
      </div>

      <div className="min-w-0">
        <p className="legend mb-1.5">Suggested argv</p>
        <pre className="overflow-x-auto whitespace-pre rounded-md border border-line bg-sunken p-2.5 text-[10.5px] leading-relaxed text-muted">{formatSuggestion(finding)}</pre>
        <p className="mt-1.5 text-[10.5px] leading-relaxed text-faint">{finding.suggested.rationale}</p>
        <div className="mt-2 flex items-center justify-end gap-1.5">
          <button type="button" className="btn btn-quiet h-6" onClick={onCopy}>
            {copied ? 'Copied' : finding.suggested.applicable ? 'Copy argv' : 'Copy template'}
          </button>
          {finding.dismissed ? (
            <button type="button" className="btn btn-quiet h-6" onClick={onRestore}>Restore</button>
          ) : (
            <button type="button" className="btn btn-quiet h-6" disabled={busy} onClick={onDismiss}>Dismiss</button>
          )}
          {finding.suggested.applicable && !finding.dismissed && (
            <button type="button" className="btn h-6" onClick={onApply}>Apply</button>
          )}
        </div>
      </div>
    </article>
  )
}
