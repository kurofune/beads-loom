import { act, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/api/client'
import { qk } from '@/api/hooks'
import type { Issue, Project } from '@/api/types'
import { IntegrationRoots } from '@/integrations'
import { useStaged, useStageInto } from './staging'
import { useShortcuts } from '@/keyboard/useShortcuts'
import { LiveProvider } from '@/components/Live'
import { ToastProvider } from '@/components/Toast'
import { useWorkspace, type Workspace } from '@/components/workspace'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const issue = (id: string, status: Issue['status']): Issue =>
  ({
    id,
    title: `title ${id}`,
    type: 'task',
    status,
    priority: 2,
    assignee: 'nate',
    labels: [],
    estimate: null,
    created: '2026-08-01T00:00:00Z',
    updated: '2026-08-01T00:00:00Z',
    closed: null,
    due: null,
    deferUntil: null,
    ready: status === 'open',
    missingAcceptance: false,
    missingCloseReason: false,
    pinned: false,
    depCounts: { blockedBy: 0, blocks: 0 },
    parent: null,
    childStats: null,
  }) as Issue

let root: Root | undefined
let ws: Workspace
/** djinn's staging gesture for the harness's project, the one `q` and Stage use. */
let stage: (ids: string[]) => void

const DJINN = { djinn: { capabilities: ['runs', 'evidence', 'dispatch'] } }

/** The projects the workspace resolves capabilities from: two djinn, one plain. */
const PROJECTS: Project[] = (
  [
    ['loom', DJINN],
    ['djinn', DJINN],
    ['plain', {}],
  ] as const
).map(([name, integrations]) => ({
  name,
  db: name,
  workdir: `/work/${name}`,
  source: 'config',
  reachable: true,
  reason: '',
  writable: true,
  writeReason: '',
  integrations,
  health: null,
}))

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = undefined
  document.body.replaceChildren()
  sessionStorage.clear()
})

function Harness({
  issues,
  focusedId,
  project = 'loom',
}: {
  issues: Issue[]
  focusedId?: string
  project?: string
}) {
  ws = useWorkspace({
    project,
    issues,
    order: issues.map((i) => i.id),
    ...(focusedId ? { focusedId } : {}),
    onOpenIssue: () => {},
  })
  // Bound the way the views bind it, so a real key press reaches the handlers.
  useShortcuts(ws.shortcuts)
  stage = useStageInto(project, issues)
  const staged = useStaged(project)
  return (
    <>
      <div data-testid="staged">{staged.join(',')}</div>
      {ws.overlays}
    </>
  )
}

/**
 * Render `children` into one persistent provider tree. Calling again re-renders
 * the SAME providers with new children — exactly what the router does when it
 * swaps a view element — so the tray's survival across "navigation" is what is
 * actually under test.
 */
function renderStep(qc: QueryClient, children: ReactNode) {
  act(() => {
    root?.render(
      <QueryClientProvider client={qc}>
        <ToastProvider>
          <LiveProvider>
            <IntegrationRoots>
              <MemoryRouter>{children}</MemoryRouter>
            </IntegrationRoots>
          </LiveProvider>
        </ToastProvider>
      </QueryClientProvider>,
    )
  })
}

function newTree(): { qc: QueryClient; container: HTMLElement } {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  // Seeded, so resolving capabilities issues no request.
  qc.setQueryData(qk.projects, PROJECTS)
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  return { qc, container }
}

function renderWorkspace(issues: Issue[], focusedId?: string) {
  const { qc, container } = newTree()
  renderStep(qc, <Harness issues={issues} {...(focusedId ? { focusedId } : {})} />)
  return container
}

const stagedText = (container: HTMLElement) =>
  container.querySelector('[data-testid="staged"]')?.textContent ?? ''

