import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/api/client'
import type { Initiative, Issue, Project } from '@/api/types'
import { ToastProvider } from '@/components/Toast'
import { IntegrationRoots } from '@/integrations'
import { BoardView } from './BoardView'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

vi.mock('@/components/DoltSync', () => ({ DoltSync: () => null }))
vi.mock('@/components/Live', () => ({
  useInteractionLock: () => undefined,
  useActiveRun: () => undefined,
}))
vi.mock('@/views/IssueDetail', () => ({ IssueDetail: () => null }))

const project: Project = {
  name: 'loom',
  source: 'config',
  db: 'loom',
  workdir: '/work/loom',
  reachable: true,
  reason: '',
  writable: true,
  writeReason: '',
  integrations: { djinn: { capabilities: ['runs', 'evidence', 'dispatch'] } },
  health: { ready: 1, inProgress: 0, blocked: 0, open: 3, total: 3 },
}

const issue = (values: Partial<Issue>): Issue => ({
  id: 'issue',
  title: 'Issue',
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
  pinned: false,
  depCounts: { blockedBy: 0, blocks: 0 },
  parent: null,
  childStats: null,
  ...values,
  // bd stamps closed_at on every close, so a closed fixture carries one too —
  // the Closed column's time window reads exactly this field, and a fixture
  // with no finish time would be testing data drift rather than the feature.
  ...(values.status === 'closed' && values.closed === undefined
    ? { closed: new Date().toISOString() }
    : {}),
  missingAcceptance: values.missingAcceptance ?? false,
  missingCloseReason: values.missingCloseReason ?? false,
})

const initiative = (id: string, closeEligible: boolean): Initiative => ({
  id,
  title: `Initiative ${id}`,
  status: 'open',
  priority: 2,
  assignee: '',
  due: null,
  updated: '2026-08-01T00:00:00Z',
  lastActivity: '2026-08-02T00:00:00Z',
  subtree: { total: 2, closed: 2, started: 0, ready: 0, depBlocked: 0, deferScheduled: 0 },
  closeEligible,
  needsTarget: false,
  health: { state: 'on-track', explanation: 'All descendants closed' },
  noReceipt: { total: 0, noAcceptance: 0, noCloseReason: 0 },
})

let root: Root | undefined

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = undefined
  document.body.replaceChildren()
  sessionStorage.clear()
  vi.restoreAllMocks()
})

