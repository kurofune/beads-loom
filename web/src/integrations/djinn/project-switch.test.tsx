import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { MemoryRouter, Route, Routes, useNavigate, useParams, type NavigateFunction } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { qk } from '@/api/hooks'
import type { IssueDetail, Project } from '@/api/types'
import { LiveProvider } from '@/components/Live'
import { ToastProvider } from '@/components/Toast'
import { IntegrationRoots, IntegrationRoutes, useIntegrations } from '@/integrations'
import { DJINN, testProject } from '@/test/projects'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const PROJECTS: Project[] = [testProject('alpha', DJINN), testProject('plain')]

const issue = (id: string): IssueDetail => ({ id, title: `title ${id}`, status: 'open' }) as IssueDetail

let root: Root | undefined
let navigate: NavigateFunction

/**
 * The graph's issue sheet reduced to its action row: whatever issue actions
 * the URL's project mounts, and nothing else. No workspace mounts here, so the
 * only thing that can see a project switch is what the integration roots and
 * routes mount on their own.
 */
function GraphSheet() {
  const { project = '', id = '' } = useParams()
  const mounted = useIntegrations(project)
  return (
    <div data-testid="sheet">
      {mounted.issueActions.map((action) => (
        <action.component key={action.id} project={project} issue={issue(id)} />
      ))}
    </div>
  )
}

function Navigator() {
  navigate = useNavigate()
  return null
}

function mount(path: string): HTMLElement {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(qk.projects, PROJECTS)
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  act(() => {
    root?.render(
      <QueryClientProvider client={qc}>
        <ToastProvider>
          <LiveProvider>
            <IntegrationRoots>
              <MemoryRouter initialEntries={[path]}>
                <IntegrationRoutes />
                <Navigator />
                <Routes>
                  <Route path="/:project/graph/issue/:id" element={<GraphSheet />} />
                </Routes>
              </MemoryRouter>
            </IntegrationRoots>
          </LiveProvider>
        </ToastProvider>
      </QueryClientProvider>,
    )
  })
  return container
}

function unmount() {
  act(() => root?.unmount())
  root = undefined
  document.body.replaceChildren()
}

const go = (path: string) => act(() => void navigate(path))
const stageButton = (c: HTMLElement) =>
  [...c.querySelectorAll('button')].find((b) => b.textContent === 'Stage for djinn')
const queued = (c: HTMLElement) => c.textContent?.includes('queued for djinn') ?? false
const storedStaged = (): unknown => {
  const raw = sessionStorage.getItem('loom.tray')
  return raw === null ? null : (JSON.parse(raw) as { staged: unknown }).staged
}

/** Stage shared-1 from alpha's graph sheet, the way the operator would. */
function stageInAlpha(): HTMLElement {
  const container = mount('/alpha/graph/issue/shared-1')
  const button = stageButton(container)
  expect(button).toBeDefined()
  act(() => button?.click())
  expect(queued(container)).toBe(true)
  expect(storedStaged()).toEqual(['shared-1'])
  return container
}

beforeEach(() => {
  // Every query here is seeded; anything else stays pending rather than erroring.
  vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => undefined)))
})

afterEach(() => {
  if (root) unmount()
  sessionStorage.clear()
  vi.unstubAllGlobals()
})

describe('a project switch with no staging control mounted (loom-a7s)', () => {
  it('shows nothing staged back in alpha after a trip through a project without djinn', () => {
    const container = stageInAlpha()

    go('/plain/graph/issue/shared-1')
    // The plain project mounts no issue action: nothing on screen reads the tray.
    expect(stageButton(container)).toBeUndefined()
    expect(queued(container)).toBe(false)

    go('/alpha/graph/issue/shared-1')
    expect(queued(container)).toBe(false)
    expect(stageButton(container)).toBeDefined()
  })

  it('leaves no staged ids for alpha in sessionStorage after the switch', () => {
    stageInAlpha()

    go('/plain/graph/issue/shared-1')
    go('/alpha/graph/issue/shared-1')

    expect(storedStaged()).toEqual([])
  })

  it('keeps shared-1 staged across a reload with no project switch', () => {
    stageInAlpha()
    unmount()

    const container = mount('/alpha/graph/issue/shared-1')

    expect(queued(container)).toBe(true)
    expect(storedStaged()).toEqual(['shared-1'])
  })
})