describe('tray outlives the view that staged it (loom-p95)', () => {
  it('keeps the queue, in order, across unmounting and remounting the consuming view', () => {
    const issues = [issue('loom-a', 'open'), issue('loom-b', 'open')]
    const { qc, container } = newTree()

    // Board mounts, operator queues two beads.
    renderStep(qc, <Harness issues={issues} />)
    act(() => stage(['loom-b', 'loom-a']))
    expect(stagedText(container)).toBe('loom-b,loom-a')

    // Navigate away: the route swap unmounts the view entirely.
    renderStep(qc, null)
    // Navigate back: a fresh view instance mounts under the same provider.
    renderStep(qc, <Harness issues={issues} />)

    expect(stagedText(container)).toBe('loom-b,loom-a')
  })

  it('still empties the queue when the view remounts under a different project', () => {
    const issues = [issue('loom-a', 'open')]
    const { qc, container } = newTree()

    renderStep(qc, <Harness issues={issues} />)
    act(() => stage(['loom-a']))
    expect(stagedText(container)).toBe('loom-a')

    // Jump straight to another project's view: same provider, new id space.
    renderStep(qc, <Harness issues={[]} project="djinn" />)

    expect(stagedText(container)).toBe('')
  })

  it('drops a restored bead that was closed while the tab was shut', () => {
    // The store can hand back a queue staged in a previous sitting; a bead
    // closed since then must not ride into the summon preview.
    sessionStorage.setItem(
      'loom.tray',
      JSON.stringify({ staged: ['loom-a', 'loom-z'], dismissed: false, project: 'loom' }),
    )
    const { qc, container } = newTree()
    renderStep(qc, <Harness issues={[issue('loom-a', 'open'), issue('loom-z', 'closed')]} />)

    expect(stagedText(container)).toBe('loom-a')
  })

  it('survives a full reload: a fresh provider tree restores the queue from sessionStorage', () => {
    const issues = [issue('loom-a', 'open'), issue('loom-b', 'open')]
    const first = newTree()
    renderStep(first.qc, <Harness issues={issues} />)
    act(() => stage(['loom-a', 'loom-b']))
    expect(stagedText(first.container)).toBe('loom-a,loom-b')

    // Reload: the whole tree — provider included — is torn down and rebuilt.
    act(() => root?.unmount())
    first.container.remove()
    const second = newTree()
    renderStep(second.qc, <Harness issues={issues} />)

    expect(stagedText(second.container)).toBe('loom-a,loom-b')
  })
})

describe('workspace staging', () => {
  it('stages an open issue without issuing any write', () => {
    const patchSpy = vi.spyOn(api, 'patchIssue')
    const bulkSpy = vi.spyOn(api, 'bulkPatch')
    const fetchSpy = vi.spyOn(globalThis, 'fetch')

    const container = renderWorkspace([issue('loom-a', 'open')])
    act(() => stage(['loom-a']))

    expect(stagedText(container)).toBe('loom-a')
    // The acceptance criterion: staging never mutates bd status.
    expect(patchSpy).not.toHaveBeenCalled()
    expect(bulkSpy).not.toHaveBeenCalled()
    expect(fetchSpy).not.toHaveBeenCalled()
  })

  it('rejects a closed issue with a toast and does not queue it', () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
    const container = renderWorkspace([issue('loom-z', 'closed')])
    act(() => stage(['loom-z']))

    expect(stagedText(container)).toBe('')
    expect(document.body.textContent).toContain("can't be staged")
    expect(fetchSpy).not.toHaveBeenCalled()
  })

  it('stages the focused issue via the issue.stage keyboard handler', () => {
    const container = renderWorkspace(
      [issue('loom-a', 'open'), issue('loom-b', 'open')],
      'loom-b',
    )
    act(() => ws.shortcuts['issue.stage']?.(new KeyboardEvent('keydown', { key: 'q' })))

    expect(stagedText(container)).toBe('loom-b')
  })

  it('stages the non-closed part of a mixed batch and toasts for the rest', () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
    const container = renderWorkspace([
      issue('loom-a', 'open'),
      issue('loom-b', 'closed'),
      issue('loom-c', 'in_progress'),
    ])
    act(() => stage(['loom-a', 'loom-b', 'loom-c']))

    expect(stagedText(container)).toBe('loom-a,loom-c')
    expect(document.body.textContent).toContain("can't be staged")
    expect(fetchSpy).not.toHaveBeenCalled()
  })
})

