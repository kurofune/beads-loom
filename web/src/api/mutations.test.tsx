import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api } from './client'
import { qk, useIssues } from './hooks'
import {
  cachedIssue,
  useApplyFinding,
  useBulkClose,
  useCloseIssue,
  useDismissFinding,
  useHideProject,
  usePatchIssue,
  useRescanDiscovery,
  useUndismissFinding,
  useUnhideProject,
} from './mutations'
import type { DiscoveryStatus, Issue, IssueDetail, IssueList } from './types'
import { ROLLUP_QUIET_MS, initiativesRefetch } from '@/lib/rollup-refetch'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const initial: DiscoveryStatus = {
  enabled: true,
  roots: ['/work'],
  depth: 3,
  exclude: [],
  intervalSeconds: 300,
  lastScanAt: null,
  hidden: [],
}

let root: Root | undefined

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = undefined
})

describe('discovery mutations', () => {
  it('seeds returned status and invalidates the contract cache keys', async () => {
    const hidden = { ...initial, hidden: [{ workdir: '/work/alpha', name: 'alpha' }] }
    vi.spyOn(api, 'discoveryHide').mockResolvedValue(hidden)
    vi.spyOn(api, 'discoveryUnhide').mockResolvedValue(initial)
    vi.spyOn(api, 'discoveryRescan').mockResolvedValue({ added: ['alpha'], removed: [] })

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const invalidate = vi.spyOn(qc, 'invalidateQueries')
    let mutations: {
      hide: ReturnType<typeof useHideProject>
      unhide: ReturnType<typeof useUnhideProject>
      rescan: ReturnType<typeof useRescanDiscovery>
    } | undefined

    function Harness() {
      mutations = {
        hide: useHideProject(),
        unhide: useUnhideProject(),
        rescan: useRescanDiscovery(),
      }
      return null
    }

    const node = document.createElement('div')
    root = createRoot(node)
    act(() => root?.render(<QueryClientProvider client={qc}><Harness /></QueryClientProvider>))

    await act(async () => {
      await mutations!.hide.mutateAsync('/work/alpha')
    })
    expect(qc.getQueryData(qk.discovery)).toEqual(hidden)
    expect(invalidate).toHaveBeenCalledWith({ queryKey: qk.projects })

    invalidate.mockClear()
    await act(async () => {
      await mutations!.unhide.mutateAsync('/work/alpha')
    })
    expect(qc.getQueryData(qk.discovery)).toEqual(initial)
    expect(invalidate).toHaveBeenCalledWith({ queryKey: qk.projects })

    invalidate.mockClear()
    await act(async () => {
      await mutations!.rescan.mutateAsync()
    })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: qk.projects })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: qk.discovery })
  })
})

describe('issue mutations', () => {
  it('invalidates issues at once and the rollup once the burst goes quiet', async () => {
    // loom-zx1: the lists must track the edit the user just made, so they stay
    // immediate. The initiatives rollup is an unbounded recursive CTE the board
    // now mounts, so it waits out the burst — but it must still arrive without
    // a manual refresh, which is the second half of this assertion.
    vi.useFakeTimers({ shouldAdvanceTime: true })
    initiativesRefetch.reset()
    try {
      vi.spyOn(api, 'bulkClose').mockResolvedValue({ updated: 1, ids: ['loom-epic'] })
      const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
      const invalidate = vi.spyOn(qc, 'invalidateQueries')
      let close: ReturnType<typeof useBulkClose> | undefined

      function Harness() {
        close = useBulkClose('loom')
        return null
      }

      const node = document.createElement('div')
      root = createRoot(node)
      act(() => root?.render(<QueryClientProvider client={qc}><Harness /></QueryClientProvider>))

      await act(async () => {
        await close!.mutateAsync({ ids: ['loom-epic'], reason: 'done' })
      })

      expect(invalidate).toHaveBeenCalledWith({ queryKey: ['issues', 'loom'] })
      expect(invalidate).not.toHaveBeenCalledWith({ queryKey: ['initiatives', 'loom'] })

      await act(async () => {
        await vi.advanceTimersByTimeAsync(ROLLUP_QUIET_MS)
      })
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ['initiatives', 'loom'] })
    } finally {
      initiativesRefetch.reset()
      vi.useRealTimers()
    }
  })
})

/**
 * loom-7qh. The backlog list cache holds pages now, not one array. Every
 * optimistic write reaches into that cache by hand, so these two prove the
 * surgery still finds a row — including one that only exists because the
 * reader loaded page two.
 */
