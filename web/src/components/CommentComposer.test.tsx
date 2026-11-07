import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { CommentComposer } from './CommentComposer'
import { ToastProvider } from './Toast'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const reply = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })

let root: Root | undefined

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = undefined
  document.body.replaceChildren()
  vi.unstubAllGlobals()
})

function render() {
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  act(() =>
    root?.render(
      <QueryClientProvider client={qc}>
        <ToastProvider>
          <CommentComposer project="loom" id="loom-1" />
        </ToastProvider>
      </QueryClientProvider>,
    ),
  )
  return container
}

async function flush() {
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)) })
}

describe('CommentComposer submit key and IME composition (loom-gkp)', () => {
  it('posts nothing on a composing Enter, keeps the text, and posts once on the next ⌘↵', async () => {
    const posted: unknown[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        if (url.endsWith('/api/projects/loom/issues/loom-1/comments') && init?.method === 'POST') {
          posted.push(JSON.parse(String(init.body)))
          return reply(200, { id: 'loom-1', comments: [] })
        }
        return reply(404, { error: 'unexpected ' + url })
      }),
    )
    const container = render()
    const box = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Add a comment"]')!
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set?.call(box, '再現しました')
      box.dispatchEvent(new Event('input', { bubbles: true }))
    })

    const composingEnters = [
      new KeyboardEvent('keydown', { key: 'Enter', isComposing: true, bubbles: true, cancelable: true }),
      new KeyboardEvent('keydown', { key: 'Enter', isComposing: true, metaKey: true, bubbles: true, cancelable: true }),
      // Safari reports isComposing false on the confirming keydown, keyCode 229.
      new KeyboardEvent('keydown', { key: 'Enter', keyCode: 229, ctrlKey: true, bubbles: true, cancelable: true }),
    ]
    for (const composing of composingEnters) {
      act(() => {
        box.dispatchEvent(composing)
      })
      await flush()
      expect(composing.defaultPrevented).toBe(false)
      expect(posted).toHaveLength(0)
      expect(box.value).toBe('再現しました')
    }

    act(() => {
      box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', metaKey: true, bubbles: true, cancelable: true }))
    })
    await flush()
    expect(posted).toEqual([{ text: '再現しました' }])
  })
})
