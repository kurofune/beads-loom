import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api, ApiError } from '@/api/client'
import { qk } from '@/api/hooks'
import type { NowEntry, NowResponse, Project } from '@/api/types'
import { BacklogSkeleton, EmptyState, ErrorState } from '@/components/states'
import { Assignee, PriorityBadge, StatusDot } from '@/components/primitives'
import { ProjectBadges } from '@/components/ProjectBadges'
import { formatDuration } from '@/lib/time'
import { absoluteTime } from '@/lib/time'

/**
 * Now — what the agents are doing, across every project.
 *
 * The one screen that is not about a project. A manager running four agent
 * projects wants a single answer to "is anything stuck?", and the answer has to
 * be honest about its own coverage: an unreachable project is *named*, not
 * quietly omitted, because "nothing is running" and "I could not ask" look
 * identical otherwise and mean opposite things.
 *
 * Time-in-status ticks live. It is the number that turns a list into a
 * question — a worker eleven hours into one bead is the thing worth noticing.
 */
export function NowView({ projects }: { projects: Project[] }) {
  const navigate = useNavigate()
  const now = useTicker()

  const query = useQuery<NowResponse, ApiError>({
    queryKey: qk.now,
    queryFn: () => api.now(),
    // Cross-project, so no single db-invalidated covers it; a slow poll is the
    // honest mechanism here.
    refetchInterval: 20_000,
    staleTime: 10_000,
  })

  if (query.isLoading) return <Frame><BacklogSkeleton /></Frame>
  if (query.error) {
    return (
      <Frame>
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      </Frame>
    )
  }

  const data = query.data
  const entries = data?.entries ?? []
  const byProject = new Map<string, NowEntry[]>()
  for (const entry of entries) {
    const list = byProject.get(entry.project) ?? []
    list.push(entry)
    byProject.set(entry.project, list)
  }

  return (
    <Frame skipped={data?.skipped ?? []}>
      {entries.length === 0 ? (
        <EmptyState
          title="No agents working right now"
          detail={
            <>
              Nothing is <span className="mono">in_progress</span> in any reachable project. Work
              shows up here as soon as someone claims a ready issue.
            </>
          }
        />
      ) : (
        <div className="h-full overflow-y-auto px-4 py-3">
          {[...byProject.entries()].map(([project, rows]) => (
            <ProjectGroup
              key={project}
              project={project}
              rows={rows}
              now={now}
              info={projects.find((p) => p.name === project)}
              onOpenIssue={(id) =>
                navigate(`/${encodeURIComponent(project)}/issue/${encodeURIComponent(id)}`)
              }
            />
          ))}
        </div>
      )}
    </Frame>
  )
}

function Frame({ children, skipped = [] }: { children: React.ReactNode; skipped?: string[] }) {
  return (
    <div className="flex h-full min-w-0 flex-1 flex-col">
      <header
        className="flex shrink-0 items-center gap-3 border-b border-line px-4"
        style={{ height: 'var(--header-h)' }}
      >
        <h1 className="text-[13.5px] font-semibold text-ink">Now</h1>
        <span className="text-[11.5px] text-faint">in-progress work across every project</span>

        {skipped.length > 0 && (
          // Coverage, stated. "Nothing is running" and "I could not ask" are
          // different answers and must not look the same.
          <span
            className="chip ml-auto"
            data-tone="warn"
            title={`Unreachable, so excluded from this answer: ${skipped.join(', ')}`}
          >
            {skipped.length} project{skipped.length === 1 ? '' : 's'} not counted
          </span>
        )}
      </header>
      <div className="min-h-0 flex-1">{children}</div>
    </div>
  )
}

function ProjectGroup({
  project,
  rows,
  now,
  info,
  onOpenIssue,
}: {
  project: string
  rows: NowEntry[]
  now: number
  /** The project's record, for its integration badges; absent while loading. */
  info: Project | undefined
  onOpenIssue: (id: string) => void
}) {

  return (
    <section className="mb-4">
      <header className="mb-1.5 flex items-center gap-2">
        <h2 className="text-[11.5px] font-semibold text-ink">{project}</h2>
        <span className="mono text-[10.5px] text-faint">{rows.length}</span>
        {info && <ProjectBadges project={info} placement="now" />}
        <span className="hairline ml-1 flex-1 opacity-60" aria-hidden="true" />
      </header>

      <ul className="space-y-0.5">
        {rows.map((entry) => (
          <NowRow key={entry.issue.id} entry={entry} now={now} onOpen={() => onOpenIssue(entry.issue.id)} />
        ))}
      </ul>
    </section>
  )
}

function NowRow({ entry, now, onOpen }: { entry: NowEntry; now: number; onOpen: () => void }) {
  const { issue } = entry
  const since = entry.inStatusSince ? Date.parse(entry.inStatusSince) : NaN
  const elapsed = Number.isNaN(since) ? null : Math.max(0, now - since)

  return (
    <li>
      <button
        type="button"
        onClick={onOpen}
        className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors duration-[--dur-fast] hover:bg-hover"
        style={{ height: 'var(--row-h)' }}
      >
        <PriorityBadge priority={issue.priority} />
        <StatusDot status={issue.status} />
        <span className="mono w-[104px] shrink-0 truncate text-[11px] text-faint">{issue.id}</span>
        <span className="min-w-0 flex-1 truncate text-[12.5px] text-ink" title={issue.title}>
          {issue.title}
        </span>

        {entry.stale && (
          <span
            className="chip shrink-0"
            data-tone="warn"
            data-solid="true"
            title="Nothing has touched this bead in over 24 hours"
          >
            stale
          </span>
        )}

        <span
          className="mono w-[68px] shrink-0 text-right text-[10.5px]"
          style={{ color: entry.stale ? 'var(--warn)' : 'var(--text-faint)' }}
          title={entry.inStatusSince ? `In progress since ${absoluteTime(entry.inStatusSince)}` : undefined}
        >
          {elapsed === null ? '—' : formatDuration(elapsed)}
        </span>

        <Assignee name={issue.assignee} />
      </button>
    </li>
  )
}

/**
 * A 1s tick, so time-in-status counts up rather than sitting still.
 *
 * Deliberately local to this view: nothing else on screen needs a per-second
 * re-render, and hoisting it would make the whole app repaint every second.
 */
function useTicker(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 1_000)
    return () => window.clearInterval(t)
  }, [])
  return now
}
