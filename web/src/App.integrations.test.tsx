import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Issue, IssueList, Project, Run, Stats } from '@/api/types'
import { DJINN, DJINN_NO_DISPATCH, testProject } from '@/test/projects'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

/**
 * The djinn view seam, through the whole App against a stubbed fetch: the
 * real router, the real top bar, the real shortcut binding. A project's
 * `integrations` decides whether the Runs view exists at all.
 */

const RUN: Run = {
  id: 'run-history-1',
  project: 'loom',
  source: 'external',
  startedAt: '2026-10-30T10:00:00Z',
  endedAt: '2026-10-30T10:20:00Z',
  state: 'finished',
  beads: [],
  costSoFar: 1.5,
}

let mounted: Root | undefined
let container: HTMLDivElement
let requests: string[] = []

afterEach(() => {
  if (mounted) act(() => mounted?.unmount())
  mounted = undefined
  container?.remove()
  vi.unstubAllGlobals()
  requests = []
})

async function mountAt(path: string, projects: Project[], extra: (pathname: string) => unknown = () => null) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), 'http://loom.test')
      requests.push(url.pathname)
      const body =
        url.pathname === '/api/projects'
          ? projects
          : url.pathname.endsWith('/x/djinn/runs')
            ? { runs: [RUN] }
            : extra(url.pathname)
      return new Response(JSON.stringify(body ?? { error: `no stub for ${url.pathname}` }), {
        status: body ? 200 : 404,
        headers: { 'Content-Type': 'application/json' },
      })
    }),
  )
  window.history.replaceState(null, '', path)
  vi.resetModules()
  const { default: FreshApp } = await import('./App')
  container = document.createElement('div')
  document.body.append(container)
  mounted = createRoot(container)
  await act(async () => {
    mounted?.render(<FreshApp />)
  })
  // Ready once the projects resolved: the tabs enable, or the route is unknown.
  await until(() => container.querySelector('[role="tab"]:not([disabled])') !== null || text().includes('Nothing here'))
}

async function settle(ms = 0) {
  await act(async () => {
    await new Promise((resolve) => window.setTimeout(resolve, ms))
  })
}

async function until(ready: () => boolean) {
  for (let i = 0; i < 50 && !ready(); i++) await settle(10)
}

const text = () => container.textContent ?? ''
const tabs = () => [...container.querySelectorAll('[role="tab"]')].map((t) => t.textContent)
const currentTab = () => container.querySelector('[role="tab"][aria-current="true"]')?.textContent

async function press(...keys: string[]) {
  for (const key of keys) {
    await act(async () => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }))
    })
  }
  await settle(20)
}

describe('a project without djinn', () => {
  const plain = [testProject('plain')]

  it('renders no Runs tab — absent, not disabled', async () => {
    await mountAt('/plain/backlog', plain)
    expect(tabs()).toEqual(['Initiatives', 'Backlog', 'Board', 'Graph', 'Sweep'])
    expect(container.querySelector('[role="tab"][disabled]')).toBeNull()
  })

  it('does nothing on the g r chord', async () => {
    await mountAt('/plain/backlog', plain)
    await press('g', 'r')
    expect(window.location.pathname).toBe('/plain/backlog')
  })

  it('does nothing on the g r chord from any view', async () => {
    for (const view of ['initiatives', 'board', 'graph', 'sweep']) {
      await mountAt(`/plain/${view}`, plain)
      await press('g', 'r')
      expect(window.location.pathname).toBe(`/plain/${view}`)
      act(() => mounted?.unmount())
      mounted = undefined
      container.remove()
    }
  })

  it('renders the not-found view at /<project>/runs and asks djinn for nothing', async () => {
    await mountAt('/plain/runs', plain)
    await until(() => text().includes('Nothing here'))
    expect(text()).toContain('Nothing here')
    expect(container.querySelector('[role="tablist"]')).toBeNull()
    expect(requests.filter((r) => r.includes('/x/djinn/'))).toEqual([])
  })
})

describe('a djinn project with runs', () => {
  it('renders the Runs tab after Board, and g r goes there', async () => {
    await mountAt('/loom/backlog', [testProject('loom', DJINN)])
    expect(tabs()).toEqual(['Initiatives', 'Backlog', 'Board', 'Runs', 'Graph', 'Sweep'])
    await press('g', 'r')
    expect(window.location.pathname).toBe('/loom/runs')
  })

  it('renders RunsView at /<project>/runs with its history', async () => {
    await mountAt('/loom/runs', [testProject('loom', DJINN)])
    await until(() => text().includes(RUN.id))
    expect(currentTab()).toBe('Runs')
    expect(text()).toContain(RUN.id)
  })

  it('renders RunsView at /<project>/runs/:runId', async () => {
    await mountAt(`/loom/runs/${RUN.id}`, [testProject('loom', DJINN)])
    await until(() => requests.some((r) => r.endsWith(`/x/djinn/runs/${RUN.id}`)))
    expect(currentTab()).toBe('Runs')
    expect(text()).not.toContain('Nothing here')
    expect(requests).toContain(`/api/projects/loom/x/djinn/runs/${RUN.id}`)
  })

  it('lists run history when djinn has runs but cannot dispatch', async () => {
    await mountAt('/loom/runs', [testProject('loom', DJINN_NO_DISPATCH)])
    await until(() => text().includes(RUN.id))
    expect(currentTab()).toBe('Runs')
    expect(text()).toContain(RUN.id)
  })
})

