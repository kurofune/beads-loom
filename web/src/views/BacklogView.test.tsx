import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError } from '@/api/client'
import { qk } from '@/api/hooks'
import type { Issue, IssueDetail, IssueList, Project } from '@/api/types'
import { ToastProvider } from '@/components/Toast'
import { IntegrationRoots } from '@/integrations'
import { clearViews, saveView } from '@/lib/saved-views'
import { BacklogView } from './BacklogView'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

vi.mock('@tanstack/react-virtual', () => ({
  useVirtualizer: ({ count }: { count: number }) => ({
    getTotalSize: () => count * 30,
    getVirtualItems: () => Array.from({ length: count }, (_, index) => ({
      index,
      key: index,
      start: index * 30,
      size: 30,
    })),
    scrollToIndex: vi.fn(),
  }),
}))

vi.mock('@/components/DoltSync', () => ({ DoltSync: () => null }))
vi.mock('@/components/Live', () => ({
  useInteractionLock: () => undefined,
  useActiveRun: () => undefined,
}))
vi.mock('@/views/IssueDetail', () => ({ IssueDetail: () => null }))

const project: Project = {
  name: 'loom-scale',
  source: 'config',
  db: 'scale',
  workdir: '/tmp/loom-scale',
  reachable: true,
  reason: '',
  writable: true,
  writeReason: '',
  integrations: {},
  health: { ready: 1, inProgress: 0, blocked: 0, open: 1, total: 1 },
}

const issue = (id: string, parent: Issue['parent']): Issue => ({
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
  parent,
  childStats: null,
})

let root: Root | undefined
/** The rendered tree's cache — the handle a test needs to invalidate a query
 *  the way an external `bd` write does. */
let client: QueryClient | undefined

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = undefined
  client = undefined
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

async function renderScopedBacklog(
  issues: Issue[],
  total: number,
  initial = '/loom-scale/backlog?epic=scale-001',
) {
  return renderPagedBacklog([{ issues, total }], initial)
}

/**
 * The scoped backlog, answering one page per request in the order given —
 * which is what a subtree larger than a page actually looks like (loom-7qh).
 * An `ApiError` in the list is the request that fails.
 */
