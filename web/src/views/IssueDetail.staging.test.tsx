import { act } from 'react'
import { Route, Routes, useNavigate, useParams, type NavigateFunction } from 'react-router-dom'
import { afterEach, describe, expect, it } from 'vitest'
import type { IssueDetail as Detail } from '@/api/types'
import { DJINN, testProject } from '@/test/projects'
import { renderWithProjects, until, type Rendered } from '@/test/render'
import { IssueDetail } from './IssueDetail'

/**
 * The graph mounts no workspace, so its issue sheet is the only djinn staging
 * surface on the route. Two projects share an issue id here: the sheet must
 * read only its own project's queue, and moving between projects must reset
 * the queue as a workspace would.
 */

const ID = 'shared-1'
const A = testProject('project-a', DJINN)
const B = testProject('project-b', DJINN)

const detail: Detail = {
  id: ID,
  title: 'Shared id',
  type: 'task',
  status: 'open',
  priority: 2,
  assignee: '',
  labels: [],
  estimate: null,
  created: '2026-10-01T00:00:00Z',
  updated: '2026-10-02T00:00:00Z',
  closed: null,
  due: null,
  deferUntil: null,
  ready: true,
  missingAcceptance: false,
  missingCloseReason: false,
  pinned: false,
  notReady: null,
  depCounts: { blockedBy: 0, blocks: 0 },
  parent: null,
  childStats: null,
  health: null,
  description: '',
  acceptanceCriteria: '',
  design: '',
  notes: '',
  closeReason: '',
  owner: '',
  deps: { blocks: [], blockedBy: [], children: [], discoveredFrom: [], parent: null },
  comments: [],
  events: [],
}

let navigate: NavigateFunction
let view: Rendered | undefined

afterEach(() => {
  view?.unmount()
  view = undefined
  sessionStorage.clear()
})

/** What GraphView renders for `/:project/graph/issue/:id`, minus the graph. */
function GraphSheet() {
  navigate = useNavigate()
  const { project = '', id = '' } = useParams()
  return <IssueDetail project={project} id={id} onClose={() => undefined} onNavigate={() => undefined} />
}

async function openGraph(path: string) {
  view = await renderWithProjects(
    <Routes>
      <Route path="/:project/graph/issue/:id" element={<GraphSheet />} />
    </Routes>,
    {
      projects: [A, B],
      path,
      routes: Object.fromEntries(
        [A, B].map((p) => [`/api/projects/${p.name}/issues/${ID}`, () => ({ status: 200, body: { ...detail } })]),
      ),
    },
  )
}

const text = () => view?.container.textContent ?? ''
const stageButton = () =>
  [...(view?.container.querySelectorAll('button') ?? [])].find((b) => b.textContent === 'Stage for djinn')
const stored = () => JSON.parse(sessionStorage.getItem('loom.tray') ?? 'null')

async function go(path: string) {
  await act(async () => {
    await navigate(path)
  })
  await until(() => text().includes('Shared id'))
}

describe('IssueDetail staging on the graph', () => {
  it('does not show another project’s queued id as queued', async () => {
    sessionStorage.setItem('loom.tray', JSON.stringify({ staged: [ID], dismissed: false, project: A.name }))

    await openGraph(`/${B.name}/graph/issue/${ID}`)
    await until(() => text().includes('Shared id'))

    expect(text()).not.toContain('queued for djinn')
    expect(stageButton()).toBeDefined()
    expect(stored()).toEqual({ staged: [], dismissed: false, project: B.name })
  })

  it('resets the queue when the graph moves to another project with the same issue id', async () => {
    sessionStorage.setItem('loom.tray', JSON.stringify({ staged: [ID], dismissed: false, project: A.name }))

    await openGraph(`/${A.name}/graph/issue/${ID}`)
    await until(() => text().includes('queued for djinn'))
    expect(stageButton()).toBeUndefined()

    await go(`/${B.name}/graph/issue/${ID}`)
    expect(text()).not.toContain('queued for djinn')
    expect(stored()).toEqual({ staged: [], dismissed: false, project: B.name })

    // Stage in B, then return to A: B's id must not read as A's.
    await act(async () => {
      stageButton()?.click()
    })
    expect(text()).toContain('queued for djinn')
    expect(stored()).toEqual({ staged: [ID], dismissed: false, project: B.name })

    await go(`/${A.name}/graph/issue/${ID}`)
    expect(text()).not.toContain('queued for djinn')
    expect(stageButton()).toBeDefined()
    expect(stored()).toEqual({ staged: [], dismissed: false, project: A.name })
  })
})
