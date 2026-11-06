import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import type { Issue, IssueFilter, IssueList } from '@/api/types'
import { CommandPalette } from './CommandPalette'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// jsdom has no layout, so it has no scrollIntoView; the palette keeps the
// cursor row visible with one.
Element.prototype.scrollIntoView ??= () => undefined

vi.mock('./Live', () => ({ useInteractionLock: () => undefined }))

const issues = vi.fn<(project: string, filter?: IssueFilter) => Promise<IssueList>>()

vi.mock('@/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/client')>()
  return { ...actual, api: { ...actual.api, issues: (p: string, f?: IssueFilter) => issues(p, f) } }
})

/**
 * The palette against a project far larger than any page of it (loom-nlm).
 *
 * The fixture is the shape of the bug: 6,030 issues, and the bead we look for
 * sits nowhere near the front of the default sort. A palette that ranks a
 * fetched array can only pass these by fetching all 6,030 rows; the one that
 * passes them for the right reason asks the server the question.
 */
const DEEP_ID = 'scale-030.400.9'
const DEEP_TITLE = 'Initiative 30 workstream 400 detail 9'
const PROJECT_TOTAL = 6030

const issue = (id: string, title: string, over: Partial<Issue> = {}): Issue =>
  ({
    id,
    title,
    type: 'task',
    status: 'open',
    priority: 2,
    assignee: '',
    labels: [],
    estimate: 0,
    created: '',
    updated: '',
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
    ...over,
  }) as Issue

/** The first page of the default sort — where the deep bead conspicuously is not. */
const pageOne = Array.from({ length: 200 }, (_, i) =>
  issue(`scale-001.${String(i).padStart(3, '0')}`, `Initiative 01 workstream ${i}`),
)

let root: Root
let container: HTMLDivElement

function render(project = 'scale') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  act(() =>
    root.render(
      <QueryClientProvider client={client}>
        <CommandPalette
          actions={[]}
          project={project}
          goIssue={() => undefined}
          onClose={() => undefined}
        />
      </QueryClientProvider>,
    ),
  )
}

function type(text: string) {
  const input = container.querySelector('input')!
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
  act(() => {
    setter.call(input, text)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

async function settle(predicate: () => boolean, label: string) {
  for (let i = 0; i < 100; i += 1) {
    if (predicate()) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10))
    })
  }
  throw new Error(`timed out waiting for ${label}; list was:\n${rows().join('\n')}`)
}

const rows = () =>
  [...container.querySelectorAll('[role="option"]')].map((el) => el.textContent ?? '')

const scope = () => container.querySelector('[data-testid="palette-scope"]')?.textContent ?? ''

beforeEach(() => {
  issues.mockReset()
  sessionStorage.clear()
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
})

