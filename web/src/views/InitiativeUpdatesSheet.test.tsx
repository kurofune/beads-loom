import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError } from '@/api/client'
import { mockUpdateHistory } from '@/api/fixtures'
import type { InitiativeUpdate } from '@/api/types'
import { ToastProvider } from '@/components/Toast'
import { InitiativeUpdatesSheet } from './InitiativeUpdatesSheet'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

vi.mock('@/components/Live', () => ({ useInteractionLock: () => undefined }))

let root: Root | undefined

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = undefined
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

const EPIC = 'loom-d1q'

async function renderSheet(
  updates: InitiativeUpdate[],
  knownIssueIds: string[] = [`${EPIC}.1`, `${EPIC}.2`],
  onOpenIssue: (id: string) => void = () => undefined,
  knownRunIds: string[] = [],
  onOpenRun: (id: string) => void = () => undefined,
) {
  vi.spyOn(api, 'initiativeUpdates').mockResolvedValue({ updates, knownIssueIds, knownRunIds })
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  await act(async () => {
    root?.render(
      <QueryClientProvider client={qc}>
        <ToastProvider>
          <MemoryRouter>
            <InitiativeUpdatesSheet
              project="loom"
              epic={EPIC}
              onClose={() => undefined}
              onOpenIssue={onOpenIssue}
              onOpenRun={onOpenRun}
            />
          </MemoryRouter>
        </ToastProvider>
      </QueryClientProvider>,
    )
  })
  await act(async () => {
    await new Promise((resolve) => window.setTimeout(resolve, 20))
  })
  return container
}

function weekRows(container: HTMLElement): HTMLElement[] {
  return [...container.querySelectorAll<HTMLElement>('[data-week]')]
}

const draft = (week: string, body: string): InitiativeUpdate => ({
  epic: EPIC,
  week,
  status: 'draft',
  generatedAt: '2026-08-08T01:17:20Z',
  body,
  schema: 1,
  bodySha: `sha-${week}`,
  parseError: '',
})

const published = (week: string, body: string): InitiativeUpdate => ({
  ...draft(week, body),
  status: 'published',
})