describe('optimistic writes over a paged list cache', () => {
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

  /** Populate the cache the way the app does — through `useIssues`, two pages
   *  deep — so this asserts against the real cached shape, not a guess at it. */
  async function twoPages() {
    const pages: IssueList[] = [
      { issues: [row('page1-a')], total: 2 },
      { issues: [row('page2-b')], total: 2 },
    ]
    let call = 0
    vi.spyOn(api, 'issues').mockImplementation(() => Promise.resolve(pages[call++] ?? pages[1]!))

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    let issues: ReturnType<typeof useIssues> | undefined
    let patch: ReturnType<typeof usePatchIssue> | undefined

    function Harness() {
      issues = useIssues('loom')
      patch = usePatchIssue('loom')
      return null
    }

    const node = document.createElement('div')
    root = createRoot(node)
    await act(async () => {
      root?.render(<QueryClientProvider client={qc}><Harness /></QueryClientProvider>)
    })
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 10))
    })
    await act(async () => {
      issues!.loadMore()
      await new Promise((resolve) => window.setTimeout(resolve, 10))
    })
    return { qc, issues: () => issues!, patch: () => patch! }
  }

  it('applies a patch to a row that only page two carries', async () => {
    const { issues, patch } = await twoPages()
    expect(issues().issues.map((i) => i.id)).toEqual(['page1-a', 'page2-b'])

    let resolve: (detail: IssueDetail) => void = () => {}
    vi.spyOn(api, 'patchIssue').mockReturnValue(
      new Promise<IssueDetail>((r) => {
        resolve = r
      }),
    )

    act(() => {
      void patch().mutate({ id: 'page2-b', patch: { priority: 0 } })
    })
    await act(async () => {
      await new Promise((r) => window.setTimeout(r, 0))
    })

    // The guess must be on screen before the server answers — that is the
    // whole point of the optimistic path.
    expect(issues().issues.find((i) => i.id === 'page2-b')?.priority).toBe(0)
    expect(issues().issues.find((i) => i.id === 'page1-a')?.priority).toBe(2)

    await act(async () => {
      resolve({ ...row('page2-b'), priority: 0 } as IssueDetail)
      await new Promise((r) => window.setTimeout(r, 0))
    })
  })

  it('reads a row out of any loaded page for the menus that need it', async () => {
    const { qc } = await twoPages()

    expect(cachedIssue(qc, 'loom', 'page2-b')?.title).toBe('Issue page2-b')
    expect(cachedIssue(qc, 'loom', 'page1-a')?.title).toBe('Issue page1-a')
    expect(cachedIssue(qc, 'loom', 'never-fetched')).toBeUndefined()
  })
})

describe('sweep mutations', () => {
  it('applies one finding by digest and invalidates the sweep report', async () => {
    vi.spyOn(api, 'applyFinding').mockResolvedValue({ applied: true, fingerprint: 'abc123' })
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const invalidate = vi.spyOn(qc, 'invalidateQueries')
    let apply: ReturnType<typeof useApplyFinding> | undefined

    function Harness() {
      apply = useApplyFinding('loom')
      return null
    }

    const node = document.createElement('div')
    root = createRoot(node)
    act(() => root?.render(<QueryClientProvider client={qc}><Harness /></QueryClientProvider>))

    await act(async () => {
      await apply!.mutateAsync({
        fingerprint: 'abc123',
        materialDigest: 'digest-1',
        window: 2000,
        branch: 'release',
      })
    })

    expect(api.applyFinding).toHaveBeenCalledWith('loom', 'abc123', {
      materialDigest: 'digest-1',
      window: 2000,
      branch: 'release',
    })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: qk.sweep('loom') })
  })

  it('dismisses one finding with its digest and note, then invalidates the sweep report', async () => {
    vi.spyOn(api, 'dismissFinding').mockResolvedValue({ dismissed: true, fingerprint: 'abc123' })
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const invalidate = vi.spyOn(qc, 'invalidateQueries')
    let dismiss: ReturnType<typeof useDismissFinding> | undefined

    function Harness() {
      dismiss = useDismissFinding('loom')
      return null
    }

    const node = document.createElement('div')
    root = createRoot(node)
    act(() => root?.render(<QueryClientProvider client={qc}><Harness /></QueryClientProvider>))

    await act(async () => {
      await dismiss!.mutateAsync({
        fingerprint: 'abc123',
        materialDigest: 'digest-1',
        note: 'Accepted exception',
        window: 2000,
        branch: 'release',
      })
    })

    expect(api.dismissFinding).toHaveBeenCalledWith(
      'loom',
      'abc123',
      {
        materialDigest: 'digest-1',
        note: 'Accepted exception',
        window: 2000,
        branch: 'release',
      },
    )
    expect(invalidate).toHaveBeenCalledWith({ queryKey: qk.sweep('loom') })
  })

  it('undismisses one finding and invalidates the sweep report', async () => {
    vi.spyOn(api, 'undismissFinding').mockResolvedValue({ dismissed: false, fingerprint: 'abc123' })
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const invalidate = vi.spyOn(qc, 'invalidateQueries')
    let undismiss: ReturnType<typeof useUndismissFinding> | undefined

    function Harness() {
      undismiss = useUndismissFinding('loom')
      return null
    }

    const node = document.createElement('div')
    root = createRoot(node)
    act(() => root?.render(<QueryClientProvider client={qc}><Harness /></QueryClientProvider>))

    await act(async () => {
      await undismiss!.mutateAsync({ fingerprint: 'abc123' })
    })

    expect(api.undismissFinding).toHaveBeenCalledWith('loom', 'abc123')
    expect(invalidate).toHaveBeenCalledWith({ queryKey: qk.sweep('loom') })
  })
})