describe('CommandPalette issue lookup', () => {
  it('finds a bead that sits far outside the first page of the project', async () => {
    // The server answers a *search*; an unfiltered page never carries this row.
    issues.mockImplementation(async (_project, filter) => {
      const q = filter?.q?.trim()
      if (!q) return { issues: pageOne, total: PROJECT_TOTAL }
      const hit = `${DEEP_ID} ${DEEP_TITLE}`.toLowerCase().includes(q.toLowerCase())
      return { issues: hit ? [issue(DEEP_ID, DEEP_TITLE)] : [], total: hit ? 1 : 0 }
    })

    render()
    type('400.9')
    await settle(() => rows().some((r) => r.includes(DEEP_ID)), DEEP_ID)

    const sent = issues.mock.calls.at(-1)!
    expect(sent[0]).toBe('scale')
    expect(sent[1]?.q).toBe('400.9')
    // Looking a bead up by id must work whatever its status.
    expect(sent[1]?.includeClosed).toBe(true)
  })

  it('never asks the server for an unfiltered page — that page was the bug', async () => {
    issues.mockResolvedValue({ issues: [issue(DEEP_ID, DEEP_TITLE)], total: 1 })
    render()
    type('workstream 400')
    await settle(() => issues.mock.calls.length > 0, 'a request')
    expect(issues.mock.calls.every(([, filter]) => Boolean(filter?.q))).toBe(true)
  })

  it('states the true match count when it cannot draw every match', async () => {
    const many = Array.from({ length: 50 }, (_, i) =>
      issue(`scale-0${i}.workstream`, `Workstream ${i}`),
    )
    issues.mockResolvedValue({ issues: many, total: 431 })

    render()
    type('workstream')
    await settle(() => /Showing \d+ of 431/.test(scope()), 'the match-count notice')

    const drawn = rows().filter((r) => r.includes('workstream')).length
    expect(scope()).toBe(`Showing ${drawn} of 431 matching issues`)
    expect(drawn).toBeLessThan(431)
  })

  it('names the match count plainly when every match is on screen', async () => {
    issues.mockResolvedValue({ issues: [issue(DEEP_ID, DEEP_TITLE)], total: 1 })
    render()
    type('400.9')
    await settle(() => scope() === '1 matching issue', 'the complete-match notice')
  })

  it('says the search covers the whole project before anything is typed', () => {
    issues.mockResolvedValue({ issues: [], total: 0 })
    render()
    expect(scope()).toBe('Type to search every issue in scale')
    expect(issues).not.toHaveBeenCalled()
  })

  it('does not search on a single character', async () => {
    issues.mockResolvedValue({ issues: [], total: 0 })
    render()
    type('s')
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 250))
    })
    expect(issues).not.toHaveBeenCalled()
    expect(scope()).toBe('Type 2 characters to search issues')
  })

  it('reports a failed search instead of showing an empty list as an answer', async () => {
    // A 4xx, so the hook's retry policy answers immediately: this asserts what
    // the palette says about a failure, not how long it waits to say it.
    issues.mockRejectedValue(new ApiError('project is unreachable', 404))
    render()
    type('400.9')
    await settle(() => scope().startsWith('Issue search failed'), 'the failure notice')
    expect(scope()).toBe('Issue search failed — no issue results')

    // …and the list must not answer "nothing matches", which a failed search
    // has not established.
    const list = container.querySelector('#palette-results')!
    expect(list.textContent).not.toContain('Nothing matches')
    expect(list.textContent).toContain('Issue search failed')

    // The one control that can change the outcome is on screen, not implied.
    const again = [...list.querySelectorAll('button')].find((b) =>
      b.textContent?.includes('Search again'),
    )
    expect(again).toBeTruthy()
    const before = issues.mock.calls.length
    act(() => again!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await settle(() => issues.mock.calls.length > before, 'a retried request')
  })

  it('counts a match drawn under Recent rather than reporting it missing', async () => {
    // The bead is both recently visited and the sole server hit, so the dedup
    // draws it once, under Recent. The footer counts matches, not headings —
    // "Showing 0 of 1" while the match is on screen is the palette calling
    // itself a liar.
    sessionStorage.setItem(
      'loom.recent.scale',
      JSON.stringify([{ id: DEEP_ID, title: DEEP_TITLE }]),
    )
    issues.mockResolvedValue({ issues: [issue(DEEP_ID, DEEP_TITLE)], total: 1 })

    render()
    type('400.9')
    await settle(() => scope() === '1 matching issue', 'the deduped-match count')
    expect(rows().filter((r) => r.includes(DEEP_ID))).toHaveLength(1)
  })

  it('offers a recently opened bead the server search did not return', async () => {
    // Recency is remembered with its title, so it survives with no corpus.
    sessionStorage.setItem(
      'loom.recent.scale',
      JSON.stringify([{ id: DEEP_ID, title: DEEP_TITLE }]),
    )
    issues.mockResolvedValue({ issues: [], total: 0 })
    render()
    expect(rows().some((r) => r.includes(DEEP_ID))).toBe(true)
  })
})
