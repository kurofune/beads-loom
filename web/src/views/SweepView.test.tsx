import { act } from 'react'
import { flushSync } from 'react-dom'
import { createRoot, type Root } from 'react-dom/client'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api } from '@/api/client'
import type { Project, SweepFinding, SweepReport } from '@/api/types'
import { ToastProvider } from '@/components/Toast'
import { formatSuggestion, SweepView } from './SweepView'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const live = vi.hoisted(() => ({ interactionLock: vi.fn() }))

vi.mock('@/components/Live', () => ({
  useInteractionLock: (active: boolean) => live.interactionLock(active),
}))
vi.mock('@/components/DoltSync', () => ({ DoltSync: () => null }))

const project: Project = {
  name: 'loom',
  db: 'loom',
  workdir: '/work/loom',
  source: 'config',
  reachable: true,
  reason: '',
  writable: true,
  writeReason: '',
  integrations: { djinn: { capabilities: ['runs', 'evidence', 'dispatch'] } },
  health: { ready: 1, inProgress: 0, blocked: 0, open: 1, total: 1 },
}

const emptyReport: SweepReport = {
  sweptAt: '2026-08-04T12:57:00Z',
  branch: 'main',
  window: 500,
  commitsScanned: 500,
  findings: [],
  partial: [],
  counts: {},
}

function finding(index: number, kind: string): SweepFinding {
  const applicable = kind === 'dangling-in-progress' || kind === 'untracked-commit'
  return {
    fingerprint: `scale-${index}`,
    materialDigest: `digest-${index}`,
    kind,
    severity: kind === 'dangling-in-progress' ? 'medium' : 'low',
    title: `Scale finding ${String(index + 1).padStart(3, '0')}`,
    issueIds: kind === 'untracked-commit' ? [] : [`scale-${index}`],
    commits: kind === 'untracked-commit' ? [`sha-${index}`] : [],
    evidence: [`Evidence line ${index}`],
    apply:
      kind === 'untracked-commit'
        ? { title: `Scale finding ${index}`, type: 'chore', priority: 3, sha: `sha-${index}` }
        : { issueId: `scale-${index}`, status: 'open' },
    suggested: {
      steps: [['bd', applicable ? 'update' : 'close', `scale-${index}`]],
      applicable,
      rationale: applicable ? 'Mechanical fix.' : 'Human context required.',
    },
    dismissed: false,
    dismissedAt: null,
  }
}

let root: Root | undefined

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = undefined
  document.body.replaceChildren()
  live.interactionLock.mockClear()
  vi.restoreAllMocks()
})

async function renderSweep() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  await act(async () => {
    root?.render(
      <QueryClientProvider client={client}>
        <ToastProvider>
          <MemoryRouter initialEntries={['/loom/sweep']}>
            <Routes>
              <Route path="/:project/sweep" element={<SweepView projects={[project]} />} />
            </Routes>
          </MemoryRouter>
        </ToastProvider>
      </QueryClientProvider>,
    )
  })
  return container
}

function button(container: Element, label: string): HTMLButtonElement {
  const found = [...container.querySelectorAll('button')].find((candidate) =>
    candidate.textContent?.includes(label),
  )
  if (!(found instanceof HTMLButtonElement)) throw new Error(`button ${label} not found`)
  return found
}