describe('InitiativeUpdatesSheet', () => {
  it('stays readable at a year of updates: 52 headers, newest expanded, rest collapsed', async () => {
    const history = mockUpdateHistory(EPIC, 52)
    const container = await renderSheet(history)

    const rows = weekRows(container)
    expect(rows).toHaveLength(52)
    // Newest first, and the year boundary sorts correctly.
    expect(rows.map((row) => row.dataset.week)[0]).toBe('2026-W32')
    expect(rows.at(-1)?.dataset.week).toBe('2025-W33')

    const expandedRows = rows.filter(
      (row) => row.querySelector('[aria-expanded="true"]') !== null,
    )
    expect(expandedRows).toHaveLength(1)
    expect(expandedRows[0]?.dataset.week).toBe('2026-W32')
    // Every other week is a one-line header button, so 52 weeks is a scroll and
    // not 52 rendered markdown bodies.
    expect(container.querySelectorAll('.prose-loom')).toHaveLength(1)
  })

  it('expands an older week on click and collapses it again', async () => {
    const container = await renderSheet(mockUpdateHistory(EPIC, 6))
    const older = weekRows(container)[4]!
    const toggle = older.querySelector('button')!

    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    await act(async () => toggle.click())
    expect(older.querySelector('[aria-expanded="true"]')).not.toBeNull()
    expect(container.querySelectorAll('.prose-loom')).toHaveLength(2)

    await act(async () => older.querySelector('button')!.click())
    expect(container.querySelectorAll('.prose-loom')).toHaveLength(1)
  })

  it('marks a draft distinctly from a published update', async () => {
    const container = await renderSheet([
      draft('2026-W32', `- ${EPIC}.1 landed (${EPIC}.1)`),
      published('2026-W31', `- ${EPIC}.1 started (${EPIC}.1)`),
    ])
    const [newest, older] = weekRows(container)

    expect(newest?.dataset.status).toBe('draft')
    expect(newest?.textContent).toContain('DRAFT')
    expect(newest?.style.borderLeft).toContain('var(--warn)')
    expect(older?.dataset.status).toBe('published')
    expect(older?.textContent).toContain('PUBLISHED')
    expect(older?.style.borderLeft).not.toContain('var(--warn)')
  })

  it('offers Publish on a draft only', async () => {
    const container = await renderSheet([published('2026-W32', `- ${EPIC}.1 landed (${EPIC}.1)`)])
    expect(container.textContent).not.toContain('Publish…')

    document.body.replaceChildren()
    act(() => root?.unmount())
    const withDraft = await renderSheet([draft('2026-W32', `- ${EPIC}.1 landed (${EPIC}.1)`)])
    expect(withDraft.textContent).toContain('Publish…')
  })

  it('links a citation whose bead exists and leaves the rest as text', async () => {
    const opened: string[] = []
    const container = await renderSheet(
      [published('2026-W32', `- ${EPIC}.1 landed in b64f334; the read-only view shipped`)],
      [`${EPIC}.1`],
      (id) => opened.push(id),
    )

    const links = [...container.querySelectorAll('a')]
    expect(links).toHaveLength(1)
    expect(links[0]?.textContent).toBe(`${EPIC}.1`)
    await act(async () => links[0]?.click())
    expect(opened).toEqual([`${EPIC}.1`])

    // The sha is mono, not a link; "read-only" is prose and stays untouched.
    expect(container.querySelector('code')?.textContent).toBe('b64f334')
    expect(container.textContent).toContain('the read-only view shipped')
  })

  it('links a cited run the project has, and leaves an unknown run as text', async () => {
    const known = 'run-1754270000000000000'
    const opened: string[] = []
    const container = await renderSheet(
      [
        published(
          '2026-W32',
          `- ${EPIC}.1 landed, verified by ${known}\n- ${EPIC}.1 retried under run-1600000000000000000`,
        ),
      ],
      [],
      () => undefined,
      [known],
      (id) => opened.push(id),
    )

    const links = [...container.querySelectorAll('a')]
    expect(links).toHaveLength(1)
    expect(links[0]?.textContent).toBe(known)
    await act(async () => links[0]?.click())
    expect(opened).toEqual([known])

    // The run with no record stays mono text, the same as a sha.
    expect(container.querySelector('code')?.textContent).toBe('run-1600000000000000000')
  })

  it('renders a quiet week as the agent wrote it, with no filler', async () => {
    const body = `# ${EPIC} — 2026-W33\n\nQuiet week for ${EPIC}: no beads closed, started, or discovered.\n`
    const container = await renderSheet([published('2026-W33', body)])
    expect(container.textContent).toContain(`Quiet week for ${EPIC}`)
    expect(container.textContent).not.toContain('Closed')
  })

  it('renders an unreadable stored value as a row, not a crash', async () => {
    const container = await renderSheet([
      { ...draft('2026-W32', ''), parseError: 'unexpected end of JSON input' },
    ])
    expect(container.textContent).toContain('UNREADABLE')
    expect(container.textContent).toContain('unexpected end of JSON input')
    // A row Loom cannot read is a row Loom must not offer to publish.
    expect(container.textContent).not.toContain('Publish…')
  })

  it('renders the empty state with the bd command that stores an update', async () => {
    const container = await renderSheet([])
    expect(container.textContent).toContain(`No stored updates for ${EPIC}`)
    expect(container.textContent).toContain(`bd kv set initiative-retro.${EPIC}.<YYYY-Www> '<json>'`)
  })

  it('renders the loading state before the first response', async () => {
    vi.spyOn(api, 'initiativeUpdates').mockImplementation(() => new Promise(() => undefined))
    const container = document.createElement('div')
    document.body.append(container)
    root = createRoot(container)
    await act(async () => {
      root?.render(
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <ToastProvider>
            <MemoryRouter>
              <InitiativeUpdatesSheet
                project="loom"
                epic={EPIC}
                onClose={() => undefined}
                onOpenIssue={() => undefined}
                onOpenRun={() => undefined}
              />
            </MemoryRouter>
          </ToastProvider>
        </QueryClientProvider>,
      )
    })
    expect(container.querySelector('[aria-busy="true"][aria-label="Loading issue"]')).not.toBeNull()
  })

  it('renders a kv read failure with bd stderr and a retry', async () => {
    vi.spyOn(api, 'initiativeUpdates').mockRejectedValue(
      new ApiError('bd kv read failed', 500, { stderr: 'bd: no beads database in this directory' }),
    )
    // A 5xx is retried twice by the shared policy; zeroing the delay lets the
    // error state arrive without a three-second test.
    const qc = new QueryClient({ defaultOptions: { queries: { retryDelay: 0 } } })
    const container = document.createElement('div')
    document.body.append(container)
    root = createRoot(container)
    await act(async () => {
      root?.render(
        <QueryClientProvider client={qc}>
          <ToastProvider>
            <MemoryRouter>
              <InitiativeUpdatesSheet
                project="loom"
                epic={EPIC}
                onClose={() => undefined}
                onOpenIssue={() => undefined}
                onOpenRun={() => undefined}
              />
            </MemoryRouter>
          </ToastProvider>
        </QueryClientProvider>,
      )
    })
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 60))
    })
    expect(container.textContent).toContain('bd kv read failed')
    expect(container.textContent).toContain('Retry')
  })
})

