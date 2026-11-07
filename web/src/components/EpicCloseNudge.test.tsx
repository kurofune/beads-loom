import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Initiative } from '@/api/types'
import { EpicCloseNudge } from './EpicCloseNudge'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const mocks = vi.hoisted(() => ({
  interactionLock: vi.fn(),
  closeIDs: [] as string[],
}))

vi.mock('./Live', () => ({
  useInteractionLock: (active: boolean) => mocks.interactionLock(active),
}))

vi.mock('./CloseDialog', () => ({
  CloseDialog: ({ ids }: { ids: string[] }) => {
    mocks.closeIDs = ids
    return <div data-testid="close-dialog">{ids.join(',')}</div>
  },
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
  subtree: { total: 3, closed: 3, started: 0, ready: 0, depBlocked: 0, deferScheduled: 0, chained: 0 },
  closeEligible: true,
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
  mocks.interactionLock.mockClear()
  mocks.closeIDs = []
})

function render(eligible: Initiative[], readOnly = false): HTMLElement {
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  act(() => root?.render(
    <EpicCloseNudge project="loom" eligible={eligible} readOnly={readOnly} />,
  ))
  return container
}

function click(element: Element | null): void {
  if (!(element instanceof HTMLElement)) throw new Error('click target not found')
  act(() => element.click())
}

describe('EpicCloseNudge', () => {
  it('stays absent with no eligible epics or on a read-only board', () => {
    const empty = render([])
    expect(empty.textContent).toBe('')

    act(() => root?.unmount())
    root = undefined
    document.body.replaceChildren()
    const readOnly = render([initiative('epic-1')], true)
    expect(readOnly.textContent).toBe('')
  })

  it('shows the eligible count and dismisses the current set', () => {
    const container = render([initiative('epic-1'), initiative('epic-2')])

    expect(container.textContent).toContain('2 epics ready to close')
    click(container.querySelector('[aria-label="Dismiss"]'))
    expect(container.textContent).toBe('')
    expect(sessionStorage.getItem('loom.epicNudge.loom')).toBe('epic-1,epic-2')
  })

  it('keeps the same set dismissed across remount but shows a changed set', () => {
    render([initiative('epic-b'), initiative('epic-a')])
    click(document.querySelector('[aria-label="Dismiss"]'))

    act(() => root?.unmount())
    root = undefined
    document.body.replaceChildren()
    const same = render([initiative('epic-a'), initiative('epic-b')])
    expect(same.textContent).toBe('')

    act(() => root?.unmount())
    root = undefined
    document.body.replaceChildren()
    const changed = render([initiative('epic-a'), initiative('epic-c')])
    expect(changed.textContent).toContain('2 epics ready to close')
  })

  it('stays usable when session storage rejects reads and writes', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('SecurityError')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceededError')
    })

    const container = render([initiative('epic-1')])
    expect(container.textContent).toContain('1 epic ready to close')
    expect(() => click(container.querySelector('[aria-label="Dismiss"]'))).not.toThrow()
    expect(container.textContent).toBe('')
  })

  it('lists each eligible epic with its id, title, and closed-total count', () => {
    const container = render([initiative('epic-1')])

    click([...container.querySelectorAll('button')].find((button) =>
      button.textContent?.includes('1 epic ready to close'),
    ) ?? null)

    const row = container.querySelector('input[type="checkbox"]')?.closest('label')
    expect(row?.textContent).toContain('epic-1')
    expect(row?.textContent).toContain('Title epic-1')
    expect(row?.textContent).toContain('3/3')
  })

  it('uses a singular confirmation label when exactly one epic is selected', () => {
    const container = render([initiative('epic-1')])

    click([...container.querySelectorAll('button')].find((button) =>
      button.textContent?.includes('1 epic ready to close'),
    ) ?? null)

    const confirm = [...container.querySelectorAll('button')].find((button) =>
      button.textContent?.startsWith('Close '),
    )
    expect(confirm?.textContent).toBe('Close 1 epic…')
  })

  it('caps and scrolls 30 rows while keeping confirmation outside the list', () => {
    const eligible = Array.from({ length: 30 }, (_, index) => initiative(`epic-${index + 1}`))
    const container = render(eligible)

    click([...container.querySelectorAll('button')].find((button) =>
      button.textContent?.includes('30 epics ready to close'),
    ) ?? null)

    const scroll = container.querySelector('[data-testid="eligible-epics-scroll"]')
    const confirm = [...container.querySelectorAll('button')].find((button) =>
      button.textContent?.includes('Close 30 epics'),
    )
    expect(scroll?.querySelectorAll('input[type="checkbox"]')).toHaveLength(30)
    expect(confirm).toBeInstanceOf(HTMLButtonElement)
    expect(scroll?.contains(confirm ?? null)).toBe(false)
    expect(mocks.interactionLock).toHaveBeenLastCalledWith(true)
  })

  it('hands only the checked epic ids to the existing close dialog', () => {
    const container = render([
      initiative('epic-1'),
      initiative('epic-2'),
      initiative('epic-3'),
    ])
    click([...container.querySelectorAll('button')].find((button) =>
      button.textContent?.includes('3 epics ready to close'),
    ) ?? null)

    const checkboxes = container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')
    expect(checkboxes).toHaveLength(3)
    expect([...checkboxes].every((checkbox) => checkbox.checked)).toBe(true)
    act(() => {
      checkboxes[1]!.click()
    })
    click([...container.querySelectorAll('button')].find((button) =>
      button.textContent?.includes('Close 2 epics'),
    ) ?? null)

    expect(container.querySelector('[data-testid="close-dialog"]')?.textContent)
      .toBe('epic-1,epic-3')
    expect(mocks.closeIDs).toEqual(['epic-1', 'epic-3'])
  })
})
