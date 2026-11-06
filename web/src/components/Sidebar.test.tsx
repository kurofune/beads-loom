import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import type { Project } from '@/api/types'
import { CONFIG_PATH } from '@/lib/settings'
import { Sidebar } from './Sidebar'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

vi.mock('./Live', () => ({
  ConnectionIndicator: () => null,
  useActiveRun: () => undefined,
}))

const project = (name: string, source: Project['source']): Project => ({
  name,
  source,
  db: name,
  workdir: `/work/${name}`,
  reachable: true,
  reason: '',
  writable: true,
  writeReason: '',
  integrations: {},
  health: { ready: 1, inProgress: 0, blocked: 0, open: 1, total: 1 },
})

describe('Sidebar project source', () => {
  it('badges only automatically discovered projects', () => {
    const qc = new QueryClient()
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => {
      root.render(
        <QueryClientProvider client={qc}>
          <MemoryRouter initialEntries={['/alpha/backlog']}>
            <Sidebar
              projects={[project('alpha', 'discovered'), project('beta', 'config')]}
              loading={false}
              onReorder={() => undefined}
              view="backlog"
              currentProject=""
              theme="loom-dark"
              onToggleTheme={() => undefined}
              onShowHelp={() => undefined}
              onOpenPalette={() => undefined}
              onOpenSettings={() => undefined}
            />
          </MemoryRouter>
        </QueryClientProvider>,
      )
    })

    const badges = [...container.querySelectorAll('[title="Discovered automatically — manage in Settings"]')]
    expect(badges).toHaveLength(1)
    expect(badges[0]?.textContent).toBe('auto')
    act(() => root.unmount())
  })

  it('exposes a deliberate project-scoped reconciliation sweep link', () => {
    const qc = new QueryClient()
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => {
      root.render(
        <QueryClientProvider client={qc}>
          <MemoryRouter initialEntries={['/alpha/backlog']}>
            <Sidebar
              projects={[project('alpha', 'config')]}
              loading={false}
              onReorder={() => undefined}
              view="backlog"
              currentProject="alpha"
              theme="loom-dark"
              onToggleTheme={() => undefined}
              onShowHelp={() => undefined}
              onOpenPalette={() => undefined}
              onOpenSettings={() => undefined}
            />
          </MemoryRouter>
        </QueryClientProvider>,
      )
    })

    const link = [...container.querySelectorAll('a')].find((candidate) =>
      candidate.textContent?.includes('Reconciliation sweep'),
    )
    expect(link?.getAttribute('href')).toBe('/alpha/sweep')
    act(() => root.unmount())
  })

  it('says no projects were found and names the config path when the list is empty', () => {
    const qc = new QueryClient()
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => {
      root.render(
        <QueryClientProvider client={qc}>
          <MemoryRouter initialEntries={['/']}>
            <Sidebar
              projects={[]}
              loading={false}
              onReorder={() => undefined}
              view="backlog"
              currentProject=""
              theme="loom-dark"
              onToggleTheme={() => undefined}
              onShowHelp={() => undefined}
              onOpenPalette={() => undefined}
              onOpenSettings={() => undefined}
            />
          </MemoryRouter>
        </QueryClientProvider>,
      )
    })

    expect(container.textContent).toContain('No projects found')
    expect(container.textContent).toContain(CONFIG_PATH)
    act(() => root.unmount())
  })
})
