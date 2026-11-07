import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '@/api/client'
import type { Project } from '@/api/types'
import { ToastProvider } from '@/components/Toast'
import { IntegrationRoots } from '@/integrations'
import { GraphView } from './GraphView'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

vi.mock('@/components/DoltSync', () => ({ DoltSync: () => null }))
vi.mock('@/components/Live', () => ({
  useInteractionLock: () => undefined,
  useActiveRun: () => undefined,
}))
vi.mock('@/views/IssueDetail', () => ({ IssueDetail: () => null }))

const project: Project = {
  name: 'loom',
  source: 'config',
  db: 'loom',
  workdir: '/work/loom',
  reachable: true,
  reason: '',
  writable: true,
  writeReason: '',
  integrations: {},
  health: { ready: 0, inProgress: 0, blocked: 0, open: 0, total: 0 },
}

let root: Root | undefined

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = undefined
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

function LocationProbe() {
  const location = useLocation()
  return <output data-location>{location.pathname}{location.search}</output>
}

async function renderGraph(initial: string) {
  const graph = vi.spyOn(api, 'graph').mockResolvedValue({ nodes: [], edges: [] })
  vi.spyOn(api, 'issues').mockResolvedValue({ issues: [], total: 0 })
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  await act(async () => {
    root?.render(
      <QueryClientProvider client={qc}>
        <ToastProvider>
          <IntegrationRoots>
            <MemoryRouter initialEntries={[initial]}>
              <LocationProbe />
              <Routes>
                <Route path="/:project/graph" element={<GraphView projects={[project]} />} />
              </Routes>
            </MemoryRouter>
          </IntegrationRoots>
        </ToastProvider>
      </QueryClientProvider>,
    )
  })
  await act(async () => {
    await new Promise((resolve) => window.setTimeout(resolve, 20))
  })
  const toggle = [...container.querySelectorAll('button')].find((button) => button.textContent === 'Closed')
  if (!(toggle instanceof HTMLButtonElement)) throw new Error('Closed toggle not found')
  return { container, graph, toggle }
}

it('shows closed issues when the URL asks, as the Initiatives chained drill does', async () => {
  const { graph, toggle } = await renderGraph('/loom/graph?epic=epic-a&closed=1')

  expect(toggle.getAttribute('aria-pressed')).toBe('true')
  expect(graph).toHaveBeenLastCalledWith('loom', { epic: 'epic-a', includeClosed: true })
})

it('hides closed issues by default and keeps the toggle in the URL', async () => {
  const { container, graph, toggle } = await renderGraph('/loom/graph?epic=epic-a')

  expect(toggle.getAttribute('aria-pressed')).toBe('false')
  expect(graph).toHaveBeenLastCalledWith('loom', { epic: 'epic-a', includeClosed: false })

  await act(async () => toggle.click())
  expect(container.querySelector('[data-location]')?.textContent).toBe('/loom/graph?epic=epic-a&closed=1')
  expect(graph).toHaveBeenLastCalledWith('loom', { epic: 'epic-a', includeClosed: true })

  await act(async () => toggle.click())
  expect(container.querySelector('[data-location]')?.textContent).toBe('/loom/graph?epic=epic-a')
})
