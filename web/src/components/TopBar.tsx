import type { ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import type { Project } from '@/api/types'
import { useIntegrations } from '@/integrations'
import type { ViewKind } from '@/lib/views'
import { DoltSync } from './DoltSync'
import { ProjectBadges } from './ProjectBadges'
import { ReadOnlyBadge } from './ReadOnlyBadge'

/** Core's views. Integrations add theirs through their `views` slot. */
const CORE_TABS = [
  { id: 'initiatives', label: 'Initiatives', path: 'initiatives', chord: 'g i' },
  { id: 'backlog', label: 'Backlog', path: 'backlog', chord: 'g l' },
  { id: 'board', label: 'Board', path: 'board', chord: 'g b' },
  { id: 'graph', label: 'Graph', path: 'graph', chord: 'g d' },
  { id: 'sweep', label: 'Sweep', path: 'sweep', chord: '' },
]

export function TopBar({
  project,
  current = 'backlog',
  children,
}: {
  project: Project | undefined
  current?: ViewKind
  children?: ReactNode
}) {
  const navigate = useNavigate()
  const mounted = useIntegrations(project)
  // A contributed view's tab exists only where it mounted, placed where Runs
  // always sat: after Board.
  const at = CORE_TABS.findIndex((t) => t.id === 'board') + 1
  const tabs = [
    ...CORE_TABS.slice(0, at),
    ...mounted.views.map((v) => ({ id: v.id, label: v.label, path: v.path, chord: v.chord ?? '' })),
    ...CORE_TABS.slice(at),
  ]
  return (
    <header
      className="flex shrink-0 items-center gap-4 border-b border-line px-4"
      style={{ height: 'var(--header-h)' }}
    >
      <div className="flex min-w-0 items-baseline gap-2">
        <h1 className="truncate text-[13.5px] font-semibold text-ink">
          {project?.name ?? '—'}
        </h1>
        {/* The dolt database name, when it differs from the display name —
            repeating an identical name is noise, not information. */}
        {project && project.db !== project.name && (
          <span className="mono text-[10.5px] text-faint" title={project.workdir}>
            {project.db}
          </span>
        )}
        {project && <ProjectBadges project={project} placement="topbar" />}
        {project && <ReadOnlyBadge project={project} />}
      </div>

      <div className="seg shrink-0" role="tablist" aria-label="View">
        {tabs.map((v) => (
          <button
            key={v.id}
            type="button"
            role="tab"
            aria-current={v.id === current ? 'true' : undefined}
            aria-selected={v.id === current}
            disabled={!project}
            onClick={() => project && navigate(`/${encodeURIComponent(project.name)}/${v.path}`)}
            title={[v.label, v.chord && `(${v.chord})`].filter(Boolean).join(' ')}
          >
            {v.label}
          </button>
        ))}
      </div>

      <div className="ml-auto flex items-center gap-2">
        {project && <DoltSync project={project.name} enabled={project.reachable} />}
        {children}
      </div>
    </header>
  )
}
