import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Issue, IssueList } from './types'
import { BUILTIN_SCHEMA } from './types'
import { ApiError, api } from './client'
import { qk, useIssues, useProjectSchema, useSweep } from './hooks'
import { applyActions } from './sse'
import { actionsForConnect } from '@/lib/sse-events'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

let root: Root | undefined

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = undefined
  vi.restoreAllMocks()
})

/* ── useIssues paging (loom-7qh) ─────────────────────────────────────────── */

const row = (id: string): Issue =>
  ({
    id,
    title: `Issue ${id}`,
    type: 'task',
    status: 'open',
    priority: 2,
    assignee: '',
    labels: [],
    estimate: null,
    created: '2026-08-01T00:00:00Z',
    updated: '2026-08-02T00:00:00Z',
    closed: null,
    due: null,
    deferUntil: null,
    ready: true,
    missingAcceptance: false,
    missingCloseReason: false,
    pinned: false,
    depCounts: { blockedBy: 0, blocks: 0 },
    parent: null,
    childStats: null,
  }) as Issue

const listOf = (ids: string[], total: number): IssueList => ({ issues: ids.map(row), total })

/**
 * Pages answered in the order given. An `ApiError` in the list is the request
 * that fails, so a page-2 failure is expressible without a second harness.
 */
async function renderIssues(
  pages: (IssueList | ApiError)[],
  options?: Parameters<typeof useIssues>[1],
): Promise<{
  result: () => ReturnType<typeof useIssues>
  spy: ReturnType<typeof vi.spyOn>
  rerender: () => void
}> {
  let call = 0
  const spy = vi.spyOn(api, 'issues').mockImplementation(() => {
    const page = pages[Math.min(call, pages.length - 1)]!
    call += 1
    if (page instanceof ApiError) return Promise.reject(page)
    return Promise.resolve(page)
  })
  let latest: ReturnType<typeof useIssues> | undefined
  let rerender = () => {}

  function Harness() {
    const [, tick] = useState(0)
    rerender = () => tick((n) => n + 1)
    latest = useIssues('loom-scale', options)
    return null
  }

  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const node = document.createElement('div')
  root = createRoot(node)
  await act(async () => {
    root?.render(
      <QueryClientProvider client={client}>
        <Harness />
      </QueryClientProvider>,
    )
  })
  await act(async () => {
    await new Promise((resolve) => window.setTimeout(resolve, 10))
  })
  return { result: () => latest!, spy: spy as never, rerender: () => rerender() }
}

