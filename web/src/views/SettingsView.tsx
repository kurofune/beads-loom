import { useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import clsx from 'clsx'
import type { Project } from '@/api/types'
import { useDiscovery, useHealth } from '@/api/hooks'
import { useHideProject, useRescanDiscovery, useUnhideProject } from '@/api/mutations'
import { MOCK_MODE } from '@/api/client'
import { GROUP_ORDER, SHORTCUTS, SURFACE_LABEL, type Surface } from '@/keyboard/registry'
import { THEMES, type ThemeId } from '@/lib/theme'
import { CONFIG_PATH, defaultProject, setDefaultProject } from '@/lib/settings'
import { useLive } from '@/components/Live'
import { absoluteTime } from '@/lib/time'

/**
 * Settings.
 *
 * Deliberately small. Loom's real configuration lives in
 * `~/.config/loom/config.yaml` and is the operator's, not the browser's — which
 * projects exist, where their workdirs are, how many workers summoner may use.
 * What belongs here is only what is genuinely *this browser's* preference, plus
 * an honest pointer at where everything else actually lives.
 */
const SURFACE_ORDER: Surface[] = ['*', 'backlog', 'board', 'graph', 'now', 'detail']

export function SettingsView({
  projects,
  theme,
  onSetTheme,
}: {
  projects: Project[]
  theme: ThemeId
  onSetTheme: (theme: ThemeId) => void
}) {
  const navigate = useNavigate()
  const health = useHealth()
  const { status } = useLive()
  const start = defaultProject()

  return (
    <div className="flex h-full min-w-0 flex-1 flex-col">
      <header
        className="flex shrink-0 items-center gap-3 border-b border-line px-4"
        style={{ height: 'var(--header-h)' }}
      >
        <h1 className="text-[13.5px] font-semibold text-ink">Settings</h1>
        <span className="text-[11.5px] text-faint">preferences stored in this browser</span>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto max-w-[720px] px-6 py-6">
          <Section
            legend="Theme"
            note="Applied to <html data-theme> and remembered here. First run follows your OS setting."
          >
            <div className="flex flex-wrap gap-2">
              {THEMES.map((t) => (
                <button
                  key={t.id}
                  type="button"
                  onClick={() => onSetTheme(t.id)}
                  aria-pressed={t.id === theme}
                  className={clsx(
                    'flex items-center gap-2.5 rounded-md px-3 py-2 text-left transition-colors duration-[--dur-fast]',
                    t.id === theme ? 'bg-selected' : 'bg-raised hover:bg-hover',
                  )}
                  style={{
                    boxShadow:
                      t.id === theme
                        ? 'inset 0 0 0 1.5px var(--accent)'
                        : 'inset 0 0 0 1px var(--border)',
                  }}
                >
                  <Swatch theme={t.id} />
                  <span className="text-[12.5px] text-ink">{t.label}</span>
                  {t.id === theme && (
                    <span className="chip" data-tone="accent">
                      active
                    </span>
                  )}
                </button>
              ))}
            </div>
            <p className="mt-2 text-[11px] leading-relaxed text-faint">
              A third theme is data-only — one CSS file assigning the same tokens under a new{' '}
              <span className="mono">[data-theme]</span> selector, plus one line in{' '}
              <span className="mono">THEMES</span>. See{' '}
              <span className="mono">web/src/themes/README.md</span>.
            </p>
          </Section>

          <Section
            legend="Starting project"
            note={`Which project Loom opens on. Projects themselves are configured in ${CONFIG_PATH}.`}
          >
            <div className="flex flex-wrap gap-1.5">
              <ProjectChoice
                label="First in config"
                active={!start}
                onClick={() => setDefaultProject(null)}
                hint="Follow the order in config.yaml"
              />
              {projects.map((p) => (
                <ProjectChoice
                  key={p.name}
                  label={p.name}
                  active={start === p.name}
                  onClick={() => setDefaultProject(p.name)}
                  {...(p.reachable ? {} : { hint: 'currently unreachable' })}
                />
              ))}
            </div>
            {start && !projects.some((p) => p.name === start) && (
              // A project can vanish from config.yaml between sessions; saying so
              // beats silently ignoring the preference.
              <p className="mt-2 text-[11.5px]" style={{ color: 'var(--warn)' }}>
                <span className="mono">{start}</span> is no longer in your config, so Loom will
                fall back to the first project.
              </p>
            )}
          </Section>

          <ProjectDiscoverySection projects={projects} />

          <Section legend="Connection" note="Where this browser is talking to, and how.">
            <dl className="grid grid-cols-[132px_1fr] gap-x-4 gap-y-1.5 text-[12px]">
              <dt className="text-faint">API</dt>
              <dd className="mono text-muted">{MOCK_MODE ? 'mock fixtures (VITE_MOCK=1)' : '/api → the loom binary'}</dd>
              <dt className="text-faint">bd</dt>
              <dd className="mono text-muted">{health.data?.bdVersion ?? '—'}</dd>
              <dt className="text-faint">event stream</dt>
              <dd className="mono text-muted">
                {status.state}
                {status.deferred > 0 && ` · ${status.deferred} update(s) queued`}
              </dd>
              <dt className="text-faint">projects</dt>
              <dd className="text-muted">
                {projects.filter((p) => p.reachable).length} reachable of {projects.length}
              </dd>
            </dl>
            <p className="mt-2 text-[11px] leading-relaxed text-faint">
              Everything else — the listen address, project workdirs, remote servers, per-project
              integration settings — lives in <span className="mono">{CONFIG_PATH}</span>{' '}
              and is read at startup. Loom does not write it.
            </p>
          </Section>

          <Section legend="Keyboard" note="Every binding, grouped by where it works. Press ? anywhere for the same list.">
            <div className="space-y-4">
              {SURFACE_ORDER.map((surface) => {
                const rows = SHORTCUTS.filter((s) =>
                  surface === '*'
                    ? !s.surfaces || s.surfaces.length === 0
                    : s.surfaces?.includes(surface),
                )
                if (rows.length === 0) return null
                return (
                  <div key={surface}>
                    <p className="legend mb-1.5">{SURFACE_LABEL[surface]}</p>
                    <ul className="space-y-0.5">
                      {rows.map((s) => (
                        <li key={`${surface}-${s.id}`} className="flex items-baseline gap-3">
                          <span className="flex shrink-0 items-center gap-1" style={{ width: 76 }}>
                            {(s.display ?? s.keys).map((k, i) => (
                              <kbd key={i} className="kbd">
                                {k === 'Escape' ? 'esc' : k === 'Enter' ? '↵' : k}
                              </kbd>
                            ))}
                          </span>
                          <span className="flex-1 text-[12px] text-muted">{s.label}</span>
                          {s.alsoBy && (
                            <span className="shrink-0 text-[11px] text-faint">{s.alsoBy}</span>
                          )}
                        </li>
                      ))}
                    </ul>
                  </div>
                )
              })}
            </div>
            <p className="mt-3 text-[11px] leading-relaxed text-faint">
              {SHORTCUTS.length} bindings across {GROUP_ORDER.length} categories. Every one has a
              mouse equivalent — nothing in Loom is keyboard-only.
            </p>
          </Section>

          <div className="pt-2">
            <button
              type="button"
              className="btn btn-quiet"
              onClick={() => navigate(-1)}
            >
              ← Back
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}

const DISCOVERY_YAML = `discovery:
  enabled: true
  roots:
    - ~/Projects`

export function ProjectDiscoverySection({ projects }: { projects: Project[] }) {
  const discovery = useDiscovery()
  const hide = useHideProject()
  const unhide = useUnhideProject()
  const rescan = useRescanDiscovery()
  const [copied, setCopied] = useState(false)
  const status = discovery.data

  return (
    <Section
      legend="Project discovery"
      note="Find beads workspaces beneath configured roots and keep the project list current."
    >
      {discovery.isLoading && (
        <p className="text-[11.5px] text-faint">Reading discovery settings…</p>
      )}

      {discovery.error && (
        <p className="text-[11.5px]" style={{ color: 'var(--danger)' }}>
          Could not read discovery settings: {discovery.error.message}
        </p>
      )}

      {status && !status.enabled && (
        <div>
          <p className="mb-2 text-[11.5px] leading-relaxed text-muted">
            Automatic discovery is disabled. Add this block to{' '}
            <span className="mono">{CONFIG_PATH}</span> and restart Loom.
          </p>
          <button
            type="button"
            className="group flex w-full items-start gap-3 rounded-md bg-sunken px-3 py-2 text-left ring-1 ring-line hover:bg-hover"
            title="Copy discovery YAML"
            onClick={() => {
              void navigator.clipboard?.writeText(DISCOVERY_YAML)
              setCopied(true)
              window.setTimeout(() => setCopied(false), 1200)
            }}
          >
            <code className="mono min-w-0 flex-1 whitespace-pre text-[11px] leading-relaxed text-muted">
              {DISCOVERY_YAML}
            </code>
            <span className="shrink-0 text-[10px] text-faint group-hover:text-muted">
              {copied ? 'copied' : 'copy'}
            </span>
          </button>
        </div>
      )}

      {status?.enabled && (
        <div className="space-y-4">
          <dl className="grid grid-cols-[88px_1fr] gap-x-3 gap-y-1 text-[11.5px]">
            <dt className="text-faint">roots</dt>
            <dd className="mono text-muted">{status.roots.join(', ')}</dd>
            <dt className="text-faint">schedule</dt>
            <dd className="text-muted">every {formatInterval(status.intervalSeconds)}</dd>
            <dt className="text-faint">last scan</dt>
            <dd className="text-muted">
              {status.lastScanAt ? absoluteTime(status.lastScanAt) : 'never'}
            </dd>
          </dl>

          <div className="flex items-center gap-2">
            <button
              type="button"
              className="btn"
              disabled={rescan.isPending}
              onClick={() => rescan.mutate()}
            >
              {rescan.isPending ? 'Scanning…' : 'Rescan now'}
            </button>
            {rescan.data && (
              <span className="text-[11px] text-muted" role="status">
                {rescan.data.added.length} added, {rescan.data.removed.length} removed
              </span>
            )}
            {rescan.error && (
              <span className="text-[11px]" style={{ color: 'var(--danger)' }} role="alert">
                {rescan.error.message}
              </span>
            )}
          </div>

          <DiscoveryList
            legend="Discovered projects"
            empty={
              <>
                No projects found. Run <span className="mono">bd init</span> under a root above, or add
                a project to <span className="mono">{CONFIG_PATH}</span>.
              </>
            }
            projects={projects.filter((project) => project.source === 'discovered').map((project) => ({
              name: project.name,
              workdir: project.workdir,
            }))}
            action="Hide"
            pendingWorkdir={hide.isPending ? hide.variables : undefined}
            onAction={(workdir) => hide.mutate(workdir)}
          />

          <DiscoveryList
            legend="Hidden projects"
            empty="No hidden projects."
            projects={status.hidden}
            action="Unhide"
            pendingWorkdir={unhide.isPending ? unhide.variables : undefined}
            onAction={(workdir) => unhide.mutate(workdir)}
          />
        </div>
      )}
    </Section>
  )
}

function DiscoveryList({
  legend,
  empty,
  projects,
  action,
  pendingWorkdir,
  onAction,
}: {
  legend: string
  empty: ReactNode
  projects: { name: string; workdir: string }[]
  action: 'Hide' | 'Unhide'
  pendingWorkdir?: string
  onAction: (workdir: string) => void
}) {
  return (
    <div>
      <p className="legend mb-1.5">{legend}</p>
      {projects.length === 0 ? (
        <p className="rounded-md bg-sunken px-3 py-2 text-[11.5px] text-faint ring-1 ring-line">
          {empty}
        </p>
      ) : (
        <ul className="divide-y divide-line overflow-hidden rounded-md bg-raised ring-1 ring-line">
          {projects.map((project) => {
            const pending = pendingWorkdir === project.workdir
            return (
              <li key={project.workdir} className="flex items-center gap-3 px-3 py-2">
                <span className="min-w-0 flex-1">
                  {project.name && <span className="block text-[12px] text-ink">{project.name}</span>}
                  <span className="mono block truncate text-[10.5px] text-faint" title={project.workdir}>
                    {project.workdir}
                  </span>
                </span>
                <button
                  type="button"
                  className="btn btn-quiet h-6 shrink-0 px-2 text-[11px]"
                  disabled={pending}
                  aria-label={`${action} ${project.name || project.workdir}`}
                  onClick={() => onAction(project.workdir)}
                >
                  {pending ? `${action}…` : action}
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

function formatInterval(seconds: number): string {
  if (seconds % 3600 === 0) return `${seconds / 3600}h`
  if (seconds % 60 === 0) return `${seconds / 60}m`
  return `${seconds}s`
}

function Section({
  legend,
  note,
  children,
}: {
  legend: string
  note?: string
  children: React.ReactNode
}) {
  return (
    <section className="mb-7 border-b border-line pb-6 last:border-0">
      <h2 className="text-[12.5px] font-semibold text-ink">{legend}</h2>
      {note && <p className="mt-0.5 mb-3 text-[11.5px] leading-relaxed text-faint">{note}</p>}
      {children}
    </section>
  )
}

function ProjectChoice({
  label,
  active,
  hint,
  onClick,
}: {
  label: string
  active: boolean
  hint?: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={clsx(
        'rounded-md px-2.5 py-1.5 text-[12px] transition-colors duration-[--dur-fast]',
        active ? 'bg-selected text-ink' : 'bg-raised text-muted hover:bg-hover hover:text-ink',
      )}
      style={{
        boxShadow: active ? 'inset 0 0 0 1.5px var(--accent)' : 'inset 0 0 0 1px var(--border)',
      }}
      title={hint}
    >
      {label}
      {hint && <span className="ml-1.5 text-[10.5px] text-faint">{hint}</span>}
    </button>
  )
}

/**
 * A live preview of the theme, drawn with that theme's own tokens.
 *
 * `data-theme` on the swatch means the CSS variables resolve to the *other*
 * theme's values inside it — which is the whole argument for the token layer,
 * demonstrated in 20 pixels.
 */
function Swatch({ theme }: { theme: ThemeId }) {
  return (
    <span
      data-theme={theme}
      className="flex h-6 w-9 shrink-0 items-center gap-0.5 overflow-hidden rounded-xs px-1"
      style={{ background: 'var(--surface)', boxShadow: 'inset 0 0 0 1px var(--border)' }}
      aria-hidden="true"
    >
      <span className="h-2.5 w-1 rounded-full" style={{ background: 'var(--accent)' }} />
      <span className="h-2.5 w-1 rounded-full" style={{ background: 'var(--status-in-progress)' }} />
      <span className="h-2.5 w-1 rounded-full" style={{ background: 'var(--priority-p0)' }} />
      <span className="h-2.5 flex-1 rounded-full" style={{ background: 'var(--surface-raised)' }} />
    </span>
  )
}