/**
 * The boundary gate's render half: the App built with no integration at all.
 * Core must stand on its own — no integration's words, no integration's
 * shortcut, no request under an integration's `/x/` prefix. The registry
 * module is replaced, so no integration module is even loaded.
 */
describe('the App with zero integrations registered', () => {
  const bare = [testProject('plain', {})]

  const row = (id: string, status: Issue['status']): Issue => ({
    id,
    title: `title ${id}`,
    type: 'task',
    status,
    priority: 2,
    assignee: 'nate',
    labels: [],
    estimate: null,
    created: '2026-10-01T00:00:00Z',
    updated: '2026-10-01T00:00:00Z',
    closed: null,
    due: null,
    deferUntil: null,
    ready: status === 'open',
    missingAcceptance: false,
    missingCloseReason: false,
    pinned: false,
    depCounts: { blockedBy: 0, blocks: 0 },
    parent: null,
    childStats: null,
  })
  const ISSUES: IssueList = {
    issues: [row('plain-1', 'open'), row('plain-2', 'open'), row('plain-3', 'in_progress')],
    total: 3,
  }
  const STATS: Stats = { byStatus: { open: 2, in_progress: 1 }, ready: 2, depBlocked: 0, closedLast7d: 0 }
  const coreData = (pathname: string) =>
    pathname === '/api/projects/plain/issues' ? ISSUES : pathname === '/api/projects/plain/stats' ? STATS : null

  afterEach(() => {
    vi.doUnmock('@/integrations/registry')
    vi.doUnmock('@tanstack/react-virtual')
  })

  async function mountBare(path: string) {
    vi.doMock('@/integrations/registry', () => ({ registeredIntegrations: () => [] }))
    // jsdom has no layout, so the real virtualizer would render no backlog row.
    vi.doMock('@tanstack/react-virtual', () => ({
      useVirtualizer: ({ count }: { count: number }) => ({
        getTotalSize: () => count * 30,
        getVirtualItems: () =>
          Array.from({ length: count }, (_, index) => ({ index, key: index, start: index * 30, size: 30 })),
        scrollToIndex: () => undefined,
      }),
    }))
    await mountAt(path, bare, coreData)
    await until(() => text().includes('title plain-1'))
    expect(text()).toContain('title plain-1')
    const { registeredIntegrations } = await import('@/integrations')
    expect(registeredIntegrations()).toEqual([])
  }

  /**
   * The body's text nodes, one space between each. textContent glues adjacent
   * elements together — the tabs read "BoardRunsGraph" — so a word-boundary
   * pattern run on textContent alone cannot see a Runs tab.
   */
  function spacedText(): string {
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
    const parts: string[] = []
    for (let node = walker.nextNode(); node; node = walker.nextNode()) parts.push(node.nodeValue ?? '')
    return parts.join(' ')
  }

  function expectNoIntegrationText() {
    for (const body of [document.body.textContent ?? '', spacedText()]) {
      for (const banned of [/djinn/i, /summon/i, /\bRuns\b/, /needs-operator/]) {
        expect(body).not.toMatch(banned)
      }
    }
  }

  for (const view of ['backlog', 'board']) {
    it(`renders /plain/${view} and its cheat sheet with no integration text and asks no /x/ route`, async () => {
      await mountBare(`/plain/${view}`)
      expect(currentTab()).toBe(view === 'backlog' ? 'Backlog' : 'Board')
      expectNoIntegrationText()
      await press('?')
      expect(text()).toContain('Keyboard shortcuts')
      expectNoIntegrationText()
      expect(requests.filter((r) => r.includes('/x/'))).toEqual([])
    })
  }

  it('binds no issue.stage shortcut on the backlog or the board', async () => {
    await mountBare('/plain/backlog')
    const { SHORTCUTS } = await import('@/keyboard/registry')
    const { registeredShortcuts } = await import('@/integrations')
    const onSurface = [...SHORTCUTS, ...registeredShortcuts()].filter(
      (s) => !s.surfaces || s.surfaces.includes('backlog') || s.surfaces.includes('board'),
    )
    expect(onSurface.length).toBeGreaterThan(0)
    expect(onSurface.map((s) => s.id)).not.toContain('issue.stage')
  })
})
