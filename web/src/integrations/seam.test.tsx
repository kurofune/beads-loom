import { act } from 'react'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import type { Issue, NowResponse, Project } from '@/api/types'
import { CheatSheet } from '@/components/CheatSheet'
import { EditMenu } from '@/components/EditMenu'
import { QuickCreate } from '@/components/QuickCreate'
import { Sidebar } from '@/components/Sidebar'
import { TopBar } from '@/components/TopBar'
import { NowView } from '@/views/NowView'
import { DJINN, testProject } from '@/test/projects'
import { renderWithProjects, until, type Rendered } from '@/test/render'

/**
 * The seam, component by component: with no djinn key in a project's
 * `integrations`, no djinn UI renders — absent, not disabled — and with djinn
 * it comes back. Text assertions, so a renamed chip still has to pass.
 */

const plain = testProject('plain')
const loom = testProject('loom', DJINN)

let view: Rendered | undefined
// The pickers scroll their cursor row into view; jsdom has no layout.
beforeAll(() => {
  Element.prototype.scrollIntoView ??= () => undefined
})
afterEach(() => {
  view?.unmount()
  view = undefined
})

const issue = (id: string, labels: string[] = []): Issue =>
  ({
    id,
    title: `title ${id}`,
    type: 'task',
    status: 'open',
    priority: 2,
    assignee: '',
    labels,
    estimate: null,
    created: '2026-10-01T00:00:00Z',
    updated: '2026-10-01T00:00:00Z',
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
  }) as Issue

const chips = (root: ParentNode) => [...root.querySelectorAll('.chip')].map((c) => c.textContent?.trim())

describe('TopBar', () => {
  it('renders no Runs tab and no djinn chip for a project without djinn', async () => {
    view = await renderWithProjects(<TopBar project={plain} />, { projects: [plain] })
    const tabs = [...view.container.querySelectorAll('[role="tab"]')].map((t) => t.textContent)
    expect(tabs).not.toContain('Runs')
    expect(chips(view.container)).not.toContain('djinn')
    expect(view.container.textContent).not.toMatch(/djinn/i)
  })

  it('renders the Runs tab, enabled, and the djinn chip for a djinn project', async () => {
    view = await renderWithProjects(<TopBar project={loom} />, { projects: [loom] })
    const runs = [...view.container.querySelectorAll<HTMLButtonElement>('[role="tab"]')].find(
      (t) => t.textContent === 'Runs',
    )
    expect(runs?.disabled).toBe(false)
    expect(chips(view.container)).toContain('djinn')
  })
})

describe('Sidebar', () => {
  const render = (projects: Project[]) =>
    renderWithProjects(
      <Sidebar
        projects={projects}
        loading={false}
        onReorder={() => undefined}
        view="runs"
        currentProject=""
        theme="loom-dark"
        onToggleTheme={() => undefined}
        onShowHelp={() => undefined}
        onOpenPalette={() => undefined}
        onOpenSettings={() => undefined}
      />,
      { projects },
    )

  it('renders no djinn chip for a project without djinn', async () => {
    view = await render([plain])
    expect(chips(view.container)).not.toContain('djinn')
  })

  it('renders the djinn chip only on the djinn project', async () => {
    view = await render([plain, loom])
    const rows = [...view.container.querySelectorAll('a')]
    const row = (name: string) => rows.find((a) => a.getAttribute('href')?.startsWith(`/${name}/`))
    expect(chips(row('loom')!)).toContain('djinn')
    expect(chips(row('plain')!)).not.toContain('djinn')
  })

  it('keeps a contributed view only for the project that mounts it', async () => {
    view = await render([plain, loom])
    const hrefs = [...view.container.querySelectorAll('a')].map((a) => a.getAttribute('href'))
    expect(hrefs).toContain('/loom/runs')
    expect(hrefs).toContain('/plain/backlog')
    expect(hrefs).not.toContain('/plain/runs')
  })
})

/** The chip the labels picker draws for `needs-operator`. */
function operatorChip(): HTMLElement {
  const chip = [...document.querySelectorAll<HTMLElement>('[role="option"] .chip')].find(
    (c) => c.textContent?.trim() === 'needs-operator',
  )
  if (!chip) throw new Error('needs-operator is not in the labels picker')
  return chip
}