async function renderPagedBacklog(
  pages: (IssueList | ApiError)[],
  initial = '/loom-scale/backlog?epic=scale-001',
) {
  let call = 0
  vi.spyOn(api, 'issues').mockImplementation(() => {
    const page = pages[Math.min(call++, pages.length - 1)]!
    return page instanceof ApiError ? Promise.reject(page) : Promise.resolve(page)
  })
  vi.spyOn(api, 'issue').mockResolvedValue({
    ...issue('scale-001', null),
    title: 'Scale epic 001',
  } as IssueDetail)
  vi.spyOn(api, 'stats').mockResolvedValue({ byStatus: {}, ready: 1, depBlocked: 0, closedLast7d: 0 })
  vi.spyOn(api, 'stale').mockResolvedValue({ issues: [], days: 14 })

  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  client = qc
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  await act(async () => {
    root?.render(
      <QueryClientProvider client={qc}>
        <ToastProvider>
          <IntegrationRoots>
            <MemoryRouter initialEntries={[initial]}>
              <Routes>
                <Route path="/:project/backlog" element={<BacklogView projects={[project]} />} />
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
  return container
}

describe('BacklogView', () => {
  it('passes a copyable epic-scoped no-receipt URL to the issues API', async () => {
    await renderScopedBacklog([], 0, '/loom-scale/backlog?epic=scale-001&noReceipt=true')

    expect(api.issues).toHaveBeenCalledWith('loom-scale', {
      epic: 'scale-001',
      noReceipt: true,
      limit: 200,
    })
  })

  it('names and clears the scoped no-receipt filter and identifies the missing field per bead', async () => {
    const missing = {
      ...issue('closed-child', { id: 'scale-001', title: 'Scale epic 001' }),
      status: 'closed' as const,
      closed: '2026-08-03T00:00:00Z',
      ready: false,
      missingAcceptance: true,
    }
    const container = await renderScopedBacklog(
      [missing],
      1,
      '/loom-scale/backlog?epic=scale-001&noReceipt=true',
    )
    const indicator = [...container.querySelectorAll('button')].find((button) =>
      button.textContent?.includes('No receipt · Scale epic 001'),
    )
    if (!(indicator instanceof HTMLButtonElement)) throw new Error('no-receipt filter indicator not found')

    expect(indicator.title).toContain('missing acceptance criteria or a close reason')
    expect(container.querySelector('[data-row="closed-child"]')?.textContent).toContain('no acceptance')
    expect(container.querySelector('[data-row="closed-child"]')?.textContent).not.toContain('no close reason')
    expect(container.textContent).toContain('1 issue')

    await act(async () => indicator.click())
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    expect(container.textContent).not.toContain('No receipt ·')
    expect(api.issues).toHaveBeenCalledWith('loom-scale', { limit: 200 })
  })

  it('names an empty no-receipt result instead of showing the generic no-issues state', async () => {
    const container = await renderScopedBacklog(
      [],
      0,
      '/loom-scale/backlog?epic=scale-001&noReceipt=true',
    )

    expect(container.textContent).toContain('No closes without receipts in this epic')
    expect(container.textContent).toContain('missing acceptance criteria or a close reason')
    expect(container.textContent).not.toContain('Nothing open in this project')
    expect(container.textContent).toContain('Show the whole backlog')
  })

  // The filter chip already treats an epic-less noReceipt URL as "all epics", so
  // the empty state must not claim a scope the URL never named.
  it('does not claim an epic scope when the no-receipt URL carries none', async () => {
    const container = await renderScopedBacklog([], 0, '/loom-scale/backlog?noReceipt=true')

    expect(container.textContent).toContain('No closes without receipts')
    expect(container.textContent).not.toContain('in this epic')
    expect(container.textContent).toContain('missing acceptance criteria or a close reason')
    expect(container.textContent).toContain('Show the whole backlog')
  })

  it('sends epic scope to the server and renders the returned page without post-filtering', async () => {
    const grandchild = issue('grandchild', { id: 'missing-parent', title: 'Missing parent' })
    const issuesSpy = vi.spyOn(api, 'issues').mockResolvedValue({ issues: [grandchild], total: 1 })
    const issueSpy = vi.spyOn(api, 'issue').mockResolvedValue({
      ...issue('scale-001', null),
      title: 'Scale epic 001',
    } as IssueDetail)
    vi.spyOn(api, 'stats').mockResolvedValue({ byStatus: {}, ready: 1, depBlocked: 0, closedLast7d: 0 })
    vi.spyOn(api, 'stale').mockResolvedValue({ issues: [], days: 14 })

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const container = document.createElement('div')
    document.body.append(container)
    root = createRoot(container)
    await act(async () => {
      root?.render(
        <QueryClientProvider client={client}>
          <ToastProvider>
            <IntegrationRoots>
              <MemoryRouter initialEntries={['/loom-scale/backlog?epic=scale-001']}>
                <Routes>
                  <Route path="/:project/backlog" element={<BacklogView projects={[project]} />} />
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

    expect(issuesSpy).toHaveBeenCalledWith('loom-scale', { epic: 'scale-001', limit: 200 })
    expect(issueSpy).toHaveBeenCalledWith('loom-scale', 'scale-001')
    expect(container.textContent).toContain('Scale epic 001')
    expect(container.textContent).toContain('Issue grandchild')
  })

  /* — paging (loom-7qh) ————————————————————————————————————————————————— */

  const subtree = (from: number, count: number) =>
    Array.from({ length: count }, (_, index) =>
      issue(`scale-001.${String(from + index).padStart(3, '0')}`, {
        id: 'scale-001',
        title: 'Scale epic 001',
      }),
    )

  it('says the page is loaded, not that the subtree is 200 issues', async () => {
    const container = await renderPagedBacklog([{ issues: subtree(1, 200), total: 312 }])

    expect(container.textContent).toContain('200 of 312 loaded')
  })

  /**
   * The bead itself: an epic whose subtree exceeds one page must expose a way
   * to reach every row, and reaching page 2 must not mean editing the URL.
   */
  it('reaches the rest of an oversized subtree from a control in the view', async () => {
    const container = await renderPagedBacklog([
      { issues: subtree(1, 200), total: 312 },
      { issues: subtree(201, 112), total: 312 },
    ])
    const more = container.querySelector('[data-action="load-more"]')
    if (!(more instanceof HTMLButtonElement)) throw new Error('no way to reach page 2')
    expect(more.textContent).toContain('Load 112 more')

    await act(async () => more.click())
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    // Page 2 was asked for at the offset the rows in hand imply, at the larger
    // page size — and page 1's rows are still there beside page 2's.
    expect(api.issues).toHaveBeenLastCalledWith('loom-scale', {
      epic: 'scale-001',
      limit: 1000,
      offset: 200,
    })
    expect(container.querySelector('[data-row="scale-001.001"]')).not.toBeNull()
    expect(container.querySelector('[data-row="scale-001.312"]')).not.toBeNull()
    // AC3: the footer count and the rows the reader can reach now agree, and
    // the control that got them there is gone because there is nothing left.
    expect(container.textContent).toContain('312 issues')
    expect(container.querySelectorAll('[data-row]')).toHaveLength(312)
    expect(container.querySelector('[data-action="load-more"]')).toBeNull()
  })

  /**
   * loom-rz0 corner 1. A LIMIT/OFFSET seam has no snapshot: page 2 opens on a
   * row page 1 already held, so 312 raw rows dedupe to 311 reachable ones. The
   * footer counts what the table can reach, the offset arithmetic used to
   * count raw rows, and where they disagreed paging stopped — "311 of 312
   * loaded" with the control that would fetch the 312th already gone.
   */
  it('keeps a way to the last row when a page boundary hands back a row already held', async () => {
    const container = await renderPagedBacklog([
      { issues: subtree(1, 200), total: 312 },
      // Re-opens on scale-001.200, page 1's last row: 111 new, 1 duplicate.
      { issues: subtree(200, 112), total: 312 },
      { issues: subtree(312, 1), total: 312 },
    ])
    const more = container.querySelector('[data-action="load-more"]')
    if (!(more instanceof HTMLButtonElement)) throw new Error('no way to reach page 2')

    await act(async () => more.click())
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    // The duplicate is on screen once, and the footer says so — 311 reachable
    // rows of a stated 312. The row the seam displaced is still missing, so
    // the control that reaches it must still be here.
    expect(container.querySelectorAll('[data-row]')).toHaveLength(311)
    expect(container.textContent).toContain('311 of 312 loaded')
    const rest = container.querySelector('[data-action="load-more"]')
    if (!(rest instanceof HTMLButtonElement)) throw new Error('the last row became unreachable')
    expect(rest.textContent).toContain('Load 1 more')

    await act(async () => rest.click())
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    // Asked from the count the footer prints — 311, not the 312 raw rows in
    // hand — and the two converge rather than stopping one short.
    expect(api.issues).toHaveBeenLastCalledWith('loom-scale', {
      epic: 'scale-001',
      limit: 1000,
      offset: 311,
    })
    expect(container.querySelectorAll('[data-row]')).toHaveLength(312)
    expect(container.textContent).toContain('312 issues')
    expect(container.querySelector('[data-action="load-more"]')).toBeNull()
    expect(container.querySelector('[data-action="reach-rest"]')).toBeNull()
  })

  /**
   * And when forward paging genuinely cannot close the gap — every row of the
   * next page is one already in hand — the footer stays honest AND keeps a way
   * out. Letting the control vanish here is the SCARS.md "invisible
   * affordances" failure: it stops the contradiction by hiding the row.
   */
  it('offers a refetch rather than vanishing when paging stalls short of the total', async () => {
    const container = await renderPagedBacklog([
      { issues: subtree(1, 200), total: 312 },
      { issues: subtree(200, 112), total: 312 },
      // Nothing new: asking from the same offset again would only spin.
      { issues: subtree(311, 1), total: 312 },
    ])
    const more = container.querySelector('[data-action="load-more"]')
    if (!(more instanceof HTMLButtonElement)) throw new Error('no way to reach page 2')
    await act(async () => more.click())
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })
    const rest = container.querySelector('[data-action="load-more"]')
    if (!(rest instanceof HTMLButtonElement)) throw new Error('the last row became unreachable')
    await act(async () => rest.click())
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    expect(container.textContent).toContain('311 of 312 loaded')
    const reach = container.querySelector('[data-action="reach-rest"]')
    if (!(reach instanceof HTMLButtonElement)) throw new Error('the outstanding row is unreachable')
    expect(reach.textContent).toContain('Refresh to reach 1 row')

    const before = vi.mocked(api.issues).mock.calls.length
    await act(async () => reach.click())
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })
    expect(vi.mocked(api.issues).mock.calls.length).toBeGreaterThan(before)
  })

  /**
   * loom-rz0 corner 3. The label is clamped to what one request returns and
   * the tooltip on the same control was not, so the unscoped 6,030-row project
   * had a button reading "Load 1,000 more" whose tooltip promised 5,830.
   */
  it('names the same row count in the load-more label and its tooltip', async () => {
    const container = await renderPagedBacklog([{ issues: subtree(1, 200), total: 6030 }])
    const more = container.querySelector('[data-action="load-more"]')
    if (!(more instanceof HTMLButtonElement)) throw new Error('no way to reach page 2')

    expect(more.textContent).toContain('Load 1,000 more')
    expect(more.title).toContain('1,000')
    // The rows outstanding are the footer's sentence, not a promise this
    // control can keep: one click returns at most NEXT_PAGE of them.
    expect(more.title).not.toContain('5,830')
    expect(container.textContent).toContain('200 of 6,030 loaded')
  })

  it('names the same row count when the tooltip is a retry after a failed page', async () => {
    const container = await renderPagedBacklog([
      { issues: subtree(1, 200), total: 6030 },
      new ApiError('the request timed out', 408),
    ])
    const more = container.querySelector('[data-action="load-more"]')
    if (!(more instanceof HTMLButtonElement)) throw new Error('no way to reach page 2')
    await act(async () => more.click())
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    const retry = container.querySelector('[data-action="load-more"]')
    if (!(retry instanceof HTMLButtonElement)) throw new Error('no way to try page 2 again')
    expect(retry.textContent).toContain('load 1,000 more')
    expect(retry.title).toContain('the same 1,000 rows again')
    expect(retry.title).not.toContain('5,830')
  })

  /**
   * loom-rz0 corner 4. The refetch notice describes the BACKLOG query. In raw
   * mode the table under it is showing `bd query` results the failed backlog
   * refetch never touched, so painting "showing the rows we already had" over
   * them describes rows that are not on screen — the same class loom-9st names.
   */
  it('says nothing about a failed backlog refetch while the table shows bd query results', async () => {
    const container = await renderPagedBacklog([
      { issues: subtree(1, 200), total: 312 },
      new ApiError('the request timed out', 408),
    ])
    vi.spyOn(api, 'query').mockResolvedValue({
      issues: subtree(900, 3),
      total: 3,
      query: 'status=open',
    })
    const input = container.querySelector('[aria-label="Filter issues"]')
    if (!(input instanceof HTMLInputElement)) throw new Error('filter input not found')

    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(
        input,
        '>status=open',
      )
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })
    expect(container.textContent).toContain('3 issues')

    // What an external `bd` write does to the backlog cache — and it fails.
    await act(async () => {
      await client?.invalidateQueries({ queryKey: qk.issues('loom-scale', { epic: 'scale-001' }) })
    })
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    // The bd query result is untouched, and nothing claims otherwise over it.
    expect(container.querySelectorAll('[data-row]')).toHaveLength(3)
    expect(container.querySelector('[data-error="refetch"]')).toBeNull()
    expect(container.textContent).not.toContain('Refresh failed')
    expect(container.textContent).not.toContain('showing the rows we already had')
  })

  /* — the id column (loom-w7d) ————————————————————————————————————————— */

  /**
   * The maths lives in `idcolumn.ts` and is tested there. What these two pin is
   * the wiring the unit tests cannot see: that the view actually measures the
   * rows it loaded and publishes the result where the row reads it.
   */
  const idCol = (container: HTMLElement) => {
    const grid = container.querySelector('[role="grid"]')
    if (!(grid instanceof HTMLElement)) throw new Error('no backlog grid')
    return grid.style.getPropertyValue('--id-col')
  }

  it('sizes the id column to the longest id it loaded, not to a fixed width', async () => {
    const short = await renderPagedBacklog(
      [{ issues: [issue('loom-7qh', null)], total: 1 }],
      '/loom-scale/backlog',
    )
    expect(idCol(short)).toBe('8ch')

    act(() => root?.unmount())
    document.body.replaceChildren()

    const long = await renderPagedBacklog(
      [{ issues: [issue('djinn-8ncc0.2.2', null)], total: 1 }],
      '/loom-scale/backlog',
    )
    expect(idCol(long)).toBe('15ch')
  })

  it('widens the id column when a longer id arrives on a later page', async () => {
    const container = await renderPagedBacklog(
      [
        { issues: subtree(1, 200), total: 201 },
        { issues: [issue('djinn-8ncc0.2.2', null)], total: 201 },
      ],
      '/loom-scale/backlog',
    )
    // `scale-001.001` is 13 characters; every row on page 1 is that shape.
    expect(idCol(container)).toBe('13ch')

    const more = container.querySelector('[data-action="load-more"]')
    if (!(more instanceof HTMLButtonElement)) throw new Error('no way to reach page 2')
    await act(async () => more.click())
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    expect(idCol(container)).toBe('15ch')
  })

  /**
   * The full-screen error state is for a view with genuinely nothing to show.
   * A page-2 failure is not that: the 200 rows on screen are still true, and
   * replacing them with an error — whose Retry then refetches every page —
   * costs the reader everything they already had for a failure that touched
   * nothing. It is reported beside the control that caused it instead.
   *
   * 408 rather than a 5xx because the shared `retry` policy declines to retry
   * 4xx, so the failure lands on the first attempt rather than after two
   * backoff delays.
   */
  it('keeps the loaded rows when the next page fails, and reports it beside the control', async () => {
    const container = await renderPagedBacklog([
      { issues: subtree(1, 200), total: 312 },
      new ApiError('the request timed out', 408),
    ])
    const more = container.querySelector('[data-action="load-more"]')
    if (!(more instanceof HTMLButtonElement)) throw new Error('no way to reach page 2')

    await act(async () => more.click())
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    // Everything page 1 loaded is still on screen, under a scope chip that is
    // still telling the truth about those rows.
    expect(container.querySelectorAll('[data-row]')).toHaveLength(200)
    expect(container.querySelector('[data-row="scale-001.001"]')).not.toBeNull()
    expect(container.querySelector('[data-row="scale-001.200"]')).not.toBeNull()
    expect(container.textContent).not.toContain('The request failed')
    expect(
      [...container.querySelectorAll('button')].some((b) => b.title.startsWith('Showing only')),
    ).toBe(true)

    // The failure is named where the click happened.
    const failure = container.querySelector('[data-error="load-more"]')
    expect(failure?.textContent).toContain('the request timed out')

    // And the control is a retry, not a corpse: it asks for the same page.
    const retry = container.querySelector('[data-action="load-more"]')
    if (!(retry instanceof HTMLButtonElement)) throw new Error('no way to try page 2 again')
    expect(retry.disabled).toBe(false)

    await act(async () => retry.click())
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    expect(api.issues).toHaveBeenCalledTimes(3)
    expect(api.issues).toHaveBeenLastCalledWith('loom-scale', {
      epic: 'scale-001',
      limit: 1000,
      offset: 200,
    })
    expect(container.querySelectorAll('[data-row]')).toHaveLength(200)
  })

  /**
   * The third failure, and the quietest one. A full refetch — staleTime
   * expiry, a window-focus refetch, an invalidation after an external `bd`
   * write — can fail over rows already on screen. It is not
   * `isFetchNextPageError`, so the load-more line never sees it; the rows are
   * real, so the error screen must not take them. Left unsaid, a screen of
   * hours-old rows reads exactly like a screen of fresh ones.
   *
   * 408 for the same reason as the page-2 test: the shared retry policy
   * declines 4xx, so the failure lands on the first attempt.
   */
  it('says a background refetch failed rather than passing off the rows it could not replace', async () => {
    const container = await renderPagedBacklog([
      { issues: subtree(1, 200), total: 312 },
      new ApiError('the request timed out', 408),
    ])
    expect(container.querySelectorAll('[data-row]')).toHaveLength(200)
    // Nothing has failed yet, so nothing claims otherwise.
    expect(container.querySelector('[data-error="refetch"]')).toBeNull()

    // What an external `bd` write does to this cache.
    await act(async () => {
      await client?.invalidateQueries({ queryKey: qk.issues('loom-scale', { epic: 'scale-001' }) })
    })
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    // The rows the failed refetch could not replace are all still there —
    // the full-screen error state did not take them.
    expect(container.querySelectorAll('[data-row]')).toHaveLength(200)
    expect(container.querySelector('[data-row="scale-001.001"]')).not.toBeNull()
    expect(container.querySelector('[data-row="scale-001.200"]')).not.toBeNull()
    expect(container.textContent).toContain('200 of 312 loaded')

    // And the failure is stated where the outage banner states its own, not
    // beside a load-more control that no one clicked.
    const notice = container.querySelector('[data-error="refetch"]')
    if (!(notice instanceof HTMLElement)) throw new Error('a failed refetch was reported nowhere')
    expect(notice.textContent).toContain('the request timed out')
    expect(container.querySelector('[data-error="load-more"]')).toBeNull()

    // The notice carries the way to try again, like the stale banner does.
    const again = notice.querySelector('[data-action="refetch"]')
    if (!(again instanceof HTMLButtonElement)) throw new Error('no way to ask again')
    const before = vi.mocked(api.issues).mock.calls.length
    await act(async () => again.click())
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })
    expect(vi.mocked(api.issues).mock.calls.length).toBeGreaterThan(before)
  })

  // The notice is a report of a failure, not furniture: a healthy refetch over
  // the same rows must leave the view saying nothing.
  it('says nothing when a background refetch succeeds', async () => {
    const container = await renderPagedBacklog([
      { issues: subtree(1, 200), total: 312 },
      { issues: subtree(1, 200), total: 312 },
    ])

    await act(async () => {
      await client?.invalidateQueries({ queryKey: qk.issues('loom-scale', { epic: 'scale-001' }) })
    })
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    expect(container.querySelector('[data-error="refetch"]')).toBeNull()
    expect(container.querySelectorAll('[data-row]')).toHaveLength(200)
  })

  it('says the next page is in flight and refuses a second click while it is', async () => {
    let release: (page: IssueList) => void = () => {}
    let call = 0
    vi.spyOn(api, 'issues').mockImplementation(() => {
      if (call++ === 0) return Promise.resolve({ issues: subtree(1, 200), total: 312 })
      return new Promise<IssueList>((resolve) => {
        release = resolve
      })
    })
    vi.spyOn(api, 'issue').mockResolvedValue({
      ...issue('scale-001', null),
      title: 'Scale epic 001',
    } as IssueDetail)
    vi.spyOn(api, 'stats').mockResolvedValue({ byStatus: {}, ready: 1, depBlocked: 0, closedLast7d: 0 })
    vi.spyOn(api, 'stale').mockResolvedValue({ issues: [], days: 14 })

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const container = document.createElement('div')
    document.body.append(container)
    root = createRoot(container)
    await act(async () => {
      root?.render(
        <QueryClientProvider client={client}>
          <ToastProvider>
            <IntegrationRoots>
              <MemoryRouter initialEntries={['/loom-scale/backlog?epic=scale-001']}>
                <Routes>
                  <Route path="/:project/backlog" element={<BacklogView projects={[project]} />} />
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

    const more = container.querySelector('[data-action="load-more"]')
    if (!(more instanceof HTMLButtonElement)) throw new Error('no load-more control')
    await act(async () => {
      more.click()
      await new Promise((resolve) => window.setTimeout(resolve, 0))
    })

    const inFlight = container.querySelector('[data-action="load-more"]')
    if (!(inFlight instanceof HTMLButtonElement)) throw new Error('control vanished mid-flight')
    expect(inFlight.textContent).toContain('Loading…')
    expect(inFlight.disabled).toBe(true)

    await act(async () => {
      release({ issues: subtree(201, 112), total: 312 })
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    expect(api.issues).toHaveBeenCalledTimes(2)
    expect(container.textContent).toContain('312 issues')
  })

  /** `bd query` answers in one shot; this view does not page it, so offering
   *  to fetch a page that does not exist would be a lie. */
  it('offers no paging control over a raw bd query result', async () => {
    const container = await renderPagedBacklog([{ issues: subtree(1, 200), total: 312 }])
    vi.spyOn(api, 'query').mockResolvedValue({
      issues: subtree(1, 3),
      total: 3,
      query: 'status=open',
    })
    const input = container.querySelector('[aria-label="Filter issues"]')
    if (!(input instanceof HTMLInputElement)) throw new Error('filter input not found')

    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(
        input,
        '>status=open',
      )
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    expect(container.querySelector('[data-action="load-more"]')).toBeNull()
    expect(container.textContent).toContain('3 issues')
  })

  it('offers no way to load more once the first page covers the subtree', async () => {
    const container = await renderPagedBacklog([{ issues: subtree(1, 12), total: 12 }])

    expect(container.querySelector('[data-action="load-more"]')).toBeNull()
    expect(container.textContent).toContain('12 issues')
  })

  /**
   * A filter over a partial fetch is the sentence that used to hide the
   * truncation: "1 of 200" reads as a complete answer when 112 rows the filter
   * never looked at are still on the server.
   */
  it('separates what a client filter saw from what the server holds', async () => {
    const page = subtree(1, 200)
    page[137] = { ...page[137]!, title: 'Unique needle' }
    const container = await renderPagedBacklog([{ issues: page, total: 312 }])
    const input = container.querySelector('[aria-label="Filter issues"]')
    if (!(input instanceof HTMLInputElement)) throw new Error('filter input not found')

    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, 'needle')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })

    expect(container.textContent).toContain('1 of 200 loaded · 312 match')
    // The rows the filter has not seen are still reachable from here.
    expect(container.querySelector('[data-action="load-more"]')).not.toBeNull()
  })

  /**
   * loom-9st. The server now rejects a query string it cannot parse (a ';'
   * separator makes net/url drop every parameter), so a scoped view can fail
   * where it used to be answered with the full unscoped backlog. The chip must
   * fail with it: an epic-scope chip rendered above an error claims a filter
   * that no data ever went through — the same silently-wrong affordance, moved
   * one layer up.
   */
  it('drops the epic-scope chip when the scoped query fails, and offers the way out in the error body', async () => {
    vi.spyOn(api, 'issues').mockRejectedValue(
      new ApiError('malformed query string: invalid semicolon separator in query', 400),
    )
    vi.spyOn(api, 'issue').mockResolvedValue({
      ...issue('scale-001', null),
      title: 'Scale epic 001',
    } as IssueDetail)
    vi.spyOn(api, 'stats').mockResolvedValue({ byStatus: {}, ready: 1, depBlocked: 0, closedLast7d: 0 })
    vi.spyOn(api, 'stale').mockResolvedValue({ issues: [], days: 14 })

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const container = document.createElement('div')
    document.body.append(container)
    root = createRoot(container)
    await act(async () => {
      root?.render(
        <QueryClientProvider client={client}>
          <ToastProvider>
            <IntegrationRoots>
              <MemoryRouter initialEntries={['/loom-scale/backlog?epic=scale-001']}>
                <Routes>
                  <Route path="/:project/backlog" element={<BacklogView projects={[project]} />} />
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

    // The body is the error, not rows.
    expect(container.textContent).toContain('HTTP 400')
    expect(container.textContent).toContain('invalid semicolon separator in query')
    expect(container.querySelector('[data-row]')).toBeNull()

    // And the TopBar makes no scope claim over it.
    const chip = [...container.querySelectorAll('button')].find((button) =>
      button.title.startsWith('Showing only'),
    )
    expect(chip).toBeUndefined()
    expect(container.textContent).not.toContain('Scale epic 001')

    // The scope is still in the URL, so the escape hatch has to exist somewhere
    // — it moved into the error body (SCARS: invisible affordances).
    const escape = [...container.querySelectorAll('button')].find(
      (button) => button.textContent === 'Show the whole backlog',
    )
    if (!(escape instanceof HTMLButtonElement)) throw new Error('no way out of the failed scope')

    await act(async () => escape.click())
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })
    expect(api.issues).toHaveBeenLastCalledWith('loom-scale', { limit: 200 })
  })

  // The chip is only suppressed by an error — a healthy scoped page must still
  // announce its scope, or this fix would have deleted the affordance outright.
  it('still shows the epic-scope chip when the scoped query succeeds', async () => {
    const container = await renderScopedBacklog(
      [issue('scale-001.001', { id: 'scale-001', title: 'Scale epic 001' })],
      1,
    )
    const chip = [...container.querySelectorAll('button')].find((button) =>
      button.title.startsWith('Showing only'),
    )
    if (!(chip instanceof HTMLButtonElement)) throw new Error('epic-scope chip not found')
    expect(chip.title).toContain("Scale epic 001's subtree")
  })

  /**
   * Epic grouping reads ancestry from the whole fetched page, and every filter
   * in this view can remove the epic rows while leaving their children on
   * screen — "Leaves" removes them by definition. Grouping the narrowed rows
   * against themselves would put the entire backlog in "No epic", which is a
   * silent collapse rather than a visible break, so the wiring gets a test.
   */
  it('keeps epic groups when the hierarchy mode filters the epic rows away', async () => {
    const epic: Issue = {
      ...issue('scale-001', null),
      title: 'Scale epic 001',
      type: 'epic',
      childStats: { total: 1, closed: 0, started: 0 },
    }
    const container = await renderScopedBacklog(
      [epic, issue('scale-001.001', { id: 'scale-001', title: 'Scale epic 001' })],
      2,
    )
    const byName = (name: string) =>
      [...container.querySelectorAll('button')].find((b) => b.textContent?.trim() === name)

    const epicMode = byName('Epic')
    if (!(epicMode instanceof HTMLButtonElement)) throw new Error('no Epic group mode')
    await act(async () => epicMode.click())

    const header = () =>
      [...container.querySelectorAll('button[aria-expanded]')].find((b) =>
        b.textContent?.includes('Scale epic 001'),
      )
    expect(header()?.textContent).toContain('scale-001')

    const leaves = byName('Leaves')
    if (!(leaves instanceof HTMLButtonElement)) throw new Error('no Leaves hierarchy mode')
    await act(async () => leaves.click())

    // The epic row itself is gone; its child is still under its epic's header.
    expect(container.querySelector('[data-row="scale-001"]')).toBeNull()
    expect(container.querySelector('[data-row="scale-001.001"]')).not.toBeNull()
    expect(header()?.textContent).toContain('scale-001')
    expect(container.textContent).not.toContain('No epic')
  })
})

describe('BacklogView toolbar at narrow widths', () => {
  const longName = 'Everything stale across the orchestration workspace this week'

  afterEach(() => {
    clearViews(project.name)
  })

  /** The TopBar part that holds Backlog's own filter controls. */
  function controlRow(container: HTMLElement): HTMLElement {
    const search = container.querySelector('header input[type="search"]')
    let el = search?.parentElement ?? null
    while (el && el.parentElement?.tagName !== 'HEADER') el = el.parentElement
    if (!el) throw new Error('no filter control row in the top bar')
    return el
  }

  it('wraps whole controls onto new rows instead of squeezing or wrapping their labels', async () => {
    const container = await renderScopedBacklog([], 0, '/loom-scale/backlog')
    const row = controlRow(container)
    const classes = row.className.split(/\s+/)

    expect(classes).toContain('flex-wrap')
    expect(classes).toContain('max-w-full')
    expect(classes).toContain('[&>*]:shrink-0')
    expect(classes).toContain('[&_button]:whitespace-nowrap')
    for (const label of ['Stale', 'Closed', 'Save view']) {
      const button = [...row.querySelectorAll('button')].find((b) => b.textContent === label)
      expect(button, label).toBeDefined()
    }
  })

  it('bounds a long saved-view name to an ellipsis and keeps the full name readable', async () => {
    saveView(project.name, {
      name: longName,
      query: '',
      raw: false,
      group: 'status',
      includeClosed: false,
      staleOnly: true,
      hierarchy: 'all',
    })
    const container = await renderScopedBacklog([], 0, '/loom-scale/backlog')
    const row = controlRow(container)
    const stale = [...row.querySelectorAll('button')].find((b) => b.textContent === 'Stale')
    await act(async () => stale?.click())

    const saved = [...row.querySelectorAll('button')].find((b) => b.title.startsWith('This is the saved view'))
    if (!saved) throw new Error('the saved view did not match the current backlog')
    expect(saved.title).toContain(longName)
    expect(saved.textContent).toContain(longName)
    expect(saved.className.split(/\s+/).some((c) => c.startsWith('max-w-'))).toBe(true)
    const label = saved.querySelector('span')
    expect(label?.className.split(/\s+/)).toContain('truncate')
    expect(label?.textContent).toContain(longName)
  })
})
