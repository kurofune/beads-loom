import { useCallback, useMemo, useState } from 'react'
import {
  Navigate,
  Route,
  BrowserRouter as Router,
  Routes,
  useLocation,
  useNavigate,
  useParams,
} from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ApiError } from '@/api/client'
import { useProjects } from '@/api/hooks'
import { Sidebar } from '@/components/Sidebar'
import { CheatSheet } from '@/components/CheatSheet'
import { CommandPalette } from '@/components/CommandPalette'
import { ToastProvider } from '@/components/Toast'
import { LiveProvider } from '@/components/Live'
import { LabelRulesProvider } from '@/components/labels'
import { EmptyState, ErrorState } from '@/components/states'
import { NoProjectsFound } from '@/components/NoProjectsFound'
import { BacklogView } from '@/views/BacklogView'
import { InitiativesView } from '@/views/InitiativesView'
import { BoardView } from '@/views/BoardView'
import { NowView } from '@/views/NowView'
import { GraphView } from '@/views/GraphView'
import { SettingsView } from '@/views/SettingsView'
import { SweepView } from '@/views/SweepView'
import { useShortcuts } from '@/keyboard/useShortcuts'
import { buildActions } from '@/lib/palette'
import { useSavedViews } from '@/components/SavedViews'
import { describeView } from '@/lib/saved-views'
import { applyTheme, currentTheme, nextTheme, type ThemeId } from '@/lib/theme'
import { resolveStartProject } from '@/lib/settings'
import { applyProjectOrder, moveProject, projectOrder, setProjectOrder } from '@/lib/project-order'
import { portableView, type ViewKind } from '@/lib/views'
import { commandActions, IntegrationRoots, IntegrationRoutes, useIntegrations, useViewPaths } from '@/integrations'
import type { ShortcutHandlers } from '@/keyboard/registry'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: true,
      // Freshness comes from the SSE stream now, not from polling.
      retry: false,
    },
  },
})

export type { ViewKind }

/**
 * Which project and view the URL is on. One parser, so the sidebar, the palette
 * and the shortcuts all agree on "where am I" — and so switching projects can
 * preserve the kind of view you were looking at.
 */
function useRoute(): { project: string; view: ViewKind } {
  const { pathname } = useLocation()
  const [, project = '', segment = ''] = pathname.split('/')
  // `issue` is the backlog's detail route; any other segment is a view, core or
  // contributed. An unknown one renders the not-found view, not the backlog.
  return {
    project: decodeURIComponent(project),
    view: segment === '' || segment === 'issue' ? 'backlog' : decodeURIComponent(segment),
  }
}