describe('SweepView', () => {
  it('renders never-swept, in-flight, all-clear, and verbatim-error states (AC4)', async () => {
    let resolveSweep: ((report: SweepReport) => void) | undefined
    vi.spyOn(api, 'sweep').mockImplementation(
      () => new Promise<SweepReport>((resolve) => { resolveSweep = resolve }),
    )
    let container = await renderSweep()

    expect(container.textContent).toContain('Check whether git and the tracker still agree')
    expect(container.textContent).toContain('It does not write anything')
    expect(button(container, 'Sweep project').disabled).toBe(false)

    await act(async () => {
      button(container, 'Sweep project').click()
      await vi.waitFor(() => {
        flushSync(() => {})
        expect(container.querySelector('[aria-label="Sweep in progress"]')).not.toBeNull()
      })
    })
    expect(container.querySelector('[aria-label="Sweep in progress"]')).not.toBeNull()
    expect(container.textContent).toContain('Sweeping git and tracker state')

    await act(async () => {
      resolveSweep?.(emptyReport)
      await vi.waitFor(() => {
        flushSync(() => {})
        expect(container.textContent).toContain('Git and the tracker are reconciled')
      })
    })
    expect(container.textContent).toContain('Git and the tracker are reconciled')
    expect(container.querySelector('time')?.getAttribute('datetime')).toBe(emptyReport.sweptAt)
    expect(container.querySelector('time')?.textContent).toBe(emptyReport.sweptAt)

    act(() => root?.unmount())
    root = undefined
    document.body.replaceChildren()
    const stderr = 'fatal: not a git repository\nbd kv list: dolt server unavailable\n'
    vi.mocked(api.sweep).mockRejectedValue(new ApiError('sweep failed', 400, { stderr }))
    container = await renderSweep()

    await act(async () => {
      button(container, 'Sweep project').click()
      await vi.waitFor(() => {
        flushSync(() => {})
        expect(container.textContent).toContain('Git or bd could not complete the sweep')
      })
    })

    expect(container.textContent).toContain('Git or bd could not complete the sweep')
    expect(container.querySelector('pre')?.textContent).toBe(stderr)
  })

  it('renders 700 findings with every group header and no truncation (AC8)', async () => {
    const kinds = [
      'dangling-in-progress',
      'untracked-commit',
      'empty-ac-close',
      'empty-reason-close',
    ]
    const findings = Array.from({ length: 700 }, (_, index) =>
      finding(index, kinds[index % kinds.length]!),
    )
    vi.spyOn(api, 'sweep').mockResolvedValue({
      ...emptyReport,
      findings,
      counts: Object.fromEntries(kinds.map((kind) => [kind, 175])),
    })
    const container = await renderSweep()

    await act(async () => {
      button(container, 'Sweep project').click()
      await vi.waitFor(() => {
        flushSync(() => {})
        expect(container.querySelectorAll('[data-finding]')).toHaveLength(700)
      })
    })

    expect(container.querySelectorAll('[data-finding]')).toHaveLength(700)
    expect(container.querySelector('#sweep-dangling-in-progress')?.textContent).toBe('Dangling in progress')
    expect(container.querySelector('#sweep-untracked-commit')?.textContent).toBe('Untracked commits')
    expect(container.querySelector('#sweep-empty-ac-close')?.textContent).toBe('Closed without acceptance')
    expect(container.querySelector('#sweep-empty-reason-close')?.textContent).toBe('Closed without a reason')
    expect(container.querySelector('[data-finding="scale-699"]')?.textContent).toContain('Scale finding 700')
  })

  it('shows exact argv before action, locks confirmation, and preserves 422 stderr', async () => {
    const applicable: SweepFinding = {
      ...finding(0, 'untracked-commit'),
      suggested: {
        steps: [
          ['bd', 'create', '--title', 'A title with spaces', '--type', 'chore'],
          ['bd', 'close', '{{created}}', '--reason', 'shipped in abc123'],
        ],
        applicable: true,
        rationale: 'Create and close a retroactive bead.',
      },
    }
    const template = finding(1, 'empty-ac-close')
    const stderr = 'guarded close: dependent loom-child remains open\nrun bd close --force only after review\n'
    vi.spyOn(api, 'sweep').mockResolvedValue({
      ...emptyReport,
      findings: [applicable, template],
      partial: [{ detector: 'untracked-commit', reason: 'fatal: bad revision main\n' }],
      counts: { 'untracked-commit': 1, 'empty-ac-close': 1 },
    })
    vi.spyOn(api, 'applyFinding').mockRejectedValue(
      new ApiError('bd apply failed', 422, { stderr }),
    )
    const container = await renderSweep()

    await act(async () => {
      button(container, 'Sweep project').click()
      await vi.waitFor(() => {
        flushSync(() => {})
        expect(container.querySelector('[aria-label="Partial sweep results"]')).not.toBeNull()
      })
    })

    const partial = container.querySelector('[aria-label="Partial sweep results"]')
    const applicableRow = container.querySelector(`[data-finding="${applicable.fingerprint}"]`)
    const templateRow = container.querySelector(`[data-finding="${template.fingerprint}"]`)
    if (!(partial instanceof HTMLElement) || !(applicableRow instanceof HTMLElement) || !(templateRow instanceof HTMLElement)) {
      throw new Error('expected sweep rows and partial strip')
    }
    expect(partial.textContent).toContain('fatal: bad revision main\n')
    expect(partial.compareDocumentPosition(applicableRow) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(applicableRow.querySelector('pre')?.textContent).toBe(formatSuggestion(applicable))
    expect([...templateRow.querySelectorAll('button')].map((candidate) => candidate.textContent)).toEqual([
      'Copy template',
      'Dismiss',
    ])

    const applyButton = [...applicableRow.querySelectorAll('button')].find(
      (candidate) => candidate.textContent === 'Apply',
    )
    if (!(applyButton instanceof HTMLButtonElement)) throw new Error('Apply button not found')
    await act(async () => applyButton.click())

    expect(live.interactionLock).toHaveBeenLastCalledWith(true)
    const dialog = container.querySelector('[role="dialog"]')
    if (!(dialog instanceof HTMLElement)) throw new Error('Apply dialog not found')
    expect(dialog.querySelector('pre')?.textContent).toBe(formatSuggestion(applicable))

    await act(async () => {
      button(dialog, 'Run this argv').click()
      await vi.waitFor(() => {
        flushSync(() => {})
        expect([...dialog.querySelectorAll('pre')].at(-1)?.textContent).toBe(stderr)
      })
    })

    expect(api.applyFinding).toHaveBeenCalledWith('loom', applicable.fingerprint, {
      materialDigest: applicable.materialDigest,
      window: emptyReport.window,
      branch: emptyReport.branch,
    })
    expect([...dialog.querySelectorAll('pre')].at(-1)?.textContent).toBe(stderr)
  })
})
