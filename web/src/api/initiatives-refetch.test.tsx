import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from './client'
import { useInitiatives } from './hooks'
import { useBulkClose } from './mutations'
import { applyActions, useLoomStream } from './sse'
import { ROLLUP_QUIET_MS, initiativesRefetch } from '@/lib/rollup-refetch'
import type { Initiative } from './types'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

/**
 * loom-zx1 — what the board actually costs.
 *
 * loom-g1v.5 mounted `useInitiatives` in BoardView for the close-eligible
 * nudge, and both invalidation paths named the rollup. That turned a burst of N
 * writes into N runs of an unbounded recursive CTE per open board tab. These
 * tests count the requests the burst produces, at the transport seam
 * (`api.initiatives` is the sole caller of
 * `GET /api/projects/{project}/initiatives`, client.ts:241) — not the window
 * constant that produces them.
 */

const initiative = (id: string, closeEligible: boolean): Initiative => ({
  id,
  title: `Initiative ${id}`,
  status: 'open',
  priority: 2,
  assignee: '',
  due: null,
  updated: '2026-08-01T00:00:00Z',
  lastActivity: '2026-08-02T00:00:00Z',
  subtree: { total: 2, closed: 2, started: 0, ready: 0, depBlocked: 0, deferScheduled: 0, chained: 0 },
  closeEligible,
  needsTarget: false,
  health: { state: 'on-track', explanation: 'All descendants closed' },
  noReceipt: { total: 0, noAcceptance: 0, noCloseReason: 0 },
})

let root: Root | undefined

interface Tab {
  /** What the nudge would render: the eligible count this tab currently holds. */
  eligible: () => number
  close: (ids: string[]) => Promise<unknown>
}

/** One open board tab: the rollup query mounted, plus the close the nudge runs. */
async function openBoardTab(qc: QueryClient): Promise<Tab> {
  let latest: Initiative[] = []
  let bulk: ReturnType<typeof useBulkClose> | undefined

  function BoardTab() {
    const { data } = useInitiatives('loom', false)
    latest = data?.initiatives ?? []
    bulk = useBulkClose('loom')
    return null
  }

  const node = document.createElement('div')
  root = createRoot(node)
  await act(async () => {
    root?.render(<QueryClientProvider client={qc}><BoardTab /></QueryClientProvider>)
  })
  // The mount's own read settles a tick later; a tab that has not painted its
  // first rollup yet would make every count below read zero for free.
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0)
  })

  return {
    eligible: () => latest.filter((i) => i.closeEligible).length,
    close: (ids) => bulk!.mutateAsync({ ids, reason: 'all children closed' }),
  }
}

