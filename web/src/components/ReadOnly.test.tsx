import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Issue, Project } from '@/api/types'
import { renderWithProjects, settle, type Rendered } from '@/test/render'
import { testProject } from '@/test/projects'
import { BulkBar } from './BulkBar'
import { CloseDialog } from './CloseDialog'
import { EditMenu } from './EditMenu'
import { QuickCreate } from './QuickCreate'
import { Sidebar } from './Sidebar'
import { TopBar } from './TopBar'

// jsdom has no layout, so it has no scrollIntoView; Menu keeps its cursor row in view.
Element.prototype.scrollIntoView ??= () => undefined

vi.mock('./Live', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./Live')>()),
  ConnectionIndicator: () => null,
}))

const REASON =
  'Loom reads database "loom_db" but bd writes to database "bd_db" on 127.0.0.1:3311, so a write would land where Loom never shows it'

const readOnly = (): Project => ({ ...testProject('loom'), writable: false, writeReason: REASON })
const writable = (): Project => testProject('loom')

const issue = { id: 'loom-7', title: 'Seven', status: 'open', priority: 2, labels: [], assignee: '' } as unknown as Issue

let rendered: Rendered | undefined
afterEach(() => {
  rendered?.unmount()
  rendered = undefined
})

async function render(ui: ReactNode, project: Project) {
  rendered = await renderWithProjects(ui, { projects: [project], current: project.name, path: '/loom/backlog' })
  await settle()
  return rendered.container
}

const buttonNamed = (root: ParentNode, text: string) => {
  const found = [...root.querySelectorAll<HTMLButtonElement>('button')].find((b) =>
    b.textContent?.trim().startsWith(text),
  )
  if (!found) throw new Error(`no button "${text}" in ${root.textContent}`)
  return found
}

const badge = (root: ParentNode) =>
  [...root.querySelectorAll<HTMLElement>('.chip')].find((c) => c.textContent === 'Read-only')

describe.each([
  ['read-only', readOnly, true],
  ['writable', writable, false],
] as const)('a %s project', (_, fixture, blocked) => {
  it(blocked ? 'shows a Read-only badge in the top bar whose tooltip is the reason' : 'shows no Read-only badge in the top bar', async () => {
    const project = fixture()
    const root = await render(<TopBar project={project} />, project)
    const chip = badge(root)
    if (blocked) expect(chip?.title).toBe(REASON)
    else expect(chip).toBeUndefined()
  })

  it(blocked ? 'shows a Read-only badge in the sidebar beside the health chip' : 'shows no Read-only badge in the sidebar', async () => {
    const project = fixture()
    const root = await render(
      <Sidebar
        projects={[project]}
        loading={false}
        onReorder={() => undefined}
        view="backlog"
        currentProject="loom"
        theme="loom-dark"
        onToggleTheme={() => undefined}
        onShowHelp={() => undefined}
        onOpenPalette={() => undefined}
        onOpenSettings={() => undefined}
      />,
      project,
    )
    const chip = badge(root)
    if (blocked) {
      expect(chip?.title).toBe(REASON)
      // Beside the health readout: the same row as the open count.
      expect(chip?.parentElement?.textContent).toContain(String(project.health?.open))
    } else {
      expect(chip).toBeUndefined()
    }
  })

  it(blocked ? 'disables Create in QuickCreate with the reason as its title' : 'leaves Create enabled in QuickCreate', async () => {
    const project = fixture()
    const root = await render(<QuickCreate project="loom" issues={[]} onClose={vi.fn()} onCreated={vi.fn()} />, project)
    const title = document.querySelector<HTMLInputElement>('input[aria-label="Title"]')!
    await typeInto(title, 'A new bead')
    const create = buttonNamed(document.body, 'Create issue')
    expect(create.disabled).toBe(blocked)
    expect(create.title).toBe(blocked ? REASON : '')
    expect(root).toBeDefined()
  })

  it(blocked ? 'disables every EditMenu option with the reason as its title' : 'leaves EditMenu options enabled', async () => {
    const project = fixture()
    for (const kind of ['status', 'priority', 'assignee', 'labels'] as const) {
      await render(
        <EditMenu
          project="loom"
          request={{ kind, anchor: new DOMRect(0, 0, 10, 10), ids: ['loom-7'] }}
          issues={[issue]}
          onClose={vi.fn()}
          onRequestIssueClose={vi.fn()}
        />,
        project,
      )
      const options = [...document.querySelectorAll<HTMLButtonElement>('[role="option"]')]
      expect(options.length, kind).toBeGreaterThan(0)
      for (const o of options) {
        expect(o.disabled, `${kind} ${o.textContent}`).toBe(blocked)
        expect(o.title, kind).toBe(blocked ? REASON : '')
      }
      rendered?.unmount()
      rendered = undefined
    }
  })

  it(blocked ? 'disables Close in CloseDialog with the reason as its title' : 'leaves Close enabled in CloseDialog', async () => {
    const project = fixture()
    await render(<CloseDialog project="loom" ids={['loom-7']} onClose={vi.fn()} />, project)
    await typeInto(document.querySelector<HTMLInputElement>('input.field')!, 'done')
    const close = buttonNamed(document.body, 'Close issue')
    expect(close.disabled).toBe(blocked)
    expect(close.title).toBe(blocked ? REASON : '')
  })

  it(blocked ? 'disables the BulkBar write controls with the reason as their title' : 'leaves the BulkBar write controls enabled', async () => {
    const project = fixture()
    const root = await render(
      <BulkBar project="loom" count={2} onOpenMenu={vi.fn()} onClose={vi.fn()} onClear={vi.fn()} />,
      project,
    )
    for (const name of ['Status', 'Priority', 'Assignee', 'Labels', 'Close…']) {
      const b = buttonNamed(root, name)
      expect(b.disabled, name).toBe(blocked)
      if (blocked) expect(b.title, name).toBe(REASON)
      else expect(b.title, name).not.toBe(REASON)
    }
    // Clearing the selection writes nothing, so it stays available.
    expect(buttonNamed(root, 'esc').disabled).toBe(false)
  })
})

async function typeInto(input: HTMLInputElement, value: string) {
  const { act } = await import('react')
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
  act(() => {
    setter.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await settle()
}
