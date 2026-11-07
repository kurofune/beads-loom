import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError } from '@/api/client'
import { qk } from '@/api/hooks'
import type { Initiative, Project } from '@/api/types'
import { ToastProvider } from '@/components/Toast'
import { IntegrationRoots } from '@/integrations'
import { INITIATIVE_COLUMNS, InitiativesView } from './InitiativesView'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

vi.mock('@tanstack/react-virtual', () => ({
  useVirtualizer: ({ count }: { count: number }) => ({
    getTotalSize: () => count * 42,
    getVirtualItems: () => Array.from({ length: count }, (_, index) => ({
      index,
      key: index,
      start: index * 42,
      size: 42,
    })),
    scrollToIndex: vi.fn(),
  }),
}))

vi.mock('@/components/DoltSync', () => ({ DoltSync: () => null }))
vi.mock('@/components/Live', () => ({
  useInteractionLock: () => undefined,
  useActiveRun: () => undefined,
}))
vi.mock('@/views/IssueDetail', () => ({ IssueDetail: () => <div data-testid="issue-detail" /> }))
// The sheet is a stub unless a test opts into the real one, which is how the
// direct-URL empty state is asserted through this view's own route.
const sheet = vi.hoisted(() => ({ real: false }))
vi.mock('@/views/InitiativeUpdatesSheet', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/views/InitiativeUpdatesSheet')>()
  return {
    InitiativeUpdatesSheet: (props: Parameters<typeof actual.InitiativeUpdatesSheet>[0]) =>
      sheet.real ? (
        <actual.InitiativeUpdatesSheet {...props} />
      ) : (
        <div data-testid="updates-sheet" data-epic={props.epic} />
      ),
  }
})

const project: Project = {
  name: 'loom',
  source: 'config',
  db: 'loom',
  workdir: '/work/loom',
  reachable: true,
  reason: '',
  writable: true,
  writeReason: '',
  integrations: {},
  health: { ready: 2, inProgress: 0, blocked: 1, open: 3, total: 3 },
}

const initiative = (id: string, values: Partial<Initiative> = {}): Initiative => ({
  id,
  title: `Initiative ${id}`,
  status: 'open',
  priority: 2,
  assignee: '',
  due: null,
  updated: '2026-08-01T00:00:00Z',
  lastActivity: '2026-08-02T00:00:00Z',
  subtree: { total: 4, closed: 1, started: 0, ready: 2, depBlocked: 1, deferScheduled: 0, chained: 0 },
  closeEligible: false,
  needsTarget: false,
  health: { state: 'at-risk', explanation: 'closed 1 in 14d, 3 remain, due in 10d' },
  ...values,
  noReceipt: values.noReceipt ?? { total: 0, noAcceptance: 0, noCloseReason: 0 },
})

let root: Root | undefined

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = undefined
  document.body.replaceChildren()
  localStorage.clear()
  sheet.real = false
})

// LocationProbe reports the current route and offers the browser's Back button,
// which is the only way to see how many history entries a click actually pushed:
// two pushes of the same path leave the URL identical either way.
function LocationProbe() {
  const location = useLocation()
  const navigate = useNavigate()
  return (
    <>
      <output data-location>{location.pathname}{location.search}</output>
      <button type="button" data-back onClick={() => navigate(-1)}>back</button>
      {/* A project switch that does NOT remount the view — the route element is
          the same component at the same position, so its state survives. That
          is exactly the case where per-project state leaks across projects. */}
      <button type="button" data-goto onClick={() => navigate(`/${SWITCH_TO}/initiatives`)}>
        switch project
      </button>
    </>
  )
}

/** The project `[data-goto]` navigates to. */
const SWITCH_TO = 'other'

async function renderView(
  rows: Initiative[],
  initial = '/loom/initiatives',
  qc?: QueryClient,
) {
  const initiativesSpy = vi.spyOn(api, 'initiatives').mockResolvedValue({ initiatives: rows })
  if (!vi.isMockFunction(api.initiativeUpdateSummaries)) {
    vi.spyOn(api, 'initiativeUpdateSummaries').mockResolvedValue({ updates: [] })
  }
  const rendered = await renderInitiatives({ initial, ...(qc ? { qc } : {}) })
  return { ...rendered, initiativesSpy }
}

async function renderInitiatives({
  projects = [project],
  initial = '/loom/initiatives',
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } }),
  settle = true,
}: {
  projects?: Project[]
  initial?: string
  qc?: QueryClient
  settle?: boolean
} = {}) {
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  await act(async () => {
    root?.render(
      <QueryClientProvider client={qc}>
        <ToastProvider>
          <IntegrationRoots>
            <MemoryRouter initialEntries={[initial]}>
              <LocationProbe />
              <Routes>
                <Route path="/:project/initiatives" element={<InitiativesView projects={projects} />} />
                <Route path="/:project/initiatives/issue/:id" element={<InitiativesView projects={projects} />} />
                <Route path="/:project/initiatives/:updatesId/updates" element={<InitiativesView projects={projects} />} />
                <Route path="/:project/backlog" element={<div>Backlog route</div>} />
                <Route path="/:project/graph" element={<div>Graph route</div>} />
              </Routes>
            </MemoryRouter>
          </IntegrationRoots>
        </ToastProvider>
      </QueryClientProvider>,
    )
  })
  if (settle) {
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })
  }
  return { container, qc }
}

