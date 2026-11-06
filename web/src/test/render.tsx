import { act, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { vi } from 'vitest'
import { qk } from '@/api/hooks'
import type { Project } from '@/api/types'
import { LabelRulesProvider } from '@/components/labels'
import { LiveProvider } from '@/components/Live'
import { ToastProvider } from '@/components/Toast'
import { IntegrationRoots, mountIntegrations } from '@/integrations'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

export interface Rendered {
  container: HTMLDivElement
  qc: QueryClient
  /** Every request path fetch saw, in order. */
  requests: string[]
  unmount: () => void
}

type Reply = { status: number; body: unknown } | 'pending'

/**
 * Mount `ui` inside the app's providers, with `projects` already in the
 * projects cache (so resolving capabilities costs no request) and fetch
 * answering from `routes` by path. Unknown paths answer 404. The label rules
 * are the current project's, as the shell provides them.
 */
export async function renderWithProjects(
  ui: ReactNode,
  {
    projects,
    current,
    routes = {},
    path = '/',
  }: {
    projects: Project[]
    /** The project whose label rules the shell would provide. */
    current?: string
    routes?: Record<string, () => Reply>
    path?: string
  },
): Promise<Rendered> {
  const requests: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), 'http://loom.test')
      requests.push(url.pathname)
      const reply = routes[url.pathname]?.() ?? { status: 404, body: { error: `no stub for ${url.pathname}` } }
      if (reply === 'pending') return new Promise<Response>(() => undefined)
      return new Response(JSON.stringify(reply.body), {
        status: reply.status,
        headers: { 'Content-Type': 'application/json' },
      })
    }),
  )
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(qk.projects, projects)
  const rules = mountIntegrations(projects.find((p) => p.name === current)).labels
  const container = document.createElement('div')
  document.body.append(container)
  const root: Root = createRoot(container)
  await act(async () => {
    root.render(
      <QueryClientProvider client={qc}>
        <ToastProvider>
          <LiveProvider>
            <IntegrationRoots>
              <MemoryRouter initialEntries={[path]}>
                <LabelRulesProvider rules={rules}>{ui}</LabelRulesProvider>
              </MemoryRouter>
            </IntegrationRoots>
          </LiveProvider>
        </ToastProvider>
      </QueryClientProvider>,
    )
  })
  return {
    container,
    qc,
    requests,
    unmount: () => {
      act(() => root.unmount())
      container.remove()
      vi.unstubAllGlobals()
    },
  }
}

/** Let pending promises and React updates settle. */
export async function settle(ms = 0) {
  await act(async () => {
    await new Promise((resolve) => window.setTimeout(resolve, ms))
  })
}

export async function until(ready: () => boolean) {
  for (let i = 0; i < 50 && !ready(); i++) await settle(10)
}