describe('useIssues paging', () => {
  it('asks for its own first page rather than inheriting the server default', async () => {
    const { spy } = await renderIssues([listOf(['a'], 1)], { epic: 'scale-001' })

    expect(spy).toHaveBeenCalledWith('loom-scale', { epic: 'scale-001', limit: 200 })
  })

  it('reports rows outstanding when the server total exceeds the page in hand', async () => {
    const { result } = await renderIssues([listOf(['a', 'b'], 312)])

    expect(result().loaded).toBe(2)
    expect(result().total).toBe(312)
    expect(result().hasMore).toBe(true)
  })

  it('has nothing more to load when the page covers the total', async () => {
    const { result } = await renderIssues([listOf(['a', 'b'], 2)])

    expect(result().hasMore).toBe(false)
  })

  it('appends the next page at the offset the rows in hand imply, and keeps both', async () => {
    const { result, spy } = await renderIssues([
      listOf(['a', 'b'], 3),
      listOf(['c'], 3),
    ])
    expect(result().issues.map((i) => i.id)).toEqual(['a', 'b'])

    await act(async () => {
      result().loadMore()
      await new Promise((resolve) => window.setTimeout(resolve, 10))
    })

    expect(spy).toHaveBeenLastCalledWith('loom-scale', { limit: 1000, offset: 2 })
    expect(result().issues.map((i) => i.id)).toEqual(['a', 'b', 'c'])
    expect(result().loaded).toBe(3)
    expect(result().hasMore).toBe(false)
  })

  /** Offset paging has no snapshot: a row can straddle the seam. */
  it('shows a row that arrives on both sides of the page seam only once', async () => {
    const { result } = await renderIssues([listOf(['a', 'b'], 3), listOf(['b', 'c'], 3)])

    await act(async () => {
      result().loadMore()
      await new Promise((resolve) => window.setTimeout(resolve, 10))
    })

    expect(result().issues.map((i) => i.id)).toEqual(['a', 'b', 'c'])
    expect(result().loaded).toBe(3)
  })

  /**
   * `issues` is the dependency of every downstream useMemo in the Backlog and
   * the Board. A new array identity per render would re-run the quick filter,
   * the grouping and the lane build over the whole accumulated set on every
   * keystroke — which at 6,030 rows is the difference between a usable view
   * and a stuttering one.
   */
  it('hands back the same array until the cache actually changes', async () => {
    const { result, rerender } = await renderIssues([listOf(['a', 'b'], 2)])
    const first = result().issues

    await act(async () => rerender())

    expect(result().issues).toBe(first)
  })

  /**
   * A later page failing is a failure of the *paging control*, not of the
   * table: every row already in hand is still true. The Backlog leans on this
   * to keep the loaded rows on screen instead of blanking them behind a
   * full-screen error, so the hook has to tell the two failures apart.
   *
   * 408 rather than a 5xx because the shared `retry` policy declines to retry
   * 4xx — the failure surfaces on the first attempt instead of after two
   * backoff delays.
   */
  it('keeps the rows in hand when a later page fails, and names it a paging failure', async () => {
    const { result } = await renderIssues([
      listOf(['a', 'b'], 3),
      new ApiError('the request timed out', 408),
    ])

    await act(async () => {
      result().loadMore()
      await new Promise((resolve) => window.setTimeout(resolve, 10))
    })

    expect(result().issues.map((i) => i.id)).toEqual(['a', 'b'])
    expect(result().loadMoreError?.message).toBe('the request timed out')
    expect(result().isLoadingMore).toBe(false)
    // The page is still outstanding, so the way to ask for it again survives.
    expect(result().hasMore).toBe(true)
  })

  /** The other half: a first page that fails has left nothing to keep, so it
   *  is not a paging failure and must not be reported as one. */
  it('does not call a first-page failure a paging failure', async () => {
    const { result } = await renderIssues([new ApiError('the request timed out', 408)])

    expect(result().issues).toEqual([])
    expect(result().error?.message).toBe('the request timed out')
    expect(result().loadMoreError).toBeNull()
  })

  it('lets a caller that cannot paginate pin its own page size', async () => {
    const { spy } = await renderIssues([listOf(['a'], 1)], { filter: { limit: 1000 } })

    expect(spy).toHaveBeenCalledWith('loom-scale', { limit: 1000 })
  })
})

describe('useSweep', () => {
  it('waits for an explicit sweep before requesting the project report', async () => {
    const report = {
      sweptAt: '2026-08-04T12:00:00Z',
      branch: 'main',
      window: 500,
      commitsScanned: 42,
      findings: [],
      partial: [],
      counts: {},
    }
    const sweep = vi.spyOn(api, 'sweep').mockResolvedValue(report)
    let query: ReturnType<typeof useSweep> | undefined

    function Harness() {
      query = useSweep('loom')
      return null
    }

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const node = document.createElement('div')
    root = createRoot(node)
    act(() => root?.render(<QueryClientProvider client={client}><Harness /></QueryClientProvider>))

    expect(sweep).not.toHaveBeenCalled()
    expect(query?.isFetched).toBe(false)

    await act(async () => {
      await query?.refetch()
      await new Promise((resolve) => window.setTimeout(resolve, 0))
    })

    expect(sweep).toHaveBeenCalledWith('loom')
    expect(query?.data).toEqual(report)
  })

  it('surfaces a failed manual sweep after one bounded request', async () => {
    const failure = new ApiError('sweep failed', 500, { stderr: 'fatal: bad revision main\n' })
    const sweep = vi.spyOn(api, 'sweep').mockRejectedValue(failure)
    let query: ReturnType<typeof useSweep> | undefined

    function Harness() {
      query = useSweep('loom')
      return null
    }

    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, retryDelay: 0 } },
    })
    const node = document.createElement('div')
    root = createRoot(node)
    act(() => root?.render(<QueryClientProvider client={client}><Harness /></QueryClientProvider>))
    expect(query?.isError).toBe(false)
    expect(query?.error).toBeNull()

    await act(async () => {
      await query?.refetch()
      await new Promise((resolve) => window.setTimeout(resolve, 0))
    })

    expect(sweep).toHaveBeenCalledTimes(1)
    expect(query?.isError).toBe(true)
    expect(query?.error).toBe(failure)
  })
})

