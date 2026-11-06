import { act, useEffect } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { qk } from '@/api/hooks'
import type { Project } from '@/api/types'
import { DJINN, testProject } from '@/test/projects'
import { useStaged } from './djinn/staging'
import { IntegrationRoots } from '.'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const STORED = JSON.stringify({ staged: ['loom-a'], dismissed: false, project: 'loom' })

let root: Root | undefined
let mounts = 0

/** Reads the queue through djinn's root, and counts its own mounts. */
function Probe() {
  const staged = useStaged('loom')
  useEffect(() => {
    mounts += 1
  }, [])
  return <div data-testid="staged">{staged.join(',')}</div>
}

function mount(qc: QueryClient): HTMLElement {
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  act(() => {
    root?.render(
      <QueryClientProvider client={qc}>
        <IntegrationRoots>
          <Probe />
        </IntegrationRoots>
      </QueryClientProvider>,
    )
  })
  return container
}

const staged = (container: HTMLElement) => container.querySelector('[data-testid="staged"]')?.textContent

beforeEach(() => {
  mounts = 0
  // A projects query that is not seeded stays pending.
  vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => undefined)))
})

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = undefined
  document.body.replaceChildren()
  sessionStorage.clear()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('IntegrationRoots', () => {
  it('renders djinn\'s root without touching sessionStorage while no project has djinn', () => {
    sessionStorage.setItem('loom.tray', STORED)
    const getItem = vi.spyOn(Storage.prototype, 'getItem')
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    const qc = new QueryClient()
    qc.setQueryData<Project[]>(qk.projects, [testProject('plain')])

    const container = mount(qc)

    expect(staged(container)).toBe('')
    expect(getItem).not.toHaveBeenCalled()
    expect(setItem).not.toHaveBeenCalled()
  })

  it('stays off sessionStorage while the projects are still loading', () => {
    const getItem = vi.spyOn(Storage.prototype, 'getItem')
    const setItem = vi.spyOn(Storage.prototype, 'setItem')

    mount(new QueryClient())

    expect(getItem).not.toHaveBeenCalled()
    expect(setItem).not.toHaveBeenCalled()
  })

  it('restores the stored queue once a djinn project arrives, without remounting what it wraps', async () => {
    sessionStorage.setItem('loom.tray', STORED)
    const qc = new QueryClient()
    const container = mount(qc)
    expect(staged(container)).toBe('')

    // The query cache notifies observers on a timer, so let it fire inside act.
    await act(async () => {
      qc.setQueryData<Project[]>(qk.projects, [testProject('loom', DJINN)])
      await new Promise((resolve) => setTimeout(resolve, 0))
    })

    expect(staged(container)).toBe('loom-a')
    expect(mounts).toBe(1)
  })

  it('restores at first render when a djinn project is already known', () => {
    sessionStorage.setItem('loom.tray', STORED)
    const qc = new QueryClient()
    qc.setQueryData<Project[]>(qk.projects, [testProject('loom', DJINN)])

    expect(staged(mount(qc))).toBe('loom-a')
  })
})
