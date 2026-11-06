import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { CloseClaimedConflict, IssueDetail } from '@/api/types'
import { CloseDialog } from './CloseDialog'
import { ToastProvider } from './Toast'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const conflict: CloseClaimedConflict = {
  error: 'loom-7 is claimed by alice; close it anyway to override the claim',
  stderr: 'cannot close loom-7: assignee is "alice", actor is "Nate Gross"; reclaim or use --force to override',
  assignee: 'alice',
}

const closedDetail = { id: 'loom-7', status: 'closed' } as IssueDetail

const reply = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })

/** Stubs fetch: the first close is refused as another actor's claim, every later one succeeds. */
function stubClose() {
  const bodies: unknown[] = []
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    if (!url.endsWith('/issues/loom-7/close')) return reply(404, { error: 'unexpected ' + url })
    bodies.push(JSON.parse(String(init?.body)))
    return bodies.length === 1 ? reply(409, conflict) : reply(200, closedDetail)
  })
  vi.stubGlobal('fetch', fetchMock)
  return bodies
}

let root: Root | undefined

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = undefined
  document.body.replaceChildren()
  vi.unstubAllGlobals()
})

function render(onClose = vi.fn(), onClosed = vi.fn()) {
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  act(() =>
    root?.render(
      <QueryClientProvider client={qc}>
        <ToastProvider>
          <CloseDialog project="loom" ids={['loom-7']} onClose={onClose} onClosed={onClosed} />
        </ToastProvider>
      </QueryClientProvider>,
    ),
  )
  return { onClose, onClosed }
}

const input = () => document.querySelector<HTMLInputElement>('[role="dialog"] input')!

function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.textContent?.trim() === label,
  )
  if (!found) throw new Error(`no button "${label}"; have: ${[...document.querySelectorAll('button')].map((b) => b.textContent).join(' | ')}`)
  return found
}

function typeReason(text: string) {
  const el = input()
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
  act(() => {
    setter.call(el, text)
    el.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

/** Lets fetch, the mutation and React settle. */
async function flush() {
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)) })
}

async function submitAndHitTheClaim() {
  typeReason('superseded by loom-9')
  act(() => button('Close issue').click())
  await flush()
}

describe('CloseDialog on another actor’s claim', () => {
  it('turns the 409 into a "Close anyway?" confirm that names the assignee, without forcing', async () => {
    const bodies = stubClose()
    const { onClose } = render()

    await submitAndHitTheClaim()

    expect(bodies).toEqual([{ reason: 'superseded by loom-9' }])
    const dialog = document.querySelector('[role="dialog"]')!
    expect(dialog.textContent).toContain('Close anyway?')
    expect(dialog.textContent).toContain('alice')
    expect(onClose).not.toHaveBeenCalled()
  })

  it('confirming re-sends the close with force: true and closes the dialog on 200', async () => {
    const bodies = stubClose()
    const { onClose, onClosed } = render()
    await submitAndHitTheClaim()

    act(() => button('Close anyway').click())
    await flush()

    expect(bodies).toEqual([
      { reason: 'superseded by loom-9' },
      { reason: 'superseded by loom-9', force: true },
    ])
    expect(onClosed).toHaveBeenCalledWith(['loom-7'])
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('dismissing the confirm sends nothing more and keeps the dialog open with the reason intact', async () => {
    const bodies = stubClose()
    const { onClose } = render()
    await submitAndHitTheClaim()

    act(() => button('Go back').click())
    await flush()

    expect(bodies).toHaveLength(1)
    expect(onClose).not.toHaveBeenCalled()
    expect(document.querySelector('[role="dialog"]')).not.toBeNull()
    expect(document.querySelector('[role="dialog"]')!.textContent).not.toContain('Close anyway?')
    expect(input().value).toBe('superseded by loom-9')
  })

  it('Enter in the reason field while the confirm is up sends nothing', async () => {
    const bodies = stubClose()
    render()
    await submitAndHitTheClaim()

    act(() => {
      input().dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    })
    await flush()

    expect(bodies).toHaveLength(1)
    expect(document.querySelector('[role="dialog"]')!.textContent).toContain('Close anyway?')
  })

  it('Escape on the confirm dismisses only the confirm, not the dialog', async () => {
    const bodies = stubClose()
    const { onClose } = render()
    await submitAndHitTheClaim()

    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    await flush()

    expect(bodies).toHaveLength(1)
    expect(onClose).not.toHaveBeenCalled()
    expect(document.querySelector('[role="dialog"]')!.textContent).not.toContain('Close anyway?')
    expect(input().value).toBe('superseded by loom-9')
  })
})