/* ── useProjectSchema caching (loom-k7q) ─────────────────────────────────── */

describe('useProjectSchema', () => {
  it('fetches once per project, however many times a menu opens', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL) =>
      new Response(JSON.stringify({ statuses: [], types: ['task', 'rfc'] }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    let seen: string[] = []
    function Menu({ project }: { project: string }) {
      seen = useProjectSchema(project).types
      return null
    }
    let setOpen: (open: { project: string } | null) => void = () => {}
    function Harness() {
      const [open, set] = useState<{ project: string } | null>(null)
      setOpen = set
      return open ? <Menu project={open.project} /> : null
    }

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    root = createRoot(document.createElement('div'))
    await act(async () => {
      root?.render(
        <QueryClientProvider client={client}>
          <Harness />
        </QueryClientProvider>,
      )
    })

    for (let i = 0; i < 50; i++) {
      await act(async () => setOpen({ project: 'loom' }))
      await act(async () => setOpen(null))
    }
    await act(async () => setOpen({ project: 'loom' }))

    const schemaCalls = (project: string) =>
      fetchMock.mock.calls.filter(([url]) => String(url).endsWith(`/api/projects/${project}/schema`)).length
    expect(schemaCalls('loom')).toBe(1)
    expect(seen).toEqual(['task', 'rfc'])
    expect(client.getQueryData(qk.schema('loom'))).toEqual({ statuses: [], types: ['task', 'rfc'] })

    await act(async () => setOpen({ project: 'other' }))
    expect(schemaCalls('other')).toBe(1)
    vi.unstubAllGlobals()
  })

  it('does not ask again on every menu open after the request failed', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL) =>
      new Response(JSON.stringify({ error: 'down' }), {
        status: 503,
        headers: { 'content-type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    let seen: string[] = []
    function Menu() {
      seen = useProjectSchema('loom').types
      return null
    }
    let setOpen: (open: boolean) => void = () => {}
    function Harness() {
      const [open, set] = useState(false)
      setOpen = set
      return open ? <Menu /> : null
    }

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    root = createRoot(document.createElement('div'))
    await act(async () => {
      root?.render(
        <QueryClientProvider client={client}>
          <Harness />
        </QueryClientProvider>,
      )
    })
    for (let i = 0; i < 5; i++) {
      await act(async () => setOpen(true))
      await act(async () => setOpen(false))
    }
    await act(async () => setOpen(true))

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(seen).toEqual(BUILTIN_SCHEMA.types)
    vi.unstubAllGlobals()
  })

  it('asks again after an SSE reconnect, even though no menu was open at the time', async () => {
    let fail = true
    const fetchMock = vi.fn(async (_input: RequestInfo | URL) =>
      fail
        ? new Response(JSON.stringify({ error: 'down' }), {
            status: 503,
            headers: { 'content-type': 'application/json' },
          })
        : new Response(JSON.stringify({ statuses: [], types: ['task', 'rfc'] }), {
            status: 200,
            headers: { 'content-type': 'application/json' },
          }),
    )
    vi.stubGlobal('fetch', fetchMock)

    let seen: string[] = []
    function Menu() {
      seen = useProjectSchema('loom').types
      return null
    }
    let setOpen: (open: boolean) => void = () => {}
    function Harness() {
      const [open, set] = useState(false)
      setOpen = set
      return open ? <Menu /> : null
    }

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    root = createRoot(document.createElement('div'))
    await act(async () => {
      root?.render(
        <QueryClientProvider client={client}>
          <Harness />
        </QueryClientProvider>,
      )
    })
    await act(async () => setOpen(true))
    await act(async () => setOpen(false))
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(client.getQueryState(qk.schema('loom'))?.status).toBe('error')

    fail = false
    await act(async () => applyActions(client, actionsForConnect(true)))
    await act(async () => setOpen(true))

    expect(fetchMock).toHaveBeenCalledTimes(2)
    await vi.waitFor(async () => {
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 0))
      })
      expect(seen).toEqual(['task', 'rfc'])
    })
    expect(client.getQueryData(qk.schema('loom'))).toEqual({ statuses: [], types: ['task', 'rfc'] })
    vi.unstubAllGlobals()
  })
})