describe('the initiatives rollup under a burst of writes', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    initiativesRefetch.reset()
  })

  afterEach(() => {
    if (root) act(() => root?.unmount())
    root = undefined
    initiativesRefetch.reset()
    vi.useRealTimers()
  })

  it('charges N local writes one refetch per board tab, not N', async () => {
    const fetches = vi
      .spyOn(api, 'initiatives')
      .mockResolvedValue({ initiatives: [initiative('loom-a', false)] })
    vi.spyOn(api, 'bulkClose').mockImplementation(async (_p, body) => ({
      updated: body.ids.length,
      ids: body.ids,
    }))

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const tab = await openBoardTab(qc)
    expect(fetches).toHaveBeenCalledTimes(1) // the mount's own read

    // Eight sequential, non-overlapping writes — the shape WriteCoordinator
    // does NOT coalesce, because each one settles before the next begins.
    const burst = 8
    await act(async () => {
      for (let i = 0; i < burst; i++) await tab.close([`loom-${i}`])
    })
    expect(fetches).toHaveBeenCalledTimes(1)

    await act(async () => {
      await vi.advanceTimersByTimeAsync(ROLLUP_QUIET_MS)
    })

    // One. Before loom-zx1 this was 1 + burst.
    expect(fetches).toHaveBeenCalledTimes(2)
    expect(fetches.mock.calls.length - 1).toBeLessThan(burst)
  })

  it('charges a streamed burst one refetch per board tab, not N', async () => {
    const fetches = vi
      .spyOn(api, 'initiatives')
      .mockResolvedValue({ initiatives: [initiative('loom-a', false)] })

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    await openBoardTab(qc)
    expect(fetches).toHaveBeenCalledTimes(1)

    // Twelve `bead-state` frames — one djinn run working through a molecule.
    await act(async () => {
      for (let i = 0; i < 12; i++) {
        applyActions(qc, [{ kind: 'invalidate-project', project: 'loom' }])
        await vi.advanceTimersByTimeAsync(40)
      }
      await vi.advanceTimersByTimeAsync(ROLLUP_QUIET_MS)
    })

    expect(fetches).toHaveBeenCalledTimes(2)
  })

  it('leaves another project’s rollup alone', async () => {
    const fetches = vi
      .spyOn(api, 'initiatives')
      .mockResolvedValue({ initiatives: [initiative('loom-a', false)] })

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    await openBoardTab(qc)
    fetches.mockClear()

    await act(async () => {
      applyActions(qc, [{ kind: 'invalidate-project', project: 'djinn' }])
      await vi.advanceTimersByTimeAsync(ROLLUP_QUIET_MS * 2)
    })

    expect(fetches).not.toHaveBeenCalled()
  })

  it('does not let the deferred refetch land under an open menu', async () => {
    // loom-323 / SCARS "refetch under the user's hands". The stream's guard
    // parks actions it dispatches, but a coalesced refetch fires from a timer
    // that no longer passes through it — so the window has to read the same
    // busy state. This proves the wiring, not just RollupRefetch's own rule.
    const fetches = vi
      .spyOn(api, 'initiatives')
      .mockResolvedValue({ initiatives: [initiative('loom-a', false)] })

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    let locks = 1

    function BoardTabWithMenu() {
      useInitiatives('loom', false)
      useLoomStream(locks, [], () => {})
      return null
    }

    const node = document.createElement('div')
    root = createRoot(node)
    await act(async () => {
      root?.render(<QueryClientProvider client={qc}><BoardTabWithMenu /></QueryClientProvider>)
    })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    fetches.mockClear()

    // A write lands while the menu is open.
    await act(async () => {
      applyActions(qc, [{ kind: 'invalidate-project', project: 'loom' }])
      await vi.advanceTimersByTimeAsync(ROLLUP_QUIET_MS * 3)
    })
    expect(fetches).not.toHaveBeenCalled()

    // The menu closes. The held refetch was parked, not dropped: it arrives on
    // the next time the window asks, without the operator reloading anything.
    locks = 0
    await act(async () => {
      root?.render(<QueryClientProvider client={qc}><BoardTabWithMenu /></QueryClientProvider>)
    })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(ROLLUP_QUIET_MS * 2)
    })
    expect(fetches).toHaveBeenCalledTimes(1)
  })

  it('still corrects the nudge count after a close, with no manual refresh', async () => {
    // loom-g1v.5 AC7. Rationing the refetch must not turn "updates itself" into
    // "updates when you reload": the count is right one interval after the
    // close the operator performed from the nudge.
    const fetches = vi.spyOn(api, 'initiatives')
    fetches.mockResolvedValue({ initiatives: [initiative('loom-epic', true)] })
    vi.spyOn(api, 'bulkClose').mockImplementation(async (_p, body) => {
      // bd closed the epic; the next rollup no longer reports it eligible.
      fetches.mockResolvedValue({ initiatives: [initiative('loom-epic', false)] })
      return { updated: body.ids.length, ids: body.ids }
    })

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const tab = await openBoardTab(qc)
    expect(tab.eligible()).toBe(1)

    await act(async () => {
      await tab.close(['loom-epic'])
    })

    await act(async () => {
      await vi.advanceTimersByTimeAsync(ROLLUP_QUIET_MS)
    })

    expect(tab.eligible()).toBe(0)
  })
})
