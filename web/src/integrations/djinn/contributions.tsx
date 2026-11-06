import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api, ApiError } from '@/api/client'
import type { EvidenceResponse, Project, SpendResponse } from '@/api/types'
import { Section } from '@/components/DetailSection'
import { InlineError, InlineLoading } from '@/components/states'
import type { BadgePlacement } from '../types'
import { EvidenceReport } from './EvidenceReport'
import { djinnKeys, useActiveRun } from './live'
import { formatUsd } from './runs'

/** The project's djinn chip: "djinn", or "running" while a run is live. */
export function DjinnBadge({ project, placement }: { project: Project; placement: BadgePlacement }) {
  const activeRun = useActiveRun(project.name)
  const navigate = useNavigate()

  if (placement === 'now') {
    return (
      <button
        type="button"
        onClick={() => navigate(`/${encodeURIComponent(project.name)}/runs`)}
        className="chip"
        data-tone={activeRun ? 'accent' : 'neutral'}
        data-outline={activeRun ? undefined : 'true'}
        title={activeRun ? `Run ${activeRun} is live` : 'Open this project’s runs'}
      >
        {activeRun ? 'run live' : 'runs'} →
      </button>
    )
  }
  if (placement === 'topbar') {
    return (
      <span className="chip" data-tone="neutral" title="This project has a .djinn directory">
        djinn
      </span>
    )
  }
  return (
    <span
      className="chip"
      data-tone={activeRun ? 'accent' : 'neutral'}
      data-solid={activeRun ? 'true' : undefined}
      data-outline={activeRun ? undefined : 'true'}
      title={activeRun ? `A summoner run is live here (${activeRun})` : 'This project has a .djinn directory'}
    >
      {activeRun ? 'running' : 'djinn'}
    </span>
  )
}

/** A missing record is an answer, not a failure: nothing to show. */
const isAbsent = (error: ApiError | null) => error?.status === 404

/**
 * djinn's own answer to "did the agent actually do it?" — present only once a
 * worker has written .djinn/evidence/<bead>.yml.
 */
export function EvidenceSection({
  project,
  issueId,
  onOpenIssue,
}: {
  project: string
  issueId: string
  onOpenIssue: (id: string) => void
}) {
  const query = useQuery<EvidenceResponse, ApiError>({
    queryKey: djinnKeys.evidence(project, issueId),
    queryFn: () => api.evidence(project, issueId),
    retry: false,
    staleTime: 30_000,
  })

  if (query.isPending) {
    return (
      <Section legend="Evidence">
        <InlineLoading label="Loading evidence" />
      </Section>
    )
  }
  if (query.error) {
    if (isAbsent(query.error)) return null
    return (
      <Section legend="Evidence">
        <InlineError error={query.error} what="evidence" onRetry={() => void query.refetch()} />
      </Section>
    )
  }
  const evidence = query.data.evidence
  if (!evidence || evidence.criteria.length === 0) return null
  return (
    <Section legend="Evidence">
      <EvidenceReport evidence={evidence} onOpenIssue={onOpenIssue} />
    </Section>
  )
}

/** Summoner spend over the last seven days, in the stats strip. */
export function SpendTile({ project }: { project: string }) {
  const query = useQuery<SpendResponse, ApiError>({
    queryKey: djinnKeys.spend(project),
    queryFn: () => api.spend(project),
    retry: false,
    staleTime: 30_000,
  })

  if (query.error) {
    return <InlineError error={query.error} what="spend" onRetry={() => void query.refetch()} />
  }
  const body = query.isPending ? (
    <InlineLoading label="Loading spend">—</InlineLoading>
  ) : query.data.last7dUsd === null ? (
    // "No runs" and "runs that cost nothing" are different answers; a $0.00
    // chip would assert the wrong one.
    'no run history'
  ) : (
    `${formatUsd(query.data.last7dUsd)} / 7d`
  )

  return (
    <span
      className="chip"
      data-tone="neutral"
      data-outline="true"
      data-testid="djinn-spend"
      title="Summoner spend in the last 7 days, summed from run summaries"
    >
      {body}
    </span>
  )
}