describe('a refused write rolls back the open detail sheet', () => {
  const detail = { id: 'loom-7', title: 'Claimed', status: 'in_progress', priority: 2, ready: false } as IssueDetail

  function harness() {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    qc.setQueryData(qk.issue('loom', 'loom-7'), detail)
    let close: ReturnType<typeof useCloseIssue> | undefined
    let patch: ReturnType<typeof usePatchIssue> | undefined
    function Harness() {
      close = useCloseIssue('loom')
      patch = usePatchIssue('loom')
      return null
    }
    const node = document.createElement('div')
    root = createRoot(node)
    act(() => root?.render(<QueryClientProvider client={qc}><Harness /></QueryClientProvider>))
    return { qc, close: () => close!, patch: () => patch! }
  }

  it('a close bd refuses for another actor’s claim leaves the sheet in_progress, not closed', async () => {
    const { qc, close } = harness()
    vi.spyOn(api, 'closeIssue').mockRejectedValue(
      new ApiError('loom-7 is claimed by alice', 409, { body: { assignee: 'alice' } }),
    )

    await act(async () => {
      await close().mutateAsync({ id: 'loom-7', reason: 'done' }).catch(() => {})
    })

    expect(qc.getQueryData<IssueDetail>(qk.issue('loom', 'loom-7'))?.status).toBe('in_progress')
  })

  it('a refused patch puts the sheet back as it was', async () => {
    const { qc, patch } = harness()
    vi.spyOn(api, 'patchIssue').mockRejectedValue(new ApiError('bd update failed', 422))

    await act(async () => {
      await patch().mutateAsync({ id: 'loom-7', patch: { priority: 0 } }).catch(() => {})
    })

    expect(qc.getQueryData<IssueDetail>(qk.issue('loom', 'loom-7'))?.priority).toBe(2)
  })
})

describe('a refused write rolls back only its own optimistic guess', () => {
  const a = { id: 'loom-7', title: 'Claimed', status: 'in_progress', priority: 2, ready: false } as IssueDetail
  const b = { id: 'loom-8', title: 'Other', status: 'open', priority: 2, ready: true } as IssueDetail

  function harness() {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    qc.setQueryData(qk.issue('loom', a.id), a)
    qc.setQueryData(qk.issue('loom', b.id), b)
    let close: ReturnType<typeof useCloseIssue> | undefined
    let patch: ReturnType<typeof usePatchIssue> | undefined
    function Harness() {
      close = useCloseIssue('loom')
      patch = usePatchIssue('loom')
      return null
    }
    const node = document.createElement('div')
    root = createRoot(node)
    act(() => root?.render(<QueryClientProvider client={qc}><Harness /></QueryClientProvider>))
    return { qc, close: () => close!, patch: () => patch! }
  }

  function heldClose() {
    let refuse: (err: unknown) => void = () => {}
    vi.spyOn(api, 'closeIssue').mockReturnValue(
      new Promise<IssueDetail>((_resolve, reject) => {
        refuse = reject
      }),
    )
    return (err: unknown) => refuse(err)
  }

  it('a successful edit to another issue survives a close that bd refuses while the edit ran', async () => {
    const { qc, close, patch } = harness()
    const refuse = heldClose()
    vi.spyOn(api, 'patchIssue').mockResolvedValue({ ...b, priority: 0 })

    let closing: Promise<unknown> = Promise.resolve()
    await act(async () => {
      closing = close().mutateAsync({ id: a.id, reason: 'done' }).catch(() => {})
    })
    await act(async () => {
      await patch().mutateAsync({ id: b.id, patch: { priority: 0 } })
    })
    await act(async () => {
      refuse(new ApiError('loom-7 is claimed by alice', 409, { body: { assignee: 'alice' } }))
      await closing
    })

    expect(qc.getQueryData<IssueDetail>(qk.issue('loom', b.id))?.priority).toBe(0)
    expect(qc.getQueryData<IssueDetail>(qk.issue('loom', a.id))?.status).toBe('in_progress')
  })

  it('a refused close does not overwrite a newer edit to its own sheet', async () => {
    const { qc, close, patch } = harness()
    const refuse = heldClose()
    vi.spyOn(api, 'patchIssue').mockResolvedValue({ ...a, priority: 0 })

    let closing: Promise<unknown> = Promise.resolve()
    await act(async () => {
      closing = close().mutateAsync({ id: a.id, reason: 'done' }).catch(() => {})
    })
    await act(async () => {
      await patch().mutateAsync({ id: a.id, patch: { priority: 0 } })
    })
    const invalidate = vi.spyOn(qc, 'invalidateQueries')
    await act(async () => {
      refuse(new ApiError('loom-7 is claimed by alice', 409, { body: { assignee: 'alice' } }))
      await closing
    })

    expect(qc.getQueryData<IssueDetail>(qk.issue('loom', a.id))?.priority).toBe(0)
    expect(invalidate).toHaveBeenCalledWith({ queryKey: qk.issue('loom', a.id), exact: true })
  })
})