function location(container: HTMLElement): string {
  return container.querySelector('[data-location]')?.textContent ?? ''
}

function renderedRowIDs(container: HTMLElement): string[] {
  return [...container.querySelectorAll<HTMLElement>('[data-row]')].map((row) => row.dataset.row ?? '')
}

const UPDATES_SUMMARY = {
  epic: 'epic-updates',
  total: 12,
  draftCount: 2,
  latestWeek: '2026-W32',
  latestStatus: 'draft',
}

function columnHeaders(container: HTMLElement): string[] {
  return [...container.querySelectorAll('[role="columnheader"]')].map((header) => {
    const label = header.cloneNode(true) as HTMLElement
    label.querySelectorAll('[role="tooltip"]').forEach((tooltip) => tooltip.remove())
    return label.textContent ?? ''
  })
}

describe('InitiativesView updates column', () => {
  it('renders no Updates column or chip when the project stores no updates', async () => {
    const { container } = await renderView([initiative('epic-a'), initiative('epic-b')])

    expect(renderedRowIDs(container)).toEqual(['epic-a', 'epic-b'])
    expect(columnHeaders(container)).not.toContain('Updates')
    expect(columnHeaders(container)).toHaveLength(INITIATIVE_COLUMNS.length - 1)
    for (const row of container.querySelectorAll('[data-row]')) {
      // Every row has one cell per visible header, so no row keeps an empty
      // Updates cell that would shift its columns out from under the header.
      expect(row.querySelectorAll('[role="gridcell"]')).toHaveLength(INITIATIVE_COLUMNS.length - 1)
      expect(row.querySelector('[aria-label^="Weekly updates"]')).toBeNull()
    }
  })

  it('shows the Updates column once one update is stored, and the chip opens the sheet', async () => {
    // The state right after the README's one `bd kv set`.
    vi.spyOn(api, 'initiativeUpdateSummaries').mockResolvedValue({
      updates: [{ epic: 'epic-new', total: 1, draftCount: 1, latestWeek: '2026-W44', latestStatus: 'draft' }],
    })
    const { container } = await renderView([initiative('epic-new'), initiative('epic-other')])

    expect(columnHeaders(container)).toEqual(INITIATIVE_COLUMNS.map((column) => column.label))
    const index = INITIATIVE_COLUMNS.findIndex((column) => column.id === 'updates')
    const chip = container
      .querySelector('[data-row="epic-new"]')
      ?.querySelectorAll('[role="gridcell"]')[index]
      ?.querySelector('button')
    if (!(chip instanceof HTMLButtonElement)) throw new Error('updates chip not found')
    expect(chip.textContent).toBe('W44 draft')
    expect(chip.type).toBe('button')
    expect(
      container.querySelector('[data-row="epic-other"]')?.querySelectorAll('[role="gridcell"]')[index]?.textContent,
    ).toBe('')

    // Enter on the focused chip is the button's own activation: the grid's
    // Enter shortcut (open the focused row's issue) must not take it, even
    // with that row focused. jsdom does not turn Enter into a click, so the
    // click that follows stands in for the browser's activation.
    act(() => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'j' })))
    act(() => chip.focus())
    expect(document.activeElement).toBe(chip)
    act(() => {
      chip.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    })
    expect(location(container)).toBe('/loom/initiatives')
    await act(async () => chip.click())

    expect(location(container)).toBe('/loom/initiatives/epic-new/updates')
    expect(container.querySelector('[data-testid="updates-sheet"]')?.getAttribute('data-epic')).toBe('epic-new')
    expect(container.querySelector('[data-testid="issue-detail"]')).toBeNull()
  })

  it('keeps the rows and drops the Updates column when the updates read fails', async () => {
    const summariesSpy = vi
      .spyOn(api, 'initiativeUpdateSummaries')
      .mockRejectedValue(new ApiError('bd kv list failed', 503))
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } })
    const { container } = await renderView([initiative('epic-a'), initiative('epic-b')], '/loom/initiatives', qc)
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    // The hook retries a 5xx; assert after the read has settled into error.
    expect(qc.getQueryState(qk.updateSummaries('loom'))?.status).toBe('error')
    expect(summariesSpy).toHaveBeenCalled()
    expect(container.querySelector('[role="grid"]')).not.toBeNull()
    expect(renderedRowIDs(container)).toEqual(['epic-a', 'epic-b'])
    expect(columnHeaders(container)).not.toContain('Updates')
    expect(container.textContent).not.toContain('HTTP 503')
  })

  it('drops the Updates column and chips when a refetch fails over cached summaries', async () => {
    const summariesSpy = vi
      .spyOn(api, 'initiativeUpdateSummaries')
      .mockResolvedValueOnce({ updates: [UPDATES_SUMMARY] })
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } })
    const { container } = await renderView(
      [initiative('epic-updates'), initiative('epic-quiet')],
      '/loom/initiatives',
      qc,
    )

    expect(columnHeaders(container)).toContain('Updates')
    expect(container.querySelector('[data-row="epic-updates"] [aria-label^="Weekly updates"]')).not.toBeNull()

    summariesSpy.mockRejectedValue(new ApiError('bd kv list failed', 503))
    await act(async () => {
      await qc.refetchQueries({ queryKey: qk.updateSummaries('loom') })
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    // TanStack Query keeps the last good data after a failed refetch, so the
    // column must follow the error state, not the cached summaries.
    const state = qc.getQueryState(qk.updateSummaries('loom'))
    expect(state?.status).toBe('error')
    expect(state?.data).toEqual({ updates: [UPDATES_SUMMARY] })
    expect(renderedRowIDs(container).sort()).toEqual(['epic-quiet', 'epic-updates'])
    expect(columnHeaders(container)).not.toContain('Updates')
    for (const row of container.querySelectorAll('[data-row]')) {
      expect(row.querySelectorAll('[role="gridcell"]')).toHaveLength(INITIATIVE_COLUMNS.length - 1)
      expect(row.querySelector('[aria-label^="Weekly updates"]')).toBeNull()
    }
    expect(container.textContent).not.toContain('HTTP 503')
  })

  it('opens the empty updates sheet from a direct URL on a project with no update keys', async () => {
    sheet.real = true
    const historySpy = vi
      .spyOn(api, 'initiativeUpdates')
      .mockResolvedValue({ updates: [], knownIssueIds: [], knownRunIds: [] })
    const { container } = await renderView([initiative('epic-a')], '/loom/initiatives/epic-a/updates')

    expect(historySpy).toHaveBeenCalledWith('loom', 'epic-a')
    expect(document.body.textContent).toContain('No stored updates for epic-a')
    expect(document.body.textContent).toContain("bd kv set initiative-retro.epic-a.<YYYY-Www> '<json>'")
    expect(columnHeaders(container)).not.toContain('Updates')
  })

  it('shows a chip only for epics that have stored updates, from one request', async () => {
    const summariesSpy = vi
      .spyOn(api, 'initiativeUpdateSummaries')
      .mockResolvedValue({ updates: [UPDATES_SUMMARY] })

    const { container } = await renderView([
      initiative('epic-updates'),
      initiative('epic-quiet'),
    ])

    // One request for the whole table, not one per row.
    expect(summariesSpy).toHaveBeenCalledTimes(1)

    const index = INITIATIVE_COLUMNS.map((column) => column.id).indexOf('updates')
    const withUpdates = container
      .querySelector('[data-row="epic-updates"]')
      ?.querySelectorAll('[role="gridcell"]')[index]
    const withoutUpdates = container
      .querySelector('[data-row="epic-quiet"]')
      ?.querySelectorAll('[role="gridcell"]')[index]

    const chip = withUpdates?.querySelector('button')
    if (!(chip instanceof HTMLButtonElement)) throw new Error('updates chip not found')
    expect(chip.textContent).toBe('W32 draft')
    expect(chip.getAttribute('data-tone')).toBe('warn')
    expect(chip.getAttribute('title')).toBe('12 updates, 2 drafts; newest 2026-W32 (draft)')
    expect(withoutUpdates?.querySelector('button')).toBeNull()
  })

  it('tones a published latest week differently and drops the draft word', async () => {
    vi.spyOn(api, 'initiativeUpdateSummaries').mockResolvedValue({
      updates: [{ ...UPDATES_SUMMARY, draftCount: 0, latestStatus: 'published' }],
    })
    const { container } = await renderView([initiative('epic-updates')])

    const chip = container.querySelector('[data-row="epic-updates"] [data-tone="accent"]')
    if (!(chip instanceof HTMLButtonElement)) throw new Error('published chip not found')
    expect(chip.textContent).toBe('W32')
    expect(chip.getAttribute('title')).toBe('12 updates; newest 2026-W32 (published)')
  })

  it('opens the updates sheet by route, without opening the issue sheet', async () => {
    vi.spyOn(api, 'initiativeUpdateSummaries').mockResolvedValue({ updates: [UPDATES_SUMMARY] })
    const { container } = await renderView([initiative('epic-updates')])

    const index = INITIATIVE_COLUMNS.map((column) => column.id).indexOf('updates')
    const chip = container
      .querySelector('[data-row="epic-updates"]')
      ?.querySelectorAll('[role="gridcell"]')[index]
      ?.querySelector('button')
    await act(async () => (chip as HTMLButtonElement).click())

    expect(location(container)).toBe('/loom/initiatives/epic-updates/updates')
    expect(container.querySelector('[data-testid="updates-sheet"]')?.getAttribute('data-epic')).toBe(
      'epic-updates',
    )
    // The row click handler must not have fired underneath the chip.
    expect(container.querySelector('[data-testid="issue-detail"]')).toBeNull()
  })

  it('renders the sheet when the route is entered directly', async () => {
    vi.spyOn(api, 'initiativeUpdateSummaries').mockResolvedValue({ updates: [UPDATES_SUMMARY] })
    const { container } = await renderView(
      [initiative('epic-updates')],
      '/loom/initiatives/epic-updates/updates',
    )
    expect(container.querySelector('[data-testid="updates-sheet"]')).not.toBeNull()
  })
})