async function renderBoard(
  issues: Issue[],
  initiatives: Initiative[] = [],
  /** Rows the server says match — larger than `issues` means the page truncated. */
  total = issues.length,
): Promise<HTMLElement> {
  vi.spyOn(api, 'issues').mockResolvedValue({ issues, total })
  vi.spyOn(api, 'initiatives').mockResolvedValue({ initiatives })

  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  await act(async () => {
    root?.render(
      <QueryClientProvider client={client}>
        <ToastProvider>
          <IntegrationRoots>
            <MemoryRouter initialEntries={['/loom/board']}>
              <Routes>
                <Route path="/:project/board" element={<BoardView projects={[project]} />} />
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

function columnHeader(container: HTMLElement, status: string): string {
  const column = container.querySelector(`[data-status="${status}"]`)
  const header = column?.querySelector('header')
  if (!header) throw new Error(`column not found: ${status}`)
  return header.textContent ?? ''
}

function buttonByText(container: HTMLElement, text: string): HTMLButtonElement {
  const button = [...container.querySelectorAll('button')].find((b) => b.textContent === text)
  if (!(button instanceof HTMLButtonElement)) throw new Error(`button not found: ${text}`)
  return button
}

function laneByTitle(container: HTMLElement, title: string): HTMLElement {
  const lane = [...container.querySelectorAll('section')].find(
    (section) => section.querySelector('h3')?.textContent === title,
  )
  if (!(lane instanceof HTMLElement)) throw new Error(`lane not found: ${title}`)
  return lane
}

describe('BoardView', () => {
  it('renders an epic as a card when swimlanes are off', async () => {
    const container = await renderBoard([
      issue({ id: 'epic-visible', title: 'Visible epic', type: 'epic' }),
    ])

    expect(container.querySelector('[data-card="epic-visible"]')).not.toBeNull()
  })

  /**
   * loom-d8s: the columns were pinned to a fixed width and `shrink-0`, so a
   * window wider than the board kept the surplus as an empty gutter — about
   * 680px of it at 1999px — while the cards inside clamped their titles.
   *
   * jsdom does no layout, so the widths themselves are browser-verified. What
   * this pins is the sizing contract that produces them: every column may grow,
   * none is pinned to a width, and each still carries a floor to stop shrinking
   * at (below which the board scrolls, as it always did).
   */
  it('lets every column grow into the window instead of pinning it to one width', async () => {
    const container = await renderBoard([
      issue({ id: 'task-1' }),
      issue({ id: 'task-2', status: 'in_progress' }),
    ])

    const columns = [...container.querySelectorAll<HTMLElement>('div[data-status]')]
    expect(columns.length).toBeGreaterThan(1)

    for (const column of columns) {
      // Pinned width is the regression: it is what left the gutter empty.
      expect(column.style.width).toBe('')
      expect(column.style.flexGrow).not.toBe('0')
      // Equal shares, not "whichever column holds the longest card is widest".
      // A zero basis is what makes the shares equal; jsdom serializes it '0px'.
      expect(column.style.flexBasis).toMatch(/^0(px)?$/)
      // Still a floor, so a narrow window scrolls rather than crushing them.
      expect(column.style.minWidth).not.toBe('')
    }
  })

  it('shows a topbar nudge counting only close-eligible initiatives', async () => {
    const container = await renderBoard(
      [issue({ id: 'task-1' })],
      [
        initiative('epic-ready-a', true),
        initiative('epic-not-ready', false),
        initiative('epic-ready-b', true),
      ],
    )

    expect(container.textContent).toContain('2 epics ready to close')
    expect(container.textContent).not.toContain('3 epics ready to close')
  })

  /**
   * The board is a kanban of bd status and nothing else (loom-ihl). The djinn
   * queue used to sit here as a rail between `open` and `in_progress`, and
   * every lane after the first reserved its width as a spacer — so the removal
   * has to hold in both places, or the columns keep a 208px hole where the rail
   * was.
   */
  it('carries no djinn queue, in either swimlane mode', async () => {
    const container = await renderBoard([
      issue({ id: 'epic-b', title: 'Beta active', type: 'epic' }),
      issue({
        id: 'child-b',
        title: 'Beta child',
        parent: { id: 'epic-b', title: 'Beta active' },
      }),
    ])

    const noQueue = () => {
      expect(container.querySelector('[data-rail="summon"]')).toBeNull()
      expect(container.querySelector('[aria-label="Djinn queue"]')).toBeNull()
      expect(container.textContent).not.toContain('Summon')
      // The spacer that reserved the rail's width in every lane after the
      // first: an aria-hidden box with a fixed width and no content.
      for (const box of container.querySelectorAll<HTMLElement>('[aria-hidden="true"]')) {
        expect(box.style.width).toBe('')
      }
    }

    noQueue()

    const epicMode = [...container.querySelectorAll('button')].find(
      (button) => button.textContent === 'Epic',
    )
    if (!(epicMode instanceof HTMLButtonElement)) throw new Error('Epic swimlane control not found')
    act(() => epicMode.click())

    noQueue()
  })

  it('carries no djinn queue when the filter empties the board', async () => {
    const container = await renderBoard([issue({ id: 'task-1', title: 'Alpha' })])

    const search = container.querySelector('input[type="search"]')
    if (!(search instanceof HTMLInputElement)) throw new Error('Filter input not found')
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(
        search,
        'zzz-nothing-matches',
      )
      search.dispatchEvent(new Event('input', { bubbles: true }))
    })

    expect(container.textContent).toContain('No issues match')
    expect(container.querySelector('[data-rail="summon"]')).toBeNull()
    expect(container.textContent).not.toContain('Summon')
  })

  it('collapses an empty epic lane to a header', async () => {
    const container = await renderBoard([
      issue({ id: 'epic-a', title: 'Alpha empty', type: 'epic' }),
      issue({
        id: 'epic-b',
        title: 'Beta active',
        type: 'epic',
        childStats: { total: 1, closed: 0, started: 0 },
      }),
      issue({
        id: 'child-b',
        title: 'Beta child',
        parent: { id: 'epic-b', title: 'Beta active' },
      }),
    ])

    const epicMode = [...container.querySelectorAll('button')].find(
      (button) => button.textContent === 'Epic',
    )
    if (!(epicMode instanceof HTMLButtonElement)) throw new Error('Epic swimlane control not found')
    act(() => epicMode.click())

    const emptyLane = laneByTitle(container, 'Alpha empty')
    const activeLane = laneByTitle(container, 'Beta active')
    const emptyHeader = emptyLane.querySelector('header')
    const activeHeader = activeLane.querySelector('header')

    expect(emptyHeader?.querySelector('h3')?.textContent).toBe('Alpha empty')
    expect(emptyHeader?.textContent).toContain('epic-a')
    expect(emptyHeader?.textContent).toContain('0')
    expect(emptyLane.querySelector('[data-status]')).toBeNull()

    expect(activeHeader?.querySelector('h3')?.textContent).toBe('Beta active')
    expect(activeHeader?.textContent).toContain('epic-b')
    expect(activeHeader?.textContent).toContain('1')
    expect(activeLane.querySelector('[data-status]')).not.toBeNull()
  })

  describe('when the project outgrows one page', () => {
    it('asks for a page size the server will honour instead of inheriting its default', async () => {
      await renderBoard([issue({ id: 'task-1' })])

      const limit = vi.mocked(api.issues).mock.calls[0]?.[1]?.limit
      // Server-side: an unnamed limit falls back to DefaultLimit=200 and
      // anything past MaxLimit=1000 is silently clamped down to it.
      expect(limit).toBeGreaterThan(200)
      expect(limit).toBeLessThanOrEqual(1000)
    })

    it('states the sample and scopes every count to it', async () => {
      const container = await renderBoard(
        [issue({ id: 'task-1' }), issue({ id: 'task-2', status: 'closed', ready: false })],
        [],
        6030,
      )

      const notice = container.querySelector('[data-notice="sample"]')
      expect(notice?.textContent).toContain('Loaded 2 of 6,030 issues')

      // The column count is a count of the sample and says so; "N ready" —
      // a claim about the project's startable work — is withheld outright.
      expect(columnHeader(container, 'open')).toContain('1 shown')
      expect(columnHeader(container, 'open')).not.toContain('ready')

      act(() => buttonByText(container, 'Epic').click())
      expect(laneByTitle(container, 'No epic').querySelector('header')?.textContent).toContain(
        '2 shown',
      )
    })

    it('offers hiding closed work as a real control, which narrows the request itself', async () => {
      const container = await renderBoard([issue({ id: 'task-1' })], [], 6030)

      expect(vi.mocked(api.issues).mock.calls[0]?.[1]?.includeClosed).toBe(true)
      act(() => buttonByText(container, 'hide closed').click())
      await act(async () => {
        await new Promise((resolve) => window.setTimeout(resolve, 20))
      })

      const last = vi.mocked(api.issues).mock.calls.at(-1)?.[1]
      expect(last?.includeClosed).toBeUndefined()
    })

    it('leaves a board that holds the whole project exactly as it was', async () => {
      const container = await renderBoard([issue({ id: 'task-1' })])

      expect(container.querySelector('[data-notice="sample"]')).toBeNull()
      expect(columnHeader(container, 'open')).toContain('1 ready')
      expect(columnHeader(container, 'open')).not.toContain('shown')

      act(() => buttonByText(container, 'Epic').click())
      expect(laneByTitle(container, 'No epic').querySelector('header')?.textContent).not.toContain(
        'shown',
      )
    })
  })

  describe('the Closed column window', () => {
    /**
     * A fixed clock, mid-afternoon on a Sunday — the same fixed-NOW discipline
     * `closed-window.test.ts` uses, and for the same reason. These fixtures are
     * built relative to `now`, so on an unpinned clock `ago(0, 1)` ("an hour
     * ago") lands in *yesterday* whenever the suite runs between 00:00 and
     * 01:00 local — which CI at UTC midnight hits every single day — and the
     * Today assertions below fail on a calendar boundary rather than on a bug.
     * Pinning before the fixtures are built puts the card and the window on the
     * same clock.
     */
    const NOW = new Date(2026, 7, 16, 14, 30)

    beforeEach(() => {
      // `shouldAdvanceTime` keeps real timer-driven work (react-query, the act
      // flushes) running; only the wall clock is frozen.
      vi.useFakeTimers({ shouldAdvanceTime: true })
      vi.setSystemTime(NOW)
    })

    afterEach(() => {
      vi.useRealTimers()
    })

    const ago = (days: number, hours = 0) => {
      const d = new Date()
      d.setDate(d.getDate() - days)
      d.setHours(d.getHours() - hours)
      return d.toISOString()
    }

    /** Today, three days back, three weeks back — one card per window. */
    const spread = () => [
      issue({ id: 'open-1' }),
      issue({ id: 'closed-today', status: 'closed', ready: false, closed: ago(0, 1) }),
      issue({ id: 'closed-3d', status: 'closed', ready: false, closed: ago(3) }),
      issue({ id: 'closed-3w', status: 'closed', ready: false, closed: ago(21) }),
    ]

    const chip = (container: HTMLElement, label: string): HTMLButtonElement => {
      const button = [
        ...container.querySelectorAll<HTMLButtonElement>('div[data-status="closed"] header button'),
      ].find((b) => b.textContent === label)
      if (!button) throw new Error(`window chip not found: ${label}`)
      return button
    }

    const closedCards = (container: HTMLElement) =>
      [...container.querySelectorAll('div[data-status="closed"] [data-card]')].map((c) =>
        c.getAttribute('data-card'),
      )

    it('offers the three windows on the Closed header and nowhere else', async () => {
      const container = await renderBoard(spread())

      expect(
        [
          ...container.querySelectorAll<HTMLButtonElement>(
            'div[data-status="closed"] header button',
          ),
        ].map((b) => b.textContent),
      ).toEqual(['Today', '7d', 'All'])

      for (const status of ['open', 'in_progress', 'blocked']) {
        expect(
          container.querySelectorAll(`div[data-status="${status}"] header button`),
        ).toHaveLength(0)
      }
      // The slot the chips occupy is the Open column's, and Open keeps it.
      expect(columnHeader(container, 'open')).toContain('1 ready')
    })

    it('starts narrowed, so the column is a recent log rather than the whole archive', async () => {
      const container = await renderBoard(spread())

      expect(chip(container, '7d').getAttribute('aria-pressed')).toBe('true')
      expect(chip(container, 'Today').getAttribute('aria-pressed')).toBe('false')
      expect(chip(container, 'All').getAttribute('aria-pressed')).toBe('false')
      expect(closedCards(container)).toEqual(['closed-today', 'closed-3d'])
    })

    it('moves the header count with the cards, never reporting the unfiltered total', async () => {
      const container = await renderBoard(spread())

      expect(columnHeader(container, 'closed')).toContain('2')
      expect(closedCards(container)).toHaveLength(2)

      act(() => chip(container, 'Today').click())
      expect(closedCards(container)).toEqual(['closed-today'])
      expect(columnHeader(container, 'closed')).toContain('1')

      act(() => chip(container, 'All').click())
      expect(closedCards(container)).toEqual(['closed-today', 'closed-3d', 'closed-3w'])
      expect(columnHeader(container, 'closed')).toContain('3')
    })

    it('says which window is active without needing a hover', async () => {
      const container = await renderBoard(spread())

      act(() => chip(container, 'Today').click())
      expect(chip(container, 'Today').getAttribute('aria-pressed')).toBe('true')
      expect(chip(container, '7d').getAttribute('aria-pressed')).toBe('false')
      // Not colour alone — the active chip is the only one carrying weight.
      expect(chip(container, 'Today').className).toContain('font-semibold')
      expect(chip(container, '7d').className).not.toContain('font-semibold')
    })

    it('touches nothing but the Closed column', async () => {
      const container = await renderBoard(spread())
      const before = ['open', 'in_progress', 'blocked'].map((s) => columnHeader(container, s))

      act(() => chip(container, 'Today').click())
      expect(['open', 'in_progress', 'blocked'].map((s) => columnHeader(container, s))).toEqual(
        before,
      )
      expect(container.querySelector('[data-card="open-1"]')).not.toBeNull()
    })

    it('keeps its ordering — the window filters, the comparator sorts', async () => {
      // Closed reads newest-first (loom-iq6); a narrower window must not
      // reshuffle what is left of that chronology.
      const container = await renderBoard(spread())

      act(() => chip(container, 'All').click())
      expect(closedCards(container)).toEqual(['closed-today', 'closed-3d', 'closed-3w'])
    })

    it('keeps the chips when the window empties the column, so All stays one click away', async () => {
      const container = await renderBoard([
        issue({ id: 'open-1' }),
        issue({ id: 'closed-3w', status: 'closed', ready: false, closed: ago(21) }),
      ])

      act(() => chip(container, 'Today').click())
      expect(closedCards(container)).toHaveLength(0)
      expect(chip(container, 'All')).toBeInstanceOf(HTMLButtonElement)

      act(() => chip(container, 'All').click())
      expect(closedCards(container)).toEqual(['closed-3w'])
    })
  })

  /**
   * loom-reg: `j` past the fold moved focus to cards that were nowhere on
   * screen — a keypress that looked like it had done nothing. The board now
   * reveals the focused card in every scroll container that clips it.
   *
   * jsdom performs no layout, so the board's geometry is modelled below: a
   * BOARD_W × BOARD_H viewport at the origin, lanes stacked LANE_H apart,
   * columns COL_W apart, and one fixed-height card per row. Scrolling a
   * container moves what is inside it by exactly the amount scrolled and clamps
   * at the content edge the way a browser does — so "the focused card's rect is
   * inside its container's rect" is an assertion about the deltas the board
   * actually asked for, and a board that asked for none fails it.
   */
  describe('keyboard focus and the viewport', () => {
    const CARD_H = 60
    /** Five cards fit — so a 12- or 25-card column is several viewports tall. */
    const COL_H = 300
    const COL_W = 300
    const LANE_HEADER_H = 40
    const LANE_H = 400
    /** Narrower than the four columns: reaching Blocked needs a board scroll. */
    const BOARD_W = 500
    /** Shorter than three lanes: reaching the last lane needs one too. */
    const BOARD_H = 500

    const box = (top: number, left: number, width: number, height: number): DOMRect =>
      ({
        top,
        left,
        right: left + width,
        bottom: top + height,
        width,
        height,
        x: left,
        y: top,
      }) as DOMRect

    function scrollable(el: HTMLElement, max: () => { top: number; left: number }) {
      let top = 0
      let left = 0
      Object.defineProperty(el, 'scrollTop', {
        configurable: true,
        get: () => top,
        set: (v: number) => {
          top = Math.max(0, Math.min(v, max().top))
        },
      })
      Object.defineProperty(el, 'scrollLeft', {
        configurable: true,
        get: () => left,
        set: (v: number) => {
          left = Math.max(0, Math.min(v, max().left))
        },
      })
    }

    /** Installs the model. Call it once the board is in its final layout. */
    function simulateLayout(container: HTMLElement): HTMLElement {
      const board = container.querySelector<HTMLElement>('[data-scroll="board"]')
      if (!board) throw new Error('board scroll container not found')
      const sections = [...container.querySelectorAll('section')]
      const listsIn = (lane: Element) => [
        ...lane.querySelectorAll<HTMLElement>('[data-scroll="column"]'),
      ]
      const cardsIn = (list: HTMLElement) => [...list.querySelectorAll<HTMLElement>('[data-card]')]
      const columns = sections[0] ? listsIn(sections[0]).length : 0

      scrollable(board, () => ({
        top: Math.max(0, sections.length * LANE_H - BOARD_H),
        left: Math.max(0, columns * COL_W - BOARD_W),
      }))
      for (const lane of sections) {
        for (const list of listsIn(lane)) {
          // A column scrolls vertically only — every horizontal move has to
          // come from the board, which is what the arrow-key case exercises.
          scrollable(list, () => ({
            top: Math.max(0, cardsIn(list).length * CARD_H - COL_H),
            left: 0,
          }))
        }
      }

      vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (
        this: Element,
      ) {
        const el = this as HTMLElement
        if (el === board) return box(0, 0, BOARD_W, BOARD_H)
        if (el.matches('[data-scroll="column"]')) {
          const lane = sections.indexOf(el.closest('section') as HTMLElement)
          const column = listsIn(sections[lane] as Element).indexOf(el)
          return box(
            lane * LANE_H + LANE_HEADER_H - board.scrollTop,
            column * COL_W - board.scrollLeft,
            COL_W,
            COL_H,
          )
        }
        if (el.matches('[data-card]')) {
          const list = el.closest<HTMLElement>('[data-scroll="column"]')
          if (!list) return box(0, 0, 0, 0)
          const view = list.getBoundingClientRect()
          const row = cardsIn(list).indexOf(el)
          return box(view.top + row * CARD_H - list.scrollTop, view.left + 8, COL_W - 16, CARD_H)
        }
        return box(0, 0, 0, 0)
      })

      return board
    }

    function press(key: string) {
      act(() => {
        window.dispatchEvent(new KeyboardEvent('keydown', { key }))
      })
    }

    function focusedCard(container: HTMLElement): HTMLElement {
      const el = container.querySelector<HTMLElement>('[data-card][data-focused="true"]')
      if (!el) throw new Error('no card is focused')
      return el
    }

    /** The criterion: fully inside every scroll container that clips it. */
    function expectOnScreen(card: HTMLElement) {
      const containers: HTMLElement[] = []
      for (let n = card.parentElement; n; n = n.parentElement) {
        if (n.matches('[data-scroll]')) containers.push(n)
      }
      expect(containers.length).toBeGreaterThan(0)

      const rect = card.getBoundingClientRect()
      for (const c of containers) {
        const view = c.getBoundingClientRect()
        const where = `${card.dataset.card} in the ${c.dataset.scroll} container`
        expect(rect.top, where).toBeGreaterThanOrEqual(view.top)
        expect(rect.bottom, where).toBeLessThanOrEqual(view.bottom)
        expect(rect.left, where).toBeGreaterThanOrEqual(view.left)
        expect(rect.right, where).toBeLessThanOrEqual(view.right)
      }
    }

    const column = (status: Issue['status'], n: number, labels: string[] = []): Issue[] =>
      Array.from({ length: n }, (_, i) =>
        issue({ id: `${status}-${i}`, title: `${status} ${i}`, status, labels }),
      )

    const listOf = (container: HTMLElement, status: string): HTMLElement => {
      const list = container.querySelector<HTMLElement>(
        `div[data-status="${status}"] [data-scroll="column"]`,
      )
      if (!list) throw new Error(`column list not found: ${status}`)
      return list
    }

    it('keeps all 25 j landings on screen in a column five viewports tall', async () => {
      const container = await renderBoard(column('open', 25))
      simulateLayout(container)

      press('j')
      const landed: string[] = []
      for (let n = 0; n < 25; n++) {
        const card = focusedCard(container)
        landed.push(card.dataset.card ?? '')
        expectOnScreen(card)
        press('j')
      }

      // Every card, not five and then a clamp — 25 presses, 25 distinct cards.
      expect(new Set(landed).size).toBe(25)
      // And the column really did move: an all-visible board would be a board
      // whose geometry model is broken rather than one that scrolls.
      expect(listOf(container, 'open').scrollTop).toBeGreaterThan(0)
    })

    it('follows k back up the same column', async () => {
      const container = await renderBoard(column('open', 25))
      simulateLayout(container)

      press('j')
      for (let n = 0; n < 24; n++) press('j')
      expect(listOf(container, 'open').scrollTop).toBeGreaterThan(0)

      const landed: string[] = []
      for (let n = 0; n < 25; n++) {
        const card = focusedCard(container)
        landed.push(card.dataset.card ?? '')
        expectOnScreen(card)
        press('k')
      }

      expect(new Set(landed).size).toBe(25)
      expect(listOf(container, 'open').scrollTop).toBe(0)
    })

    it('reveals a column move, at whatever depth the target column is sitting', async () => {
      const container = await renderBoard([
        ...column('open', 20),
        ...column('in_progress', 20),
        ...column('blocked', 20),
      ])
      const board = simulateLayout(container)

      press('j')
      for (let n = 0; n < 15; n++) press('j')
      expect(listOf(container, 'open').scrollTop).toBeGreaterThan(0)

      // The target column is still at depth 0 while the focus is 15 rows down,
      // so the arrow lands on a card its own column has never scrolled to.
      expect(listOf(container, 'in_progress').scrollTop).toBe(0)
      press('ArrowRight')
      expect(focusedCard(container).closest('[data-scroll="column"]')).toBe(
        listOf(container, 'in_progress'),
      )
      expectOnScreen(focusedCard(container))
      expect(listOf(container, 'in_progress').scrollTop).toBeGreaterThan(0)

      // Blocked starts past the right edge of a 500px-wide board, so this one
      // is the horizontal half of the reveal — and it is a further scroll than
      // In progress needed, which only had its right edge clipped.
      const nudged = board.scrollLeft
      press('ArrowRight')
      expect(focusedCard(container).closest('[data-scroll="column"]')).toBe(
        listOf(container, 'blocked'),
      )
      expectOnScreen(focusedCard(container))
      expect(board.scrollLeft).toBeGreaterThan(nudged)

      press('ArrowLeft')
      expect(focusedCard(container).closest('[data-scroll="column"]')).toBe(
        listOf(container, 'in_progress'),
      )
      expectOnScreen(focusedCard(container))
    })

    it('keeps focus on screen with swimlanes on, scrolling the board itself', async () => {
      const container = await renderBoard([
        ...column('open', 12, ['phase-1']).map((i) => ({ ...i, id: `p1-${i.id}` })),
        ...column('open', 12, ['phase-2']).map((i) => ({ ...i, id: `p2-${i.id}` })),
        ...column('open', 12, ['phase-3']).map((i) => ({ ...i, id: `p3-${i.id}` })),
      ])
      act(() => buttonByText(container, 'Phase').click())
      const board = simulateLayout(container)
      expect(container.querySelectorAll('section')).toHaveLength(3)

      // j walks the first lane's column, which is itself taller than its box.
      press('j')
      const landed: string[] = []
      for (let n = 0; n < 12; n++) {
        const card = focusedCard(container)
        landed.push(card.dataset.card ?? '')
        expectOnScreen(card)
        press('j')
      }
      expect(new Set(landed).size).toBe(12)

      // The last card of the last lane sits below the board's fold: reaching it
      // needs the board to scroll as well as the lane's column.
      press('G')
      const last = focusedCard(container)
      expect(last.dataset.card).toMatch(/^p3-/)
      expectOnScreen(last)
      expect(board.scrollTop).toBeGreaterThan(0)

      press('g')
      press('g')
      const first = focusedCard(container)
      expect(first.dataset.card).toMatch(/^p1-/)
      expectOnScreen(first)
      // Back at the top of the board — the card lands flush against the top
      // edge, with only the lane header still above it, because the reveal
      // chases the card and not the lane.
      expect(board.scrollTop).toBeLessThanOrEqual(LANE_HEADER_H)
    })

    it('does not move the viewport for a card that is already fully visible', async () => {
      const container = await renderBoard(column('open', 25))
      const board = simulateLayout(container)
      const list = listOf(container, 'open')

      press('j')
      // Rows 0–4 fill the 300px viewport exactly, so walking them is a walk
      // with nothing to reveal.
      for (let n = 0; n < 4; n++) {
        press('j')
        expect(list.scrollTop).toBe(0)
        expect(board.scrollLeft).toBe(0)
      }
      expectOnScreen(focusedCard(container))

      // Row 5 is the first that does not fit — it scrolls by exactly one card,
      // the nearest edge, and not by the half-viewport a re-centre would take.
      press('j')
      expect(list.scrollTop).toBe(CARD_H)

      // Coming back up to a card still on screen must not move it back.
      press('k')
      expect(list.scrollTop).toBe(CARD_H)
    })
  })
})
