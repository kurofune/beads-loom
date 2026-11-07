import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError } from '@/api/client'
import type { Initiative, IssueDetail, IssuePatch } from '@/api/types'
import { NO_TARGET_LABEL } from '@/lib/target-nudge'
import { ToastProvider } from './Toast'
import { dateInputValue, EpicTargetPanel } from './EpicTargetNudge'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const mocks = vi.hoisted(() => ({ interactionLock: vi.fn() }))

vi.mock('./Live', () => ({
  useInteractionLock: (active: boolean) => mocks.interactionLock(active),
}))

const initiative = (id: string): Initiative => ({
  id,
  title: `Title ${id}`,
  status: 'open',
  priority: 2,
  assignee: '',
  due: null,
  updated: '2026-08-01T00:00:00Z',
  lastActivity: '2026-08-02T00:00:00Z',
  subtree: { total: 4, closed: 1, started: 2, ready: 1, depBlocked: 0, deferScheduled: 0, chained: 0 },
  closeEligible: false,
  needsTarget: true,
  health: { state: 'no-target', explanation: 'No due date — no target to assess' },
  noReceipt: { total: 0, noAcceptance: 0, noCloseReason: 0 },
})

// The write path only reads `id` off the response, to key the detail cache.
const patched = (id: string) => ({ id }) as IssueDetail

let root: Root | undefined
let closed = 0
let dismissed: string[] = []

beforeEach(() => {
  closed = 0
  dismissed = []
  vi.useFakeTimers({ now: new Date('2026-08-10T09:00:00Z') })
})

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = undefined
  document.body.replaceChildren()
  vi.useRealTimers()
  vi.restoreAllMocks()
  mocks.interactionLock.mockClear()
})

async function renderPanel(id = 'epic-1'): Promise<HTMLElement> {
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  await act(async () => {
    root?.render(
      <QueryClientProvider client={qc}>
        <ToastProvider>
          <EpicTargetPanel
            project="loom"
            initiative={initiative(id)}
            onClose={() => {
              closed += 1
            }}
            onDismiss={(epic) => dismissed.push(epic)}
          />
        </ToastProvider>
      </QueryClientProvider>,
    )
  })
  return container
}

function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll('button')].find(
    (node) => node.textContent?.trim() === label,
  )
  if (!(found instanceof HTMLButtonElement)) throw new Error(`no button labelled ${label}`)
  return found
}

async function click(label: string): Promise<void> {
  const target = button(label)
  await act(async () => {
    target.click()
  })
}

function dateField(): HTMLInputElement {
  const input = document.querySelector('input[type="date"]')
  if (!(input instanceof HTMLInputElement)) throw new Error('date field not found')
  return input
}

