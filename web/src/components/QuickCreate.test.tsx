import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { CORE_TYPES, type ProjectSchema } from '@/api/types'
import { QuickCreate } from './QuickCreate'
import { ToastProvider } from './Toast'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
// jsdom has no layout, so it has no scrollIntoView; Menu keeps its cursor row in view.
Element.prototype.scrollIntoView ??= () => undefined

const schema: ProjectSchema = {
  statuses: [{ name: 'open', category: 'active' }],
  types: [...CORE_TYPES, 'rfc'],
}

const reply = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })

/** Stubs fetch: /schema answers with `answer`; anything else is a test bug. */
function stubSchema(answer: () => Promise<Response>) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.endsWith('/api/projects/loom/schema')) return answer()
    return reply(404, { error: 'unexpected ' + url })
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

let root: Root | undefined

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = undefined
  document.body.replaceChildren()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
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
          <QuickCreate project="loom" issues={[]} onClose={vi.fn()} onCreated={vi.fn()} />
        </ToastProvider>
      </QueryClientProvider>,
    ),
  )
}

async function flush() {
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)) })
}

/** Text a reader sees: a glyph's <title> is for assistive tech, not the label. */
const visibleText = (el: Element) => {
  const copy = el.cloneNode(true) as Element
  copy.querySelectorAll('svg').forEach((svg) => svg.remove())
  return copy.textContent?.trim() ?? ''
}

/** The type button in the attribute row: the dialog button whose text is a type. */
const typeButton = () =>
  [...document.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.closest('[aria-label="Type"]') === null && [...CORE_TYPES, 'rfc'].includes(visibleText(b)),
  )!

const openTypeMenu = () => act(() => typeButton().click())

const menuOptions = () =>
  [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"][aria-label="Type"] [role="option"]')]

const optionTexts = () => menuOptions().map(visibleText)

const press = (key: string) =>
  act(() => {
    document
      .querySelector('[role="dialog"][aria-label="Type"] input')!
      .dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }))
  })

describe('QuickCreate type menu (loom-k7q)', () => {
  it('lists a project custom type after the nine core types, and ArrowDown + Enter picks it', async () => {
    stubSchema(async () => reply(200, schema))
    render()
    await flush()
    openTypeMenu()

    expect(optionTexts()).toEqual([...CORE_TYPES, 'rfc'])
    // A custom type has no hand-drawn mark: the fallback glyph is the circle.
    const rfc = menuOptions()[9]!
    expect(rfc.querySelector('svg circle')).not.toBeNull()
    expect(rfc.querySelector('svg path')).toBeNull()

    // The menu opens on the current value (task, row 0); rfc is nine rows down.
    for (let i = 0; i < 9; i++) press('ArrowDown')
    press('Enter')

    expect(menuOptions()).toHaveLength(0)
    expect(visibleText(typeButton())).toBe('rfc')
  })

  it('shows the built-ins while the schema request is still loading', async () => {
    stubSchema(() => new Promise<Response>(() => {}))
    render()
    await flush()
    openTypeMenu()

    expect(optionTexts()).toEqual(CORE_TYPES)
  })

  it('falls back to the built-ins on a 5xx, with no toast and no console error', async () => {
    const consoleError = vi.spyOn(console, 'error')
    const fetchMock = stubSchema(async () => reply(503, { error: 'dolt server unreachable' }))
    render()
    await flush()
    openTypeMenu()

    expect(fetchMock).toHaveBeenCalled()
    expect(optionTexts()).toEqual(CORE_TYPES)
    expect(document.querySelector('[role="status"], [role="alert"]')?.textContent ?? '').not.toMatch(
      /schema|unreachable/i,
    )
    expect(document.body.textContent).not.toMatch(/unreachable|Could not/)
    expect(consoleError).not.toHaveBeenCalled()
  })
})