describe('QuickCreate', () => {
  const openLabels = async (project: Project) => {
    view = await renderWithProjects(
      <QuickCreate
        project={project.name}
        issues={[issue(`${project.name}-1`, ['needs-operator'])]}
        onClose={() => undefined}
        onCreated={() => undefined}
      />,
      { projects: [project], current: project.name },
    )
    const button = [...document.querySelectorAll('button')].find((b) => b.textContent?.startsWith('Labels'))
    act(() => button?.click())
  }

  it('draws needs-operator in the default label tone, with no djinn hint, without djinn', async () => {
    await openLabels(plain)
    const chip = operatorChip()
    expect(chip.dataset.tone).toBe('neutral')
    expect(chip.title).toBe('needs-operator')
    expect(chip.title).not.toMatch(/summoner|djinn|unattended/i)
  })

  it('draws it as a warning, with djinn’s hint, for a djinn project', async () => {
    await openLabels(loom)
    const chip = operatorChip()
    expect(chip.dataset.tone).toBe('warn')
    expect(chip.title).toMatch(/unattended/)
  })
})

describe('EditMenu', () => {
  const openLabels = async (project: Project) => {
    const target = issue(`${project.name}-1`, ['needs-operator'])
    view = await renderWithProjects(
      <EditMenu
        project={project.name}
        request={{ kind: 'labels', anchor: new DOMRect(0, 0, 10, 10), ids: [target.id] }}
        issues={[target]}
        onClose={() => undefined}
        onRequestIssueClose={() => undefined}
      />,
      { projects: [project], current: project.name },
    )
  }

  it('draws needs-operator in the default label tone, with no djinn hint, without djinn', async () => {
    await openLabels(plain)
    const chip = operatorChip()
    expect(chip.dataset.tone).toBe('neutral')
    expect(chip.title).toBe('needs-operator')
  })

  it('draws it as a warning for a djinn project', async () => {
    await openLabels(loom)
    expect(operatorChip().dataset.tone).toBe('warn')
  })

  it('does not seed needs-operator when no issue carries it and djinn is absent', async () => {
    const target = issue('plain-2')
    view = await renderWithProjects(
      <EditMenu
        project="plain"
        request={{ kind: 'labels', anchor: new DOMRect(0, 0, 10, 10), ids: [target.id] }}
        issues={[target]}
        onClose={() => undefined}
        onRequestIssueClose={() => undefined}
      />,
      { projects: [plain], current: 'plain' },
    )
    expect(document.body.textContent).not.toContain('needs-operator')
  })
})

describe('NowView', () => {
  const empty: NowResponse = { entries: [], skipped: [] }

  it('says neither "summon" nor "djinn" in its empty state', async () => {
    view = await renderWithProjects(<NowView projects={[plain]} />, {
      projects: [plain],
      routes: { '/api/now': () => ({ status: 200, body: empty }) },
    })
    await until(() => view!.container.textContent!.includes('No agents working'))
    const text = view.container.textContent ?? ''
    expect(text).toContain('No agents working right now')
    expect(text).not.toMatch(/summon/i)
    expect(text).not.toMatch(/djinn/i)
  })

  it('offers a project’s runs only when that project has djinn', async () => {
    const entry = (project: string) => ({
      project,
      issue: {
        id: `${project}-1`,
        title: 'work',
        status: 'in_progress' as const,
        priority: 2 as const,
        ready: false,
        assignee: 'worker-1',
        startedAt: null,
      },
      inStatusSince: null,
      stale: false,
    })
    view = await renderWithProjects(<NowView projects={[plain, loom]} />, {
      projects: [plain, loom],
      routes: { '/api/now': () => ({ status: 200, body: { entries: [entry('plain'), entry('loom')], skipped: [] } }) },
    })
    await until(() => view!.container.querySelectorAll('section').length === 2)
    const [first, second] = [...view.container.querySelectorAll('section')]
    const group = (name: string) => [first, second].find((s) => s?.querySelector('h2')?.textContent === name)!
    expect(group('plain').textContent).not.toMatch(/runs/)
    expect(group('loom').textContent).toMatch(/runs →/)
  })
})

describe('CheatSheet', () => {
  const rows = () => [...document.querySelectorAll('[role="dialog"] li')].map((li) => li.textContent ?? '')
  const legends = () => [...document.querySelectorAll('[role="dialog"] .legend')].map((l) => l.textContent)

  it('lists no stage key and no Runs section without djinn', async () => {
    view = await renderWithProjects(<CheatSheet project={plain} onClose={vi.fn()} />, { projects: [plain] })
    expect(rows().some((r) => /stage/i.test(r))).toBe(false)
    expect(rows().some((r) => /go to runs/i.test(r))).toBe(false)
    expect(legends()).not.toContain('Runs')
  })

  it('lists the stage key, g r and the Runs section with djinn', async () => {
    view = await renderWithProjects(<CheatSheet project={loom} onClose={vi.fn()} />, { projects: [loom] })
    expect(rows().some((r) => r.startsWith('q') && /stage/i.test(r))).toBe(true)
    expect(rows().some((r) => /go to runs/i.test(r))).toBe(true)
    expect(legends()).toContain('Runs')
  })
})
