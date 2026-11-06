import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/api/client'
import { qk } from '@/api/hooks'
import type { DiscoveryStatus, Project } from '@/api/types'
import { CONFIG_PATH } from '@/lib/settings'
import { ProjectDiscoverySection } from './SettingsView'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const disabled: DiscoveryStatus = {
  enabled: false,
  roots: [],
  depth: 3,
  exclude: [],
  intervalSeconds: 300,
  lastScanAt: null,
  hidden: [],
}

const enabled: DiscoveryStatus = {
  ...disabled,
  enabled: true,
  roots: ['/work', '/other'],
  hidden: [{ name: 'hidden-one', workdir: '/work/hidden-one' }],
}

const discovered: Project = {
  name: 'alpha',
  source: 'discovered',
  db: 'alpha',
  workdir: '/work/alpha',
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
})

function renderDiscovery(status: DiscoveryStatus, projects: Project[] = []) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(qk.discovery, status)
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  act(() => {
    root?.render(
      <QueryClientProvider client={qc}>
        <ProjectDiscoverySection projects={projects} />
      </QueryClientProvider>,
    )
  })
  return { container, qc }
}

function button(container: HTMLElement, label: string): HTMLButtonElement {
  const found = [...container.querySelectorAll('button')].find((candidate) =>
    candidate.textContent?.includes(label),
  )
  if (!(found instanceof HTMLButtonElement)) throw new Error(`button ${label} not found`)
  return found
}

describe('Project discovery settings', () => {
  it('shows a copyable YAML block when discovery is disabled', () => {
    const { container } = renderDiscovery(disabled)

    expect(container.textContent).toContain('Automatic discovery is disabled')
    expect(container.textContent).toContain('discovery:')
    expect(container.textContent).toContain('enabled: true')
    expect(container.textContent).toContain('~/Projects')
    expect(button(container, 'copy').title).toBe('Copy discovery YAML')
  })

  it('renders the enabled summary, discovered projects, and hidden projects', () => {
    const { container } = renderDiscovery(enabled, [discovered])

    expect(container.textContent).toContain('/work, /other')
    expect(container.textContent).toContain('every 5m')
    expect(container.textContent).toContain('never')
    expect(container.textContent).toContain('alpha')
    expect(container.textContent).toContain('/work/alpha')
    expect(container.textContent).toContain('/work/hidden-one')
    expect(button(container, 'Hide')).toBeTruthy()
    expect(button(container, 'Unhide')).toBeTruthy()
  })

  it('shows empty discovery copy explicitly', () => {
    const { container } = renderDiscovery({ ...enabled, hidden: [] })
    expect(container.textContent).toContain('No projects found')
    expect(container.textContent).toContain(CONFIG_PATH)
    expect(container.textContent).toContain('No hidden projects.')
  })

  it('shows a busy rescan state and reports the result', async () => {
    let finish!: (result: { added: string[]; removed: string[] }) => void
    vi.spyOn(api, 'discoveryRescan').mockReturnValue(
      new Promise((resolve) => {
        finish = resolve
      }),
    )
    vi.spyOn(api, 'discovery').mockResolvedValue(enabled)
    const { container } = renderDiscovery(enabled)

    await act(async () => {
      button(container, 'Rescan now').click()
      await new Promise((resolve) => window.setTimeout(resolve, 0))
    })
    expect(container.textContent).toContain('Scanning…')

    await act(async () => {
      finish({ added: ['alpha', 'beta'], removed: [] })
      await new Promise((resolve) => window.setTimeout(resolve, 60))
    })
    expect(container.textContent).toContain('2 added, 0 removed')
  })
})
