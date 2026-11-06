import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { BUILTIN_SCHEMA, CORE_TYPES, type Issue, type IssueDetail, type ProjectSchema } from '@/api/types'
import { statusLabel } from '@/lib/beads'
import { EditMenu } from './EditMenu'
import { ToastProvider } from './Toast'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
// jsdom has no layout, so it has no scrollIntoView; Menu keeps its cursor row in view.
Element.prototype.scrollIntoView ??= () => undefined

const schema: ProjectSchema = {
  statuses: [...BUILTIN_SCHEMA.statuses, { name: 'in_review', category: 'active' }],
  types: CORE_TYPES,
}

const issue = { id: 'loom-7', title: 'Seven', status: 'open', labels: [], assignee: '' } as unknown as Issue

const reply = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })

/** Stubs fetch: /schema answers with `answer`; a PATCH records its body. */
function stubFetch(answer: () => Promise<Response>) {
  const patches: unknown[] = []
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    if (url.endsWith('/api/projects/loom/schema')) return answer()
    if (url.endsWith('/api/projects/loom/issues/loom-7') && init?.method === 'PATCH') {
      patches.push(JSON.parse(String(init.body)))
      return reply(200, { ...issue, status: 'in_review' } as IssueDetail)
    }
    return reply(404, { error: 'unexpected ' + url })
  })
  vi.stubGlobal('fetch', fetchMock)
  return { fetchMock, patches }
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
          <EditMenu
            project="loom"
            request={{ kind: 'status', anchor: new DOMRect(0, 0, 10, 10), ids: ['loom-7'] }}
            issues={[issue]}
            onClose={vi.fn()}
            onRequestIssueClose={vi.fn()}
          />
        </ToastProvider>
      </QueryClientProvider>,
    ),
  )
}

async function flush() {
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)) })
}

const options = () => [...document.querySelectorAll<HTMLButtonElement>('[role="option"]')]
/** The row's label, without the "needs reason" hint on closed. */
const optionTexts = () => options().map((o) => o.querySelector('span')?.textContent?.trim())
const builtinLabels = BUILTIN_SCHEMA.statuses.map((s) => statusLabel(s.name))

describe('EditMenu status menu (loom-k7q)', () => {
  it('lists a custom status with a visible dot, and picking it submits that status', async () => {
    const { patches } = stubFetch(async () => reply(200, schema))
    render()
    await flush()

    expect(optionTexts()).toEqual([...builtinLabels, 'In review'])
    const row = options().at(-1)!
    const dot = row.querySelector<HTMLElement>('.dot')
    expect(dot?.dataset.status).toBe('in_review')

    act(() => row.click())
    await flush()
    expect(patches).toEqual([{ status: 'in_review' }])
  })

  it('gives a status with no theme token a neutral colour, never an invisible swatch', () => {
    // jsdom applies no stylesheet, so read the rule itself: the bare
    // [data-status] selector must bind --c before any per-status rule does.
    const css = readFileSync(resolve(process.cwd(), 'src/index.css'), 'utf8')
    const fallback = /\[data-status\]\s*\{\s*--c:\s*var\((--[\w-]+)\)/.exec(css)
    expect(fallback?.[1]).toBe('--status-open')
    expect(css).not.toMatch(/\[data-status='in_review'\]/)
    expect(css).toMatch(/\.dot\s*\{[^}]*box-shadow:[^;]*var\(--c\)/)
  })

  it('shows the built-ins while the schema request is still loading', async () => {
    stubFetch(() => new Promise<Response>(() => {}))
    render()
    await flush()

    expect(optionTexts()).toEqual(builtinLabels)
  })

  it('falls back to the built-ins on a 5xx, with no toast and no console error', async () => {
    const consoleError = vi.spyOn(console, 'error')
    const { fetchMock } = stubFetch(async () => reply(500, { error: 'schema query failed' }))
    render()
    await flush()

    expect(fetchMock).toHaveBeenCalled()
    expect(optionTexts()).toEqual(builtinLabels)
    expect(document.body.textContent).not.toMatch(/schema query failed|Could not/)
    expect(consoleError).not.toHaveBeenCalled()
  })
})