describe('djinn absent: no staging anywhere in the workspace (loom-wvr)', () => {
  const issues = [issue('plain-a', 'open'), issue('plain-b', 'open')]

  function selectBoth(container: HTMLElement) {
    act(() => ws.toggle('plain-a'))
    act(() => ws.toggle('plain-b'))
    return container.querySelector('[role="toolbar"]')
  }

  it('renders the bulk bar with no Stage button', () => {
    const { qc, container } = newTree()
    renderStep(qc, <Harness issues={issues} project="plain" />)
    const bar = selectBoth(container)
    expect(bar).not.toBeNull()
    const labels = [...bar!.querySelectorAll('button')].map((b) => b.textContent ?? '')
    expect(labels.some((l) => l.startsWith('Status'))).toBe(true)
    expect(labels.some((l) => /stage/i.test(l))).toBe(false)
  })

  it('binds no issue.stage handler, so q does nothing', () => {
    const { qc, container } = newTree()
    renderStep(qc, <Harness issues={issues} project="plain" focusedId="plain-a" />)
    expect(ws.shortcuts['issue.stage']).toBeUndefined()
    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'q' }))
    })
    expect(stagedText(container)).toBe('')
  })

  it('never mounts the staged tray, even over a queue restored from another sitting', () => {
    sessionStorage.setItem(
      'loom.tray',
      JSON.stringify({ staged: ['plain-a'], dismissed: false, project: 'plain' }),
    )
    const { qc, container } = newTree()
    renderStep(qc, <Harness issues={issues} project="plain" />)
    expect(container.querySelector('[aria-label="Staged for djinn"]')).toBeNull()
    expect(container.textContent).not.toMatch(/summon/i)
  })

  it('stages the focused issue on a real q press for a djinn project', () => {
    const { qc, container } = newTree()
    renderStep(qc, <Harness issues={[issue('loom-a', 'open')]} focusedId="loom-a" />)
    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'q' }))
    })
    expect(stagedText(container)).toBe('loom-a')
  })

  it('renders the Stage button and the tray for a djinn project', () => {
    const djinnIssues = [issue('loom-a', 'open'), issue('loom-b', 'open')]
    const { qc, container } = newTree()
    renderStep(qc, <Harness issues={djinnIssues} />)
    act(() => ws.toggle('loom-a'))
    act(() => ws.toggle('loom-b'))
    const stageButton = [...container.querySelectorAll('[role="toolbar"] button')].find((b) =>
      b.textContent?.startsWith('Stage'),
    ) as HTMLButtonElement | undefined
    expect(stageButton).toBeDefined()
    act(() => stageButton!.click())
    expect(stagedText(container)).toBe('loom-a,loom-b')
    expect(container.querySelector('[aria-label="Staged for djinn"]')).not.toBeNull()
  })
})

describe('staging at the scale of a whole visible selection', () => {
  it('stages hundreds of ids in order and resets them on a project switch', () => {
    // The Scalability Assumption: the 6,030-issue fixture, staged a visible
    // page at a time through the moved reducer.
    const many = Array.from({ length: 600 }, (_, i) => issue(`loom-${i}`, i % 50 === 0 ? 'closed' : 'open'))
    const { qc, container } = newTree()
    renderStep(qc, <Harness issues={many} />)
    act(() => stage(many.map((i) => i.id)))
    const staged = stagedText(container).split(',')
    expect(staged).toHaveLength(588)
    expect(staged[0]).toBe('loom-1')
    expect(staged.at(-1)).toBe('loom-599')

    renderStep(qc, <Harness issues={[]} project="djinn" />)
    expect(stagedText(container)).toBe('')
  })
})