describe('InitiativesView', () => {
  it('renders loading state while a reachable initiatives request is pending', async () => {
    vi.spyOn(api, 'initiatives').mockImplementation(() => new Promise(() => undefined))

    const { container } = await renderInitiatives({ settle: false })

    expect(container.querySelector('[aria-busy="true"][aria-label="Loading issues"]')).not.toBeNull()
    expect(container.querySelector('[role="grid"]')).toBeNull()
  })

  it('shows cached initiatives with a stale banner when the project is unreachable', async () => {
    const initiativesSpy = vi.spyOn(api, 'initiatives')
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    qc.setQueryData(qk.initiatives('loom', false), { initiatives: [initiative('cached')] })
    const unreachable = { ...project, reachable: false, reason: 'connection refused' }

    const { container } = await renderInitiatives({ projects: [unreachable], qc })

    expect(container.querySelector('[role="status"]')?.textContent).toContain('Stale — server unreachable')
    expect(container.querySelector('[data-row="cached"]')).not.toBeNull()
    expect(initiativesSpy).not.toHaveBeenCalled()
  })

  it('shows the unreachable state when the project is unreachable without cached initiatives', async () => {
    const initiativesSpy = vi.spyOn(api, 'initiatives')
    const unreachable = { ...project, reachable: false, reason: 'connection refused' }

    const { container } = await renderInitiatives({ projects: [unreachable] })

    expect(container.textContent).toContain('server unreachable')
    expect(container.textContent).toContain("loom's dolt server is not answering")
    expect(container.querySelector('[role="grid"]')).toBeNull()
    expect(
      [...container.querySelectorAll('[role="status"]')].some((node) =>
        node.textContent?.includes('Stale — server unreachable'),
      ),
    ).toBe(false)
    expect(initiativesSpy).not.toHaveBeenCalled()
  })

  it('fetches all initiative rows once without per-epic lookups', async () => {
    const issueSpy = vi.spyOn(api, 'issue')
    const issuesSpy = vi.spyOn(api, 'issues')

    const { container, initiativesSpy } = await renderView([
      initiative('a'),
      initiative('b'),
      initiative('c'),
    ])

    expect(container.querySelectorAll('[data-row]')).toHaveLength(3)
    expect(initiativesSpy).toHaveBeenCalledTimes(1)
    expect(issueSpy).not.toHaveBeenCalled()
    expect(issuesSpy).not.toHaveBeenCalled()
  })

  it('fetches update summaries once per load, whatever the epic count', async () => {
    const ids = Array.from({ length: 60 }, (_, index) => `epic-${index}`)
    const summariesSpy = vi.spyOn(api, 'initiativeUpdateSummaries').mockResolvedValue({
      updates: ids.map((epic) => ({ epic, total: 1, draftCount: 0, latestWeek: '2026-W44', latestStatus: 'published' })),
    })
    const historySpy = vi.spyOn(api, 'initiativeUpdates')

    const { container } = await renderView(ids.map((id) => initiative(id)))

    expect(container.querySelectorAll('[aria-label^="Weekly updates"]')).toHaveLength(60)
    expect(summariesSpy).toHaveBeenCalledTimes(1)
    expect(summariesSpy).toHaveBeenCalledWith('loom')
    expect(historySpy).not.toHaveBeenCalled()
  })

  it('moves focus with j and k, then Enter opens the focused epic', async () => {
    const { container } = await renderView([initiative('a'), initiative('b')])

    act(() => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'j' })))
    expect(container.querySelector('[data-row="a"]')?.getAttribute('aria-selected')).toBe('true')
    act(() => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'j' })))
    expect(container.querySelector('[data-row="b"]')?.getAttribute('aria-selected')).toBe('true')
    act(() => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k' })))
    expect(container.querySelector('[data-row="a"]')?.getAttribute('aria-selected')).toBe('true')
    act(() => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' })))

    expect(location(container)).toBe('/loom/initiatives/issue/a')
  })

  it('renders childless epics without a progress bar or rollup counts', async () => {
    const { container } = await renderView([
      initiative('empty', {
        subtree: { total: 0, closed: 0, started: 0, ready: 0, depBlocked: 0, deferScheduled: 0, chained: 0 },
      }),
    ])

    expect(container.textContent).toContain('no children')
    expect(container.querySelector('[aria-label*="descendants closed"]')).toBeNull()
    expect(container.querySelector('[title*="full subtree"]')).toBeNull()
  })

  it('renders every health chip faithfully and opens epic detail when clicked', async () => {
    const cases = [
      ['on-track', 'on track', 'var(--accent)', true],
      ['at-risk', 'at risk', 'var(--warn)', true],
      ['off-track', 'off track', 'var(--danger)', true],
      ['no-target', 'no target', 'var(--text-faint)', false],
      ['no-scope', 'no scope', 'var(--text-faint)', false],
    ] as const
    const rows = cases.map(([state], index) => initiative(`health-${index}`, {
      health: { state, explanation: `${state} arithmetic explanation` },
    }))

    const { container } = await renderView(rows)
    const ids = INITIATIVE_COLUMNS.map((column) => column.id)
    expect(ids.indexOf('health')).toBe(ids.indexOf('title') + 1)
    expect(ids.filter((id) => id === 'health')).toHaveLength(1)
    // The header and the row track share one min width, and it has to cover
    // every declared column — otherwise the last column clips inside the
    // horizontal scroller, which is what happens whenever a column is widened
    // and only one of the two containers is updated.
    const declaredWidth = INITIATIVE_COLUMNS.reduce((total, column) => {
      const fixed = /^(\d+)px$/.exec(column.width)
      const flexible = /minmax\((\d+)px/.exec(column.width)
      return total + Number((fixed ?? flexible)?.[1] ?? 0)
    }, 0)
    const minWidths = [...container.querySelectorAll('div')]
      .map((element) => /min-w-\[(\d+)px\]/.exec(element.className)?.[1])
      .filter((width): width is string => width !== undefined)
      .map(Number)
    expect(minWidths).toHaveLength(2)
    expect(new Set(minWidths).size).toBe(1)
    expect(minWidths[0]).toBeGreaterThanOrEqual(declaredWidth)

    const healthIndex = ids.indexOf('health')
    for (const [state, label, color, colored] of cases) {
      const row = container.querySelector(`[data-row="health-${cases.findIndex(([candidate]) => candidate === state)}"]`)
      const cell = row?.querySelectorAll('[role="gridcell"]')[healthIndex]
      const chip = cell?.querySelector('[aria-label]')
      if (!(chip instanceof HTMLElement)) throw new Error(`${state} health chip not found`)
      expect(chip.textContent).toBe(label)
      expect(chip.getAttribute('title')).toBe(`${state} arithmetic explanation`)
      expect(chip.getAttribute('aria-label')).toBe(`${label}: ${state} arithmetic explanation`)
      expect(chip.style.color).toBe(color)
      expect(chip.classList.contains('chip')).toBe(colored)
    }

    const atRisk = container.querySelector('[data-row="health-1"] [aria-label^="at risk:"]')
    if (!(atRisk instanceof HTMLButtonElement)) throw new Error('at-risk health button not found')
    act(() => atRisk.click())
    expect(location(container)).toBe('/loom/initiatives/issue/health-1')
    expect(container.querySelector('[data-testid="issue-detail"]')).not.toBeNull()
  })

  it('pushes one history entry per health-chip click, so a single Back leaves the detail', async () => {
    const { container } = await renderView([initiative('one-push')])

    const chip = container.querySelector('[data-row="one-push"] [aria-label^="at risk:"]')
    if (!(chip instanceof HTMLButtonElement)) throw new Error('at-risk health button not found')
    act(() => chip.click())
    expect(location(container)).toBe('/loom/initiatives/issue/one-push')

    const back = container.querySelector('[data-back]')
    if (!(back instanceof HTMLButtonElement)) throw new Error('back control not found')
    act(() => back.click())

    // The chip sits inside a row that carries its own onClick. If the chip lets
    // the click bubble, one tap opens the detail twice and this Back press only
    // consumes the duplicate, leaving the reader stuck on the detail route.
    expect(location(container)).toBe('/loom/initiatives')
  })

  it('keeps an initiative row visible when the server sends an unknown health state', async () => {
    const { container } = await renderView([
      initiative('unknown-health', {
        health: {
          state: 'future-state' as Initiative['health']['state'],
          explanation: 'A newer server supplied this health state',
        },
      }),
    ])

    const row = container.querySelector('[data-row="unknown-health"]')
    const health = row?.querySelector('[title="A newer server supplied this health state"]')
    expect(row).not.toBeNull()
    expect(health).not.toBeNull()
    expect(health?.textContent).toBe('future-state')
  })

  it('styles only open past-due initiatives as overdue', async () => {
    const { container } = await renderView([
      initiative('past-open', { due: '2000-01-01T00:00:00Z', status: 'open' }),
      initiative('past-closed', { due: '2000-01-01T00:00:00Z', status: 'closed' }),
      initiative('future-open', { due: '2100-01-01T00:00:00Z', status: 'open' }),
    ])

    const dueCell = (id: string) => {
      const dueIndex = INITIATIVE_COLUMNS.findIndex((column) => column.id === 'due')
      const cell = container.querySelector(`[data-row="${id}"]`)?.querySelectorAll('[role="gridcell"]')[dueIndex]
      if (!(cell instanceof HTMLElement)) throw new Error(`due cell not found for ${id}`)
      return cell
    }

    expect(dueCell('past-open').style.color).toBe('var(--danger)')
    expect(dueCell('past-closed').style.color).toBe('')
    expect(dueCell('future-open').style.color).toBe('')
  })

  it('renders the designed empty state instead of an empty grid', async () => {
    const { container } = await renderView([])

    expect(container.textContent).toContain('This project has no epics')
    expect(container.textContent).toContain('bd create --type=epic')
    expect(container.querySelector('[role="grid"]')).toBeNull()
  })

  it('sorts from the header, reverses the active key, and persists per project', async () => {
    const rows = [
      initiative('high', { subtree: { total: 4, closed: 3, started: 0, ready: 0, depBlocked: 1, deferScheduled: 0, chained: 0 } }),
      initiative('low', { subtree: { total: 4, closed: 1, started: 0, ready: 2, depBlocked: 1, deferScheduled: 0, chained: 0 } }),
    ]
    const { container } = await renderView(rows)
    const progressSort = [...container.querySelectorAll('button')].find((button) =>
      button.textContent?.startsWith('Progress'),
    )
    if (!(progressSort instanceof HTMLButtonElement)) throw new Error('progress sort button not found')

    act(() => progressSort.click())

    expect(renderedRowIDs(container)).toEqual(['low', 'high'])
    expect(localStorage.getItem('loom.initiatives.loom')).toBe('{"key":"progress","dir":"asc"}')

    act(() => progressSort.click())

    expect(renderedRowIDs(container)).toEqual(['high', 'low'])
    expect(localStorage.getItem('loom.initiatives.loom')).toBe('{"key":"progress","dir":"desc"}')

    act(() => root?.unmount())
    root = undefined
    document.body.replaceChildren()
    const { container: remounted } = await renderView(rows)
    const restoredProgressSort = [...remounted.querySelectorAll('button')].find((button) =>
      button.textContent?.startsWith('Progress'),
    )

    expect(renderedRowIDs(remounted)).toEqual(['high', 'low'])
    expect(restoredProgressSort?.getAttribute('aria-current')).toBe('true')
  })

  it('drills from progress into the scoped backlog without opening detail', async () => {
    const { container } = await renderView([initiative('epic-a')])
    const progress = container.querySelector('[title*="across the full subtree"]')
    if (!(progress instanceof HTMLButtonElement)) throw new Error('progress button not found')

    act(() => progress.click())

    expect(location(container)).toBe('/loom/backlog?epic=epic-a')
    expect(container.querySelector('[data-testid="issue-detail"]')).toBeNull()
  })

  it('shows a focusable no-receipt badge only for non-zero counts and drills to the exact filter', async () => {
    const { container } = await renderView([
      initiative('with-receipts-missing', {
        noReceipt: { total: 2, noAcceptance: 2, noCloseReason: 1 },
      }),
      initiative('complete-receipts', {
        noReceipt: { total: 0, noAcceptance: 0, noCloseReason: 0 },
      }),
    ])
    const receiptIndex = INITIATIVE_COLUMNS.findIndex((column) => column.id === 'noReceipt')
    const missingCell = container
      .querySelector('[data-row="with-receipts-missing"]')
      ?.querySelectorAll('[role="gridcell"]')[receiptIndex]
    const completeCell = container
      .querySelector('[data-row="complete-receipts"]')
      ?.querySelectorAll('[role="gridcell"]')[receiptIndex]
    const button = missingCell?.querySelector('button')
    if (!(button instanceof HTMLButtonElement)) throw new Error('no-receipt button not found')

    const breakdown = '2 closed with no acceptance criteria; 1 closed with no close reason; a bead missing both is counted once.'
    const header = [...container.querySelectorAll('[role="columnheader"]')][receiptIndex]
    const headerTrigger = header?.querySelector('[tabindex="0"]')
    if (!(headerTrigger instanceof HTMLElement)) throw new Error('no-receipt header trigger not found')
    const headerTooltip = header?.querySelector('[role="tooltip"]')
    expect(headerTrigger.textContent).toContain('No receipt')
    expect(headerTrigger.getAttribute('aria-describedby')).toBe(headerTooltip?.id)
    expect(headerTooltip?.textContent).toBe(
      'Closed descendants with no acceptance criteria or no close reason; a bead missing both is counted once.',
    )
    // Shown on hover and on keyboard focus, never only one of the two.
    expect(headerTooltip?.className).toContain('group-hover:opacity-100')
    expect(headerTooltip?.className).toContain('group-focus-visible:opacity-100')
    expect(headerTrigger.className).toContain('group')
    act(() => headerTrigger.focus())
    expect(document.activeElement).toBe(headerTrigger)
    const tooltip = missingCell?.querySelector('[role="tooltip"]')
    expect(button.title).toBe(breakdown)
    expect(tooltip?.textContent).toBe(breakdown)
    expect(button.getAttribute('aria-describedby')).toBe(tooltip?.id)
    act(() => button.focus())
    expect(document.activeElement).toBe(button)
    expect(completeCell?.querySelector('button')).toBeNull()
    expect(completeCell?.textContent).toBe('')

    act(() => button.click())

    expect(location(container)).toBe('/loom/backlog?epic=with-receipts-missing&noReceipt=true')
    expect(container.querySelector('[data-testid="issue-detail"]')).toBeNull()
  })

  it('exposes Started as a peer column of Ready, Blocked and Deferred', () => {
    const ids = INITIATIVE_COLUMNS.map((column) => column.id)

    expect(ids).toContain('started')
    // Immediately after Progress and before Ready: the pipeline reads
    // Started → Ready → Blocked → Deferred.
    expect(ids.indexOf('started')).toBe(ids.indexOf('progress') + 1)
    expect(ids.indexOf('started')).toBeLessThan(ids.indexOf('ready'))
    expect(ids.filter((id) => id === 'started')).toHaveLength(1)
    expect(INITIATIVE_COLUMNS.find((column) => column.id === 'started')?.label).toBe('Started')
  })

  it('renders the subtree started count in its own cell and drills down from it', async () => {
    const { container } = await renderView([
      initiative('busy', {
        subtree: { total: 8, closed: 2, started: 3, ready: 2, depBlocked: 1, deferScheduled: 0, chained: 0 },
      }),
    ])
    const startedIndex = INITIATIVE_COLUMNS.findIndex((column) => column.id === 'started')
    const cell = container
      .querySelector('[data-row="busy"]')
      ?.querySelectorAll('[role="gridcell"]')[startedIndex]
    if (!(cell instanceof HTMLElement)) throw new Error('started cell not found')

    expect(cell.textContent).toBe('3')
    const button = cell.querySelector('button')
    if (!(button instanceof HTMLButtonElement)) throw new Error('started drill-down not found')
    expect(button.style.color).toBe('var(--status-in-progress)')

    act(() => button.click())

    expect(location(container)).toBe('/loom/backlog?epic=busy')
  })

  it('leaves the started cell empty when no descendant is under way', async () => {
    const { container } = await renderView([
      initiative('idle', {
        subtree: { total: 4, closed: 0, started: 0, ready: 4, depBlocked: 0, deferScheduled: 0, chained: 0 },
      }),
    ])
    const startedIndex = INITIATIVE_COLUMNS.findIndex((column) => column.id === 'started')
    const cell = container
      .querySelector('[data-row="idle"]')
      ?.querySelectorAll('[role="gridcell"]')[startedIndex]

    expect(cell?.textContent).toBe('')
    expect(cell?.querySelector('button')).toBeNull()
  })

  it('places In chain after the pipeline buckets, explained in its header', async () => {
    const ids = INITIATIVE_COLUMNS.map((column) => column.id)
    expect(ids.indexOf('chained')).toBe(ids.indexOf('deferred') + 1)
    expect(INITIATIVE_COLUMNS.find((column) => column.id === 'chained')?.label).toBe('In chain')

    const { container } = await renderView([initiative('epic-a')])
    const header = [...container.querySelectorAll('[role="columnheader"]')][ids.indexOf('chained')]
    const trigger = header?.querySelector('[tabindex="0"]')
    const tooltip = header?.querySelector('[role="tooltip"]')
    if (!(trigger instanceof HTMLElement)) throw new Error('In chain header trigger not found')
    expect(trigger.getAttribute('aria-describedby')).toBe(tooltip?.id)
    // The header says what separates it from Blocked: closed blockers and the
    // blockers themselves still count.
    expect(tooltip?.textContent).toContain('either end of a blocks dependency')
    expect(tooltip?.textContent).toContain('not what is blocked now')
  })

  it('renders the chained count apart from Blocked and drills to the scoped blockers graph', async () => {
    const { container } = await renderView([
      initiative('chainy', {
        subtree: { total: 9, closed: 3, started: 0, ready: 4, depBlocked: 2, deferScheduled: 0, chained: 5 },
      }),
    ])
    const chainedIndex = INITIATIVE_COLUMNS.findIndex((column) => column.id === 'chained')
    const blockedIndex = INITIATIVE_COLUMNS.findIndex((column) => column.id === 'blocked')
    const cells = container.querySelector('[data-row="chainy"]')?.querySelectorAll('[role="gridcell"]')
    const cell = cells?.[chainedIndex]
    if (!(cell instanceof HTMLElement)) throw new Error('chained cell not found')

    expect(cell.textContent).toBe('5')
    expect(cells?.[blockedIndex]?.textContent).toBe('2')
    const button = cell.querySelector('button')
    if (!(button instanceof HTMLButtonElement)) throw new Error('chained drill-down not found')
    expect(button.getAttribute('aria-label')).toBe(
      '5 descendants in a blocking chain; open the blockers graph for chainy',
    )
    // Blocked owns the blocked colour; this count must not borrow it.
    expect(button.style.color).not.toBe('var(--status-blocked)')

    act(() => button.focus())
    expect(document.activeElement).toBe(button)
    act(() => button.click())

    // Closed issues are shown on arrival, because the count includes pairs
    // whose blocker has closed.
    expect(location(container)).toBe('/loom/graph?epic=chainy&closed=1')
    expect(container.querySelector('[data-testid="issue-detail"]')).toBeNull()
  })

  it('leaves the chained cell empty when nothing in the subtree touches a blocks edge', async () => {
    const { container } = await renderView([
      initiative('pile', {
        subtree: { total: 4, closed: 0, started: 0, ready: 4, depBlocked: 0, deferScheduled: 0, chained: 0 },
      }),
    ])
    const chainedIndex = INITIATIVE_COLUMNS.findIndex((column) => column.id === 'chained')
    const cell = container.querySelector('[data-row="pile"]')?.querySelectorAll('[role="gridcell"]')[chainedIndex]

    expect(cell?.textContent).toBe('')
    expect(cell?.querySelector('button')).toBeNull()
  })

  describe('missing-target nudge', () => {
    const needsTarget = (id: string, values: Partial<Initiative> = {}) =>
      initiative(id, {
        needsTarget: true,
        health: { state: 'no-target', explanation: 'No due date — no target to assess' },
        ...values,
      })

    const badges = (container: HTMLElement) =>
      [...container.querySelectorAll('[data-testid^="target-nudge-"]')].map((node) =>
        node.getAttribute('data-testid'),
      )

    const click = async (node: Element | null | undefined) => {
      if (!(node instanceof HTMLElement)) throw new Error('nothing to click')
      await act(async () => node.click())
    }

    it('flags only the epics the server says need a target, and says why on hover', async () => {
      const { container } = await renderView([
        needsTarget('blank'),
        initiative('dated', { due: '2026-09-01T00:00:00Z' }),
        initiative('opted-out'),
      ])

      expect(badges(container)).toEqual(['target-nudge-blank'])
      const badge = container.querySelector('[data-testid="target-nudge-blank"]')
      // The hover text has to name the epic's own numbers, not a generic line.
      expect(badge?.getAttribute('title')).toContain('3 of 4')
      expect(badge?.getAttribute('title')).toContain('no due date')
      // The health chip keeps saying what health says; the badge is additive.
      expect(container.querySelector('[data-row="blank"]')?.textContent).toContain('no target')
    })

    it('stays out of the way when the project cannot be written to', async () => {
      const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
      qc.setQueryData(qk.initiatives('loom', false), { initiatives: [needsTarget('blank')] })
      const unreachable = { ...project, reachable: false, reason: 'connection refused' }

      const { container } = await renderInitiatives({ projects: [unreachable], qc })

      expect(container.querySelector('[data-row="blank"]')).not.toBeNull()
      expect(badges(container)).toEqual([])
    })

    it('opens a panel naming the epic, and a dismiss from it persists per epic', async () => {
      const { container } = await renderView([needsTarget('blank'), needsTarget('other')])

      await click(container.querySelector('[data-testid="target-nudge-blank"]'))
      const dialog = document.querySelector('[role="dialog"]')
      expect(dialog?.getAttribute('aria-label')).toBe('Set a target for blank')

      await click(
        [...document.querySelectorAll('[role="dialog"] button')].find(
          (node) => node.textContent?.trim() === 'Dismiss',
        ),
      )

      expect(document.querySelector('[role="dialog"]')).toBeNull()
      // Only this epic goes quiet; the other still asks.
      expect(badges(container)).toEqual(['target-nudge-other'])
      expect(localStorage.getItem('loom.targetNudge.loom')).toBe('["blank"]')
    })

    it('honours a dismissal stored before this session', async () => {
      localStorage.setItem('loom.targetNudge.loom', '["blank"]')

      const { container } = await renderView([needsTarget('blank'), needsTarget('other')])

      expect(badges(container)).toEqual(['target-nudge-other'])
    })

    it('drops a stale dismissal once the epic stops needing a target, so a cleared date nags again', async () => {
      localStorage.setItem('loom.targetNudge.loom', '["blank"]')

      // The epic came back with a due date, so it no longer needs a target.
      await renderView([initiative('blank', { due: '2026-09-01T00:00:00Z' })])

      expect(localStorage.getItem('loom.targetNudge.loom')).toBe('[]')
    })

    it('leaves the destination project dismissals intact when the project switches', async () => {
      localStorage.setItem('loom.targetNudge.loom', '["loom-a"]')
      localStorage.setItem(`loom.targetNudge.${SWITCH_TO}`, '["other-a"]')
      const other = { ...project, name: SWITCH_TO, db: SWITCH_TO, workdir: `/work/${SWITCH_TO}` }
      const rowsFor = (name: string) =>
        name === 'loom' ? [needsTarget('loom-a')] : [needsTarget('other-a')]
      // Both projects are already cached, so the destination's rows are present
      // in the very commit the switch happens in — the case where a prune that
      // still holds the previous project's list writes over the new one's key.
      const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
      qc.setQueryData(qk.initiatives('loom', false), { initiatives: rowsFor('loom') })
      qc.setQueryData(qk.initiatives(SWITCH_TO, false), { initiatives: rowsFor(SWITCH_TO) })
      vi.spyOn(api, 'initiatives').mockImplementation((name: string) =>
        Promise.resolve({ initiatives: rowsFor(name) }),
      )
      vi.spyOn(api, 'initiativeUpdateSummaries').mockResolvedValue({ updates: [] })

      const { container } = await renderInitiatives({ projects: [project, other], qc })
      expect(badges(container)).toEqual([])

      await click(container.querySelector('[data-goto]'))

      expect(renderedRowIDs(container)).toEqual(['other-a'])
      expect(localStorage.getItem(`loom.targetNudge.${SWITCH_TO}`)).toBe('["other-a"]')
      expect(badges(container)).toEqual([])
      // And the project we left keeps its own dismissal.
      expect(localStorage.getItem('loom.targetNudge.loom')).toBe('["loom-a"]')
    })

    it('keeps a dismissal while the fetch is still in flight', async () => {
      localStorage.setItem('loom.targetNudge.loom', '["blank"]')
      vi.spyOn(api, 'initiatives').mockReturnValue(new Promise(() => {}))
      vi.spyOn(api, 'initiativeUpdateSummaries').mockResolvedValue({ updates: [] })

      await renderInitiatives({})

      // An empty list mid-fetch is not evidence that anything was fixed.
      expect(localStorage.getItem('loom.targetNudge.loom')).toBe('["blank"]')
    })
  })

  it('lays the started segment beside the closed one without inflating progress', async () => {
    const { container } = await renderView([
      initiative('mixed', {
        subtree: { total: 8, closed: 2, started: 4, ready: 2, depBlocked: 0, deferScheduled: 0, chained: 0 },
      }),
    ])
    const row = container.querySelector('[data-row="mixed"]')
    const closed = row?.querySelector('[data-segment="closed"]')
    const started = row?.querySelector('[data-segment="started"]')
    if (!(closed instanceof HTMLElement) || !(started instanceof HTMLElement)) {
      throw new Error('progress segments not found')
    }

    expect(closed.style.width).toBe('25%')
    expect(started.style.width).toBe('50%')
    expect(started.style.background).toBe('var(--status-in-progress)')
    expect(closed.style.background).toBe('var(--accent)')
    // The headline ratio stays closed/total.
    expect(row?.textContent).toContain('2/8')
    expect(
      row?.querySelector('[aria-label*="descendants closed"]')?.getAttribute('aria-label'),
    ).toBe('2 of 8 descendants closed, 4 started')
  })
})