describe('publish confirm', () => {
  async function openConfirm(body: string) {
    const container = await renderSheet([draft('2026-W32', body)])
    const publishButton = [...container.querySelectorAll('button')].find(
      (button) => button.textContent === 'Publish…',
    )!
    await act(async () => publishButton.click())
    return container
  }

  it('names the epic and week and states that publishing freezes the update', async () => {
    const container = await openConfirm(`- ${EPIC}.1 landed (${EPIC}.1)`)
    const dialog = document.querySelector('[role="dialog"][aria-label^="Publish "]')
    expect(dialog?.getAttribute('aria-label')).toBe(`Publish ${EPIC} 2026-W32?`)
    expect(document.body.textContent).toContain('never overwrites')
    expect(container).toBeTruthy()
  })

  it('sends the reviewed body sha and flips the badge without closing the sheet', async () => {
    const publishSpy = vi
      .spyOn(api, 'publishUpdate')
      .mockResolvedValue({ published: true, epic: EPIC, week: '2026-W32' })
    await openConfirm(`- ${EPIC}.1 landed (${EPIC}.1)`)

    const confirm = [...document.querySelectorAll('button')].find(
      (button) => button.textContent === 'Publish',
    )!
    // The refetch after a successful publish returns the published row.
    vi.spyOn(api, 'initiativeUpdates').mockResolvedValue({
      updates: [published('2026-W32', `- ${EPIC}.1 landed (${EPIC}.1)`)],
      knownIssueIds: [`${EPIC}.1`],
      knownRunIds: [],
    })
    await act(async () => confirm.click())
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    expect(publishSpy).toHaveBeenCalledWith('loom', EPIC, '2026-W32', { bodySha: 'sha-2026-W32' })
    expect(document.querySelector('[role="dialog"][aria-label^="Publish "]')).toBeNull()
    expect(document.querySelector('[data-week="2026-W32"]')?.getAttribute('data-status')).toBe(
      'published',
    )
  })

  it('shows the citation refusal verbatim, naming the offending claim', async () => {
    vi.spyOn(api, 'publishUpdate').mockRejectedValue(
      new ApiError(
        'this update cannot be published: a claim carries no citation (bead id, commit sha, or run id): "- We tidied up a few things"',
        422,
      ),
    )
    await openConfirm(`- ${EPIC}.1 landed (${EPIC}.1)\n- We tidied up a few things`)

    const confirm = [...document.querySelectorAll('button')].find(
      (button) => button.textContent === 'Publish',
    )!
    await act(async () => confirm.click())
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })

    const alert = document.querySelector('[role="alert"]')
    expect(alert?.textContent).toContain('We tidied up a few things')
    // The dialog stays open so the message sits next to the draft it refers to.
    expect(document.querySelector('[role="dialog"][aria-label^="Publish "]')).not.toBeNull()
  })

  it('shows a conflict verbatim rather than failing silently', async () => {
    vi.spyOn(api, 'publishUpdate').mockRejectedValue(
      new ApiError('the draft changed since it was reviewed; reload and review again', 409),
    )
    await openConfirm(`- ${EPIC}.1 landed (${EPIC}.1)`)

    const confirm = [...document.querySelectorAll('button')].find(
      (button) => button.textContent === 'Publish',
    )!
    await act(async () => confirm.click())
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 20))
    })
    expect(document.querySelector('[role="alert"]')?.textContent).toContain('reload and review again')
  })
})