/** Type a date the way the browser's date input reports it. */
async function setDate(value: string): Promise<void> {
  const input = dateField()
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
    setter?.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

/** A promise the test resolves or rejects when it chooses. */
function deferred<T>() {
  let settle!: (value: T) => void
  let fail!: (err: unknown) => void
  const promise = new Promise<T>((resolve, reject) => {
    settle = resolve
    fail = reject
  })
  return { promise, settle, fail }
}

describe('dateInputValue', () => {
  it('pads single-digit months and days', () => {
    expect(dateInputValue(new Date(2026, 0, 5))).toBe('2026-01-05')
    expect(dateInputValue(new Date(2026, 10, 9))).toBe('2026-11-09')
  })

  it('rolls over month and year boundaries when days are added', () => {
    expect(dateInputValue(new Date(2026, 0, 25), 14)).toBe('2026-02-08')
    expect(dateInputValue(new Date(2026, 11, 28), 7)).toBe('2027-01-04')
    // 2028 is a leap year, so the 30-day offset lands on the 29th.
    expect(dateInputValue(new Date(2028, 1, 1), 30)).toBe('2028-03-02')
  })
})

describe('EpicTargetPanel', () => {
  it('holds the interaction lock and pre-fills a date two weeks out', async () => {
    await renderPanel()

    // SCAR loom-323: an open editor must park SSE-driven refetches.
    expect(mocks.interactionLock).toHaveBeenCalledWith(true)
    expect(dateField().value).toBe(dateInputValue(new Date(), 14))
    expect(dateField().getAttribute('min')).toBe(dateInputValue(new Date()))
  })

  it('sets the target with the date on screen and closes', async () => {
    const patch = vi.spyOn(api, 'patchIssue').mockResolvedValue(patched('epic-1'))
    await renderPanel()

    await setDate('2026-09-30')
    await click('Set target')

    expect(patch).toHaveBeenCalledTimes(1)
    expect(patch).toHaveBeenCalledWith('loom', 'epic-1', { due: '2026-09-30' } satisfies IssuePatch)
    expect(closed).toBe(1)
  })

  it('sends the preset offset the manager clicked, not the pre-filled default', async () => {
    const patch = vi.spyOn(api, 'patchIssue').mockResolvedValue(patched('epic-1'))
    await renderPanel()

    await click('1 week')
    expect(dateField().value).toBe(dateInputValue(new Date(), 7))
    await click('Set target')

    expect(patch).toHaveBeenCalledWith('loom', 'epic-1', { due: dateInputValue(new Date(), 7) })
  })

  it('submits on Enter in the date field', async () => {
    const patch = vi.spyOn(api, 'patchIssue').mockResolvedValue(patched('epic-1'))
    await renderPanel()

    await setDate('2026-09-30')
    await act(async () => {
      dateField().dispatchEvent(
        new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }),
      )
    })

    expect(patch).toHaveBeenCalledWith('loom', 'epic-1', { due: '2026-09-30' })
    expect(closed).toBe(1)
  })

  it('marks the epic untimed by adding the bd label, and closes', async () => {
    const patch = vi.spyOn(api, 'patchIssue').mockResolvedValue(patched('epic-1'))
    await renderPanel()

    await click('Not time-boxed')

    expect(patch).toHaveBeenCalledTimes(1)
    expect(patch).toHaveBeenCalledWith('loom', 'epic-1', {
      labelsAdd: [NO_TARGET_LABEL],
    } satisfies IssuePatch)
    expect(closed).toBe(1)
  })

  it('keeps the panel open and surfaces bd stderr when the date write fails', async () => {
    vi.spyOn(api, 'patchIssue').mockRejectedValue(
      new ApiError('patch failed', 422, { stderr: 'bd: issue epic-1 is already closed' }),
    )
    await renderPanel()

    await setDate('2026-09-30')
    await click('Set target')

    expect(closed).toBe(0)
    expect(document.querySelector('[role="dialog"]')).not.toBeNull()
    const toast = document.querySelector('[role="status"]')
    expect(toast?.textContent).toContain('Could not set a due date on epic-1')
    // Verbatim, never reworded — it is the whole explanation.
    expect(toast?.textContent).toContain('bd: issue epic-1 is already closed')
    // The buttons come back, so the manager can retry rather than being stuck.
    expect(button('Set target').disabled).toBe(false)
  })

  it('keeps the panel open and reports failure when the untimed label write fails', async () => {
    vi.spyOn(api, 'patchIssue').mockRejectedValue(
      new ApiError('patch failed', 500, { stderr: 'bd: database is locked' }),
    )
    await renderPanel()

    await click('Not time-boxed')

    expect(closed).toBe(0)
    expect(document.querySelector('[role="dialog"]')).not.toBeNull()
    const toast = document.querySelector('[role="status"]')
    expect(toast?.textContent).toContain(`Could not label epic-1 ${NO_TARGET_LABEL}`)
    expect(toast?.textContent).toContain('bd: database is locked')
  })

  it('sends one write while a write is in flight, whichever button is pressed', async () => {
    const gate = deferred<IssueDetail>()
    const patch = vi.spyOn(api, 'patchIssue').mockReturnValue(gate.promise)
    await renderPanel()

    await click('Set target')
    expect(patch).toHaveBeenCalledTimes(1)
    expect(button('Saving…').disabled).toBe(true)
    expect(button('Not time-boxed').disabled).toBe(true)
    expect(button('Dismiss').disabled).toBe(true)

    // Disabling the buttons is only half of it: the date field stays enabled,
    // so Enter reaches setTarget directly and a leaning-on-Enter manager would
    // fire a second write that races the first. The busy guard is what stops it.
    await act(async () => {
      dateField().dispatchEvent(
        new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }),
      )
    })
    expect(patch).toHaveBeenCalledTimes(1)

    await act(async () => {
      gate.settle(patched('epic-1'))
      await gate.promise
    })
    expect(closed).toBe(1)
  })

  it('dismisses per epic without writing anything to the bead', async () => {
    const patch = vi.spyOn(api, 'patchIssue')
    await renderPanel('epic-7')

    await click('Dismiss')

    expect(dismissed).toEqual(['epic-7'])
    expect(closed).toBe(1)
    expect(patch).not.toHaveBeenCalled()
  })

  it('refuses to write an empty date', async () => {
    const patch = vi.spyOn(api, 'patchIssue')
    await renderPanel()

    await setDate('')

    expect(button('Set target').disabled).toBe(true)
    await click('Set target')
    expect(patch).not.toHaveBeenCalled()
    expect(closed).toBe(0)
  })
})
