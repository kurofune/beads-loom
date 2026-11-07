import { act, Children, isValidElement, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { DiscoveryStatus } from '@/api/types'
import { PROJECT_SNIPPET } from '@/components/NoProjectsFound'
import { IntegrationRoots, IntegrationRoutes } from '@/integrations'
import App from './App'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

/**
 * The chain of component types from the root down to `target`, or null when
 * `target` never appears. Walking the element tree — instead of mounting it —
 * pins the provider NESTING, which is exactly what loom-p95 was about: the
 * unit suites rebuild an equivalent nesting by hand, so only an assertion on
 * App itself can notice the provider sliding back under the router.
 */
function pathTo(node: ReactNode, target: unknown, trail: unknown[] = []): unknown[] | null {
  if (!isValidElement(node)) return null
  const next = [...trail, node.type]
  if (node.type === target) return next
  for (const child of Children.toArray((node.props as { children?: ReactNode }).children)) {
    const found = pathTo(child, target, next)
    if (found) return found
  }
  return null
}

describe('App wiring (loom-p95)', () => {
  it('mounts the integration roots above the router, so a route swap cannot unmount djinn\'s queue', () => {
    const path = pathTo(App(), BrowserRouter)
    // The router must exist, and the roots must already be on the way down to
    // it — an ancestor, never a descendant or a sibling. IntegrationRoots
    // renders every registered root around its children (integrations tests).
    expect(path).not.toBeNull()
    expect(path).toContain(IntegrationRoots)
  })

  it('mounts the roots\' route parts inside the router, so djinn sees every project switch (loom-a7s)', () => {
    const path = pathTo(App(), IntegrationRoutes)
    expect(path).not.toBeNull()
    expect(path).toContain(BrowserRouter)
  })
})

/**
 * `/` with no project to open. These mount the whole App against a stubbed
 * fetch, so they exercise the real route, the real hooks and the real
 * loading/error branches in front of the empty state.
 */
describe('first run with no projects (loom-9e2)', () => {
  const discovery: DiscoveryStatus = {
    enabled: true,
    roots: ['/home/stranger/tracker', '/home/stranger'],
    depth: 3,
    exclude: [],
    intervalSeconds: 300,
    lastScanAt: null,
    hidden: [],
  }

  type Reply = { status: number; body: unknown } | Promise<never>

  let mounted: Root | undefined
  let container: HTMLDivElement

  afterEach(() => {
    if (mounted) act(() => mounted?.unmount())
    mounted = undefined
    container?.remove()
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  /** Mount a fresh App (its QueryClient is module state) with fetch answering `routes`. */
  async function mountApp(routes: Record<string, () => Reply>) {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const path = new URL(String(input), 'http://loom.test').pathname
        const reply = routes[path]?.() ?? { status: 404, body: { error: `no stub for ${path}` } }
        const { status, body } = await reply
        return new Response(JSON.stringify(body), {
          status,
          headers: { 'Content-Type': 'application/json' },
        })
      }),
    )
    window.history.replaceState(null, '', '/')
    vi.resetModules()
    const { default: FreshApp } = await import('./App')
    container = document.createElement('div')
    document.body.append(container)
    mounted = createRoot(container)
    await act(async () => {
      mounted?.render(<FreshApp />)
    })
  }

  async function settle(ms = 0) {
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, ms))
    })
  }

  async function until(ready: () => boolean) {
    for (let i = 0; i < 50 && !ready(); i++) await settle(10)
  }

  function copyButton(): HTMLButtonElement {
    const found = [...container.querySelectorAll('button')].find((b) => b.title === 'Copy the projects snippet')
    if (!(found instanceof HTMLButtonElement)) throw new Error('Copy button not found')
    return found
  }

  const empty = () => ({
    '/api/projects': () => ({ status: 200, body: [] }),
    '/api/discovery': () => ({ status: 200, body: discovery }),
  })

  it('names the roots it searched, a projects snippet and bd init', async () => {
    await mountApp(empty())
    await until(() => container.querySelector('[aria-label="Discovery roots"]') !== null)

    const main = container.querySelector('main')
    expect(main?.textContent).toContain('No projects found')
    const roots = [...(main?.querySelectorAll('[aria-label="Discovery roots"] li') ?? [])].map((li) => li.textContent)
    expect(roots).toEqual(discovery.roots)
    expect(main?.textContent).toContain('3 levels deep')
    expect(main?.textContent).toContain('bd init')
    const snippet = [...(main?.querySelectorAll('pre') ?? [])].find((pre) => pre.textContent === PROJECT_SNIPPET)
    expect(snippet).toBeTruthy()
    expect(PROJECT_SNIPPET).toMatch(/^projects:\n/)
    expect(PROJECT_SNIPPET).toMatch(/workdir: \/path\/to\/your\/project/)
  })

  it('lists a root once when Loom starts in the home directory, without a duplicate-key warning', async () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    const home = '/home/stranger'
    await mountApp({
      '/api/projects': () => ({ status: 200, body: [] }),
      '/api/discovery': () => ({ status: 200, body: { ...discovery, roots: [home, home] } }),
    })
    await until(() => container.querySelector('[aria-label="Discovery roots"]') !== null)

    const roots = [...container.querySelectorAll('[aria-label="Discovery roots"] li')].map((li) => li.textContent)
    expect(roots).toEqual([home])
    const keyWarnings = error.mock.calls.filter((args) => args.some((a) => String(a).includes('same key')))
    expect(keyWarnings).toEqual([])
    error.mockRestore()
  })

  it('copies the snippet and reads Copied for two seconds', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })
    await mountApp(empty())
    await settle()
    vi.useFakeTimers()

    const button = copyButton()
    // A native, enabled button in the tab order: Tab reaches it and the
    // browser turns Enter into this click (walked for real in the browser).
    expect(button.type).toBe('button')
    expect(button.disabled).toBe(false)
    expect(button.tabIndex).toBe(0)
    button.focus()
    expect(document.activeElement).toBe(button)

    await act(async () => {
      button.click()
      await Promise.resolve()
    })
    expect(writeText).toHaveBeenCalledWith(PROJECT_SNIPPET)
    expect(copyButton().textContent).toBe('Copied')

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1900)
    })
    expect(copyButton().textContent).toBe('Copied')

    await act(async () => {
      await vi.advanceTimersByTimeAsync(200)
    })
    expect(copyButton().textContent).toBe('Copy')
  })

  it('shows no empty state while the project list is still loading', async () => {
    await mountApp({
      '/api/projects': () => new Promise<never>(() => undefined),
      '/api/discovery': () => ({ status: 200, body: discovery }),
    })
    await settle(50)

    expect(container.textContent).not.toContain('No projects found')
  })

  it('shows the error state with Retry, not the empty state, when the list fails', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    await mountApp({
      '/api/projects': () => ({ status: 503, body: { error: 'registry unavailable' } }),
      '/api/discovery': () => ({ status: 200, body: discovery }),
    })
    // useProjects retries a 5xx twice with backoff before it reports the error.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000)
    })

    const main = container.querySelector('main')
    expect(main?.textContent).toContain('HTTP 503')
    expect([...(main?.querySelectorAll('button') ?? [])].some((b) => b.textContent === 'Retry')).toBe(true)
    expect(container.textContent).not.toContain('No projects found')
  })
})
