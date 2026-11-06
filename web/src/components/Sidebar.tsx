import clsx from 'clsx'
import { DndContext, PointerSensor, closestCenter, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import { SortableContext, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { NavLink, useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import type { Project, ProjectHealth } from '@/api/types'
import { qk, useHealth } from '@/api/hooks'
import { ConnectionIndicator } from './Live'
import { ProjectBadges } from './ProjectBadges'
import { ReadOnlyBadge } from './ReadOnlyBadge'
import { SavedViewList } from './SavedViews'
import { useIntegrations } from '@/integrations'
import { portableView, type ViewKind } from '@/lib/views'
import { THEMES, type ThemeId } from '@/lib/theme'
import { CONFIG_PATH } from '@/lib/settings'

/**
 * Health bar: ready / in-progress / manually-blocked as one 3px stacked rule,
 * scaled against the project's open count.
 *
 * A stacked bar rather than three numbers because the manager's question is
 * proportional — "is this project mostly ready or mostly stuck?" — and a bar
 * answers it in the same glance that reads the project name. Exact counts stay
 * available as text below and in the tooltip.
 */
function HealthBar({ health }: { health: ProjectHealth }) {
  const { ready, inProgress, blocked, open } = health
  const denom = Math.max(open, ready + inProgress + blocked, 1)
  const pct = (n: number) => `${(n / denom) * 100}%`

  return (
    <span
      className="flex h-[3px] w-full overflow-hidden rounded-full bg-active"
      role="img"
      aria-label={`${ready} ready, ${inProgress} in progress, ${blocked} blocked, ${open} open`}
    >
      <span style={{ width: pct(ready), background: 'var(--accent)' }} />
      <span style={{ width: pct(inProgress), background: 'var(--status-in-progress)' }} />
      <span style={{ width: pct(blocked), background: 'var(--status-blocked)' }} />
    </span>
  )
}

/**
 * The browser fires a click on the row under the pointer after a drop, and
 * that click reaches the link as a plain navigation — a full page load. It is
 * swallowed at the window, ahead of the link; the timer retires the trap when
 * the drop ended off any row and no click comes.
 */
function swallowNextClick() {
  const swallow = (e: MouseEvent) => {
    e.preventDefault()
    e.stopPropagation()
  }
  window.addEventListener('click', swallow, { capture: true, once: true })
  setTimeout(() => window.removeEventListener('click', swallow, { capture: true }), 100)
}

function ProjectRow({
  project,
  view,
}: {
  project: Project
  view: ViewKind
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: project.name,
  })
  const { reachable, reason } = project
  // The backend sends a null health block for an unreachable project, so the
  // counts render only when there is something to count.
  const { health } = project
  const qc = useQueryClient()
  const mounted = useIntegrations(project)
  const target = portableView(view, mounted.views.map((v) => v.path))

  // Retry is a nudge, not a reconnect: the backend's poller owns retries on a
  // 15s backoff, so this just asks for the current answer rather than
  // pretending the UI can revive a dolt server.
  const retry = (e: React.MouseEvent) => {
    e.preventDefault()
    e.stopPropagation()
    void qc.invalidateQueries({ queryKey: qk.projects })
  }

  return (
    <NavLink
      ref={setNodeRef}
      {...attributes}
      {...listeners}
      // Links are natively draggable; the sortable drag must be the only one.
      draggable={false}
      style={{
        transform: CSS.Translate.toString(transform),
        transition,
        zIndex: isDragging ? 1 : undefined,
      }}
      // Switching projects preserves the kind of view you were on.
      to={`/${encodeURIComponent(project.name)}/${target}`}
      // Unreachable projects stay navigable: the view explains the outage, and
      // blocking the click would hide the one screen that says what to run.
      className={({ isActive }) =>
        clsx(
          'group relative block touch-none rounded-md select-none px-2 py-1.5 transition-colors duration-[--dur-fast]',
          isActive
            ? 'bg-selected shadow-[inset_0_0_0_1px_color-mix(in_oklab,var(--accent)_45%,transparent)]'
            : 'hover:bg-hover',
          isDragging && 'bg-raised shadow-[var(--shadow-overlay)]',
          !reachable && 'opacity-55',
        )
      }
      title={reachable ? undefined : `Unreachable — ${reason}`}
    >
      {({ isActive }) => (
        <>
          <span className="flex items-baseline gap-2">
            <span
              className={clsx(
                'flex-1 truncate text-[12.5px]',
                isActive ? 'font-medium text-ink' : 'text-muted group-hover:text-ink',
              )}
            >
              {project.name}
            </span>
            {project.source === 'discovered' && (
              <span
                className="mono shrink-0 rounded-xs bg-raised px-1 py-px text-[9px] text-faint"
                title="Discovered automatically — manage in Settings"
              >
                auto
              </span>
            )}
            <ProjectBadges project={project} placement="sidebar" />
            <ReadOnlyBadge project={project} />
            {reachable && health ? (
              <span className="mono text-[10.5px] text-faint">{health.open}</span>
            ) : (
              <span className="chip" data-tone="warn" title={reason}>
                off
              </span>
            )}
          </span>

          {!reachable && (
            <span className="mt-1 flex items-center gap-1.5">
              <span className="min-w-0 flex-1 truncate text-[10px] text-faint" title={reason}>
                {reason || 'unreachable'}
              </span>
              <button
                type="button"
                onClick={retry}
                className="shrink-0 rounded-xs px-1 text-[10px] text-faint hover:bg-hover hover:text-ink"
                title="Re-check this project now"
              >
                retry
              </button>
            </span>
          )}

          {reachable && health && (
            <span className="mt-1.5 flex items-center gap-2">
              <HealthBar health={health} />
              <span className="mono flex shrink-0 items-center gap-1.5 text-[10px]">
                <span style={{ color: 'var(--accent)' }} title={`${health.ready} ready`}>
                  {health.ready}
                </span>
                <span
                  style={{ color: 'var(--status-in-progress)' }}
                  title={`${health.inProgress} in progress`}
                >
                  {health.inProgress}
                </span>
                {health.blocked > 0 && (
                  <span
                    style={{ color: 'var(--status-blocked)' }}
                    title={`${health.blocked} blocked`}
                  >
                    {health.blocked}
                  </span>
                )}
              </span>
            </span>
          )}
        </>
      )}
    </NavLink>
  )
}

function Wordmark() {
  return (
    <div className="flex items-baseline gap-2 px-2">
      <span className="text-[13px] font-semibold tracking-[0.22em] text-ink">LOOM</span>
      <span className="thread h-[9px] flex-1 rounded-xs opacity-70" aria-hidden="true" />
    </div>
  )
}

export function Sidebar({
  projects,
  loading,
  failed = false,
  onReorder,
  view,
  currentProject,
  theme,
  onToggleTheme,
  onShowHelp,
  onOpenPalette,
  onOpenSettings,
}: {
  projects: Project[]
  loading: boolean
  /** The project list request failed: an empty list then means "unknown", not "none". */
  failed?: boolean
  /** Drag-sort: move project `from` into `to`'s slot. */
  onReorder: (from: string, to: string) => void
  view: ViewKind
  /** Whose saved views to list. */
  currentProject: string
  theme: ThemeId
  onToggleTheme: () => void
  onShowHelp: () => void
  onOpenPalette: () => void
  onOpenSettings: () => void
}) {
  const health = useHealth()
  const navigate = useNavigate()
  const themeLabel = THEMES.find((t) => t.id === theme)?.label ?? theme
  // A small move threshold keeps a plain click a navigation, not a drag.
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 4 } }))
  const onDragEnd = ({ active, over }: DragEndEvent) => {
    swallowNextClick()
    if (over && active.id !== over.id) onReorder(String(active.id), String(over.id))
  }

  return (
    <aside
      className="flex h-full flex-col border-r border-line bg-sunken"
      style={{ width: 'var(--sidebar-w)', flex: 'none' }}
    >
      <div className="flex items-center" style={{ height: 'var(--header-h)' }}>
        <Wordmark />
      </div>

      <div className="flex-1 overflow-y-auto px-2 pb-2">
        <p className="legend mb-1.5 px-2">Projects</p>

        {loading && (
          <div className="space-y-2 px-2">
            {[0, 1, 2].map((i) => (
              <div key={i} className="space-y-1.5">
                <div className="shimmer h-2.5 w-2/3 rounded-xs" />
                <div className="shimmer h-[3px] w-full rounded-full" />
              </div>
            ))}
          </div>
        )}

        {!loading && failed && (
          <p className="px-2 text-[11.5px] leading-relaxed text-faint">Could not load the project list.</p>
        )}

        {!loading && !failed && projects.length === 0 && (
          <p className="px-2 text-[11.5px] leading-relaxed text-faint">
            No projects found. Add one to <span className="mono">{CONFIG_PATH}</span>.
          </p>
        )}

        <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onDragEnd}>
          <SortableContext items={projects.map((p) => p.name)} strategy={verticalListSortingStrategy}>
            <nav className="space-y-0.5" title="Drag to reorder">
              {projects.map((p) => (
                <ProjectRow key={p.name} project={p} view={view} />
              ))}
            </nav>
          </SortableContext>
        </DndContext>

        {currentProject && (
          <>
            <SavedViewList
              project={currentProject}
              onApply={(v) =>
                navigate(`/${encodeURIComponent(currentProject)}/backlog?view=${encodeURIComponent(v.id)}`)
              }
            />
            <NavLink
              to={`/${encodeURIComponent(currentProject)}/sweep`}
              className={({ isActive }) =>
                clsx(
                  'mt-2 flex items-center gap-2 rounded-md px-2 py-1.5 text-[11.5px] transition-colors duration-[--dur-fast]',
                  isActive ? 'bg-selected text-ink' : 'text-faint hover:bg-hover hover:text-muted',
                )
              }
            >
              <span className="mono text-[10px]">↔</span>
              <span className="flex-1">Reconciliation sweep</span>
            </NavLink>
          </>
        )}

        {projects.length > 0 && (
          <button
            type="button"
            onClick={onOpenPalette}
            className="mt-2 flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[11.5px] text-faint transition-colors duration-[--dur-fast] hover:bg-hover hover:text-muted"
            title="Command palette"
          >
            <span className="flex-1">Jump to…</span>
            <kbd className="kbd">⌘</kbd>
            <kbd className="kbd">k</kbd>
          </button>
        )}
      </div>

      <footer className="border-t border-line px-2 py-2">
        <div className="flex items-center justify-between gap-1">
          <button
            type="button"
            className="btn btn-quiet h-6 px-2 text-[11.5px]"
            onClick={onToggleTheme}
            title="Switch theme (t)"
          >
            <ThemeGlyph theme={theme} />
            {themeLabel}
          </button>

          <span className="flex items-center gap-0.5">
            <button
              type="button"
              className="icon-btn"
              onClick={onOpenSettings}
              title="Settings"
              aria-label="Settings"
            >
              <svg viewBox="0 0 12 12" width="12" height="12" aria-hidden="true">
                <circle cx="6" cy="6" r="2" fill="none" stroke="currentColor" strokeWidth="1.1" />
                <path
                  d="M6 1v1.4M6 9.6V11M1 6h1.4M9.6 6H11M2.5 2.5l1 1M8.5 8.5l1 1M9.5 2.5l-1 1M3.5 8.5l-1 1"
                  stroke="currentColor"
                  strokeWidth="1.1"
                  strokeLinecap="round"
                />
              </svg>
            </button>
            <button
              type="button"
              className="icon-btn"
              onClick={onShowHelp}
              title="Keyboard shortcuts (?)"
              aria-label="Keyboard shortcuts"
            >
              <span className="text-[12px] font-semibold">?</span>
            </button>
          </span>
        </div>

        <div className="mt-1.5">
          <ConnectionIndicator
            {...(health.data
              ? {
                  // `bd --version` already prints "bd version 1.1.2 (Homebrew)";
                  // only add the prefix when the backend hands back a bare number.
                  bdVersion: health.data.bdVersion
                    .replace(/^bd version /i, 'bd ')
                    .replace(/^(?!bd )/, 'bd '),
                }
              : {})}
          />
        </div>
      </footer>
    </aside>
  )
}

function ThemeGlyph({ theme }: { theme: ThemeId }) {
  const dark = theme === 'loom-dark'
  return (
    <svg viewBox="0 0 12 12" width="12" height="12" aria-hidden="true">
      <circle
        cx="6"
        cy="6"
        r="3.4"
        fill={dark ? 'none' : 'currentColor'}
        stroke="currentColor"
        strokeWidth="1.1"
      />
      {dark && <path d="M6 2.6a3.4 3.4 0 0 0 0 6.8z" fill="currentColor" />}
    </svg>
  )
}