function Shell() {
  const projects = useProjects()
  const navigate = useNavigate()
  const { project: currentProject, view } = useRoute()

  const [theme, setTheme] = useState<ThemeId>(() => currentTheme())
  const [help, setHelp] = useState(false)
  const [palette, setPalette] = useState(false)

  const toggleTheme = useCallback(() => {
    setTheme((current) => {
      const next = nextTheme(current)
      applyTheme(next)
      return next
    })
  }, [])

  // Every consumer (sidebar, start project, palette) reads this one ordered list.
  const [order, setOrder] = useState<string[]>(() => projectOrder())
  const list = useMemo(() => applyProjectOrder(projects.data ?? [], order), [projects.data, order])
  const reorderProjects = useCallback(
    (from: string, to: string) => {
      const next = moveProject(
        list.map((p) => p.name),
        from,
        to,
      )
      setProjectOrder(next)
      setOrder(next)
    },
    [list],
  )

  const savedViews = useSavedViews(currentProject)
  const mounted = useIntegrations(list.find((p) => p.name === currentProject))
  const viewPaths = useViewPaths(list)

  const go = useCallback(
    (project: string, kind: ViewKind = view) => {
      // A contributed view only travels to a project that mounts it.
      const target = project === currentProject ? kind : portableView(kind, viewPaths.get(project) ?? [])
      navigate(`/${encodeURIComponent(project)}/${target}`)
    },
    [currentProject, navigate, view, viewPaths],
  )
  const goView = useCallback(
    (kind: string) => (kind === 'now' ? navigate('/now') : currentProject && go(currentProject, kind)),
    [currentProject, go, navigate],
  )

  // Opening a bead keeps the view you were on, so the palette and the recents
  // list land you back in the same place the row would have.
  const goIssue = useCallback(
    (id: string) =>
      navigate(
        view === 'initiatives'
          ? `/${encodeURIComponent(currentProject)}/initiatives/issue/${encodeURIComponent(id)}`
          : view === 'board'
            ? `/${encodeURIComponent(currentProject)}/board/issue/${encodeURIComponent(id)}`
            : `/${encodeURIComponent(currentProject)}/issue/${encodeURIComponent(id)}`,
      ),
    [currentProject, navigate, view],
  )

  // Everything in the palette that is *not* an issue. Issues are searched by
  // the palette itself, against the server, so the shell no longer fetches a
  // page of them to rank locally — that page was the whole of loom-nlm's bug.
  const actions = useMemo(
    () =>
      buildActions({
        projects: list,
        currentProject,
        currentView: view,
        // Switching projects keeps the kind of view: jumping to djinn from the
        // board lands on djinn's board.
        goProject: (name) => go(name),
        goView,
        savedViews: savedViews.map((v) => ({ id: v.id, name: v.name, hint: describeView(v) })),
        // Applying a saved view is the backlog's job; the palette asks for it
        // through the URL so there is one code path either way.
        applySavedView: (id) =>
          navigate(`/${encodeURIComponent(currentProject)}/backlog?view=${encodeURIComponent(id)}`),
        commands: commandActions(mounted.paletteCommands, { currentProject, currentView: view, goView }),
        // The views own quick-create. The palette asks for it through the same
        // key the user would press, so there is exactly one code path.
        openCreate: () => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'c' })),
        toggleTheme,
        openHelp: () => setHelp(true),
        openSettings: () => navigate('/settings'),
      }),
    [currentProject, go, goView, list, mounted, navigate, savedViews, toggleTheme, view],
  )

  // Shell-level shortcuts the mounted integrations contribute, e.g. `g r`.
  // An integration the project lacks registers no handler, so its keys do nothing.
  const integrationShortcuts = useMemo<ShortcutHandlers>(
    () =>
      Object.fromEntries(
        mounted.shortcuts.flatMap((s) =>
          s.run ? [[s.shortcut.id, () => s.run?.({ currentProject, goView })]] : [],
        ),
      ),
    [currentProject, goView, mounted],
  )

  useShortcuts({
    'theme.toggle': toggleTheme,
    'help.toggle': () => setHelp((v) => !v),
    'palette.open': () => setPalette((v) => !v),
    ...integrationShortcuts,
    'view.now': () => navigate('/now'),
    'view.initiatives': () => currentProject && go(currentProject, 'initiatives'),
    'view.graph': () => currentProject && go(currentProject, 'graph'),
  })

  return (
    <div className="flex h-screen w-screen overflow-hidden bg-surface text-ink">
      <Sidebar
        projects={list}
        loading={projects.isLoading}
        failed={projects.isError}
        onReorder={reorderProjects}
        view={view}
        currentProject={currentProject}
        theme={theme}
        onToggleTheme={toggleTheme}
        onShowHelp={() => setHelp(true)}
        onOpenPalette={() => setPalette(true)}
        onOpenSettings={() => navigate('/settings')}
      />

      <main className="flex min-w-0 flex-1 flex-col">
        <LabelRulesProvider rules={mounted.labels}>
        {projects.isError ? (
          <ErrorState error={projects.error as ApiError} onRetry={() => void projects.refetch()} />
        ) : (
          <Routes>
            <Route path="/" element={<FirstProject projects={list} loading={projects.isLoading} />} />
            <Route path="/:project/backlog" element={<BacklogView projects={list} />} />
            <Route path="/:project/issue/:id" element={<BacklogView projects={list} />} />
            <Route path="/:project/initiatives" element={<InitiativesView projects={list} />} />
            <Route path="/:project/initiatives/issue/:id" element={<InitiativesView projects={list} />} />
            {/* The weekly-updates sheet is a route so a manager can link one
                epic's history straight to a colleague. */}
            <Route path="/:project/initiatives/:updatesId/updates" element={<InitiativesView projects={list} />} />
            <Route path="/:project/board" element={<BoardView projects={list} />} />
            <Route path="/:project/board/issue/:id" element={<BoardView projects={list} />} />
            {/* Contributed views exist only for a project that mounts them; any
                other project falls through to the not-found route. */}
            {mounted.views.flatMap((v) =>
              v.routes.map((pattern) => (
                <Route key={`${v.id}:${pattern}`} path={`/:project/${pattern}`} element={<v.element projects={list} />} />
              )),
            )}
            <Route path="/:project/graph" element={<GraphView projects={list} />} />
            <Route path="/:project/graph/issue/:id" element={<GraphView projects={list} />} />
            <Route path="/:project/sweep" element={<SweepView projects={list} />} />
            <Route path="/now" element={<NowView projects={list} />} />
            <Route
              path="/settings"
              element={
                <SettingsView
                  projects={list}
                  theme={theme}
                  onSetTheme={(t) => {
                    applyTheme(t)
                    setTheme(t)
                  }}
                />
              }
            />
            <Route path="*" element={<UnknownRoute loading={projects.isLoading} />} />
          </Routes>
        )}
        </LabelRulesProvider>
      </main>

      {palette && (
        <CommandPalette
          actions={actions}
          project={currentProject}
          goIssue={goIssue}
          onClose={() => setPalette(false)}
        />
      )}
      {help && (
        <CheatSheet project={list.find((p) => p.name === currentProject)} onClose={() => setHelp(false)} />
      )}
    </div>
  )
}

/**
 * Where `/` lands.
 *
 * A Settings preference wins; otherwise the first project in config.yaml, which
 * is the one the operator listed first on purpose. With no project at all it
 * says where Loom looked and how to add one.
 */
function FirstProject({
  projects,
  loading,
}: {
  projects: { name: string }[]
  loading: boolean
}) {
  if (loading) return null
  // The saved preference wins, but only while that project still exists.
  const first = resolveStartProject(projects)
  if (!first) return <NoProjectsFound />
  return <Navigate to={`/${encodeURIComponent(first)}/backlog`} replace />
}

function UnknownRoute({ loading }: { loading: boolean }) {
  const params = useParams()
  // Until the projects load, a contributed view's route is not known yet.
  if (loading) return null
  return (
    <EmptyState
      title="Nothing here"
      detail={
        params.project ? (
          <>
            <span className="mono">{params.project}</span> has no view at this address.
          </>
        ) : (
          'That address does not match a Loom view.'
        )
      }
    />
  )
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      {/* Toast wraps Live: integration stream slots raise toasts (djinn's
          run-started and run-finished), so the toast API exists above it. */}
      <ToastProvider>
        <LiveProvider>
          {/* Integration roots sit above the router so their state outlives
              navigation — a route swap unmounts the view, not djinn's queue. */}
          <IntegrationRoots>
            <Router>
              {/* Each root's route part, outside the routes, so it sees every
                  project switch whatever view is open. */}
              <IntegrationRoutes />
              <Shell />
            </Router>
          </IntegrationRoots>
        </LiveProvider>
      </ToastProvider>
    </QueryClientProvider>
  )
}
