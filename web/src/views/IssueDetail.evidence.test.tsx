import { afterEach, describe, expect, it } from 'vitest'
import type { Evidence, IssueDetail as Detail, Project } from '@/api/types'
import { DJINN, testProject } from '@/test/projects'
import { renderWithProjects, until, type Rendered } from '@/test/render'
import { IssueDetail } from './IssueDetail'

/**
 * The Evidence section is djinn's `detailSections` contribution, read from
 * /x/djinn/beads/{id}/evidence. One mocked fetch records every request, so
 * "no djinn" is checked as "no /x/djinn/ request", not just "no legend".
 */

const ID = 'loom-ev1'

const detail: Detail = {
  id: ID,
  title: 'Evidence fixture',
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
  acceptanceCriteria: 'one\ntwo',
  design: '',
  notes: '',
  closeReason: '',
  owner: '',
  deps: { blocks: [], blockedBy: [], children: [], discoveredFrom: [], parent: null },
  comments: [],
  events: [],
}

const evidence: Evidence = {
  bead: ID,
  title: 'Evidence fixture',
  criteria: [
    {
      text: 'one',
      status: 'pass',
      nonCode: false,
      implementation: ['web/src/x.ts:1'],
      evidence: { snapshots: [], observations: ['x.test.ts passes'] },
    },
  ],
  followUp: [],
}

let view: Rendered | undefined
afterEach(() => {
  view?.unmount()
  view = undefined
})

const evidenceUrl = (project: string) => `/api/projects/${project}/x/djinn/beads/${ID}/evidence`

async function open(project: Project, evidenceReply?: () => { status: number; body: unknown } | 'pending') {
  view = await renderWithProjects(
    <IssueDetail project={project.name} id={ID} onClose={() => undefined} onNavigate={() => undefined} />,
    {
      projects: [project],
      routes: {
        [`/api/projects/${project.name}/issues/${ID}`]: () => ({ status: 200, body: { ...detail } }),
        ...(evidenceReply ? { [evidenceUrl(project.name)]: evidenceReply } : {}),
      },
    },
  )
  await until(() => document.body.textContent?.includes('Evidence fixture') ?? false)
}

const legends = () => [...document.querySelectorAll('.legend')].map((l) => l.textContent)
const evidenceSection = () =>
  [...document.querySelectorAll('section')].find((s) => s.querySelector('.legend')?.textContent === 'Evidence')

describe('IssueDetail Evidence section', () => {
  it('renders the shared loading state while the request is pending', async () => {
    await open(testProject('loom', DJINN), () => 'pending')
    const section = evidenceSection()
    expect(section).toBeDefined()
    expect(section!.querySelector('[data-state="loading"][aria-busy="true"]')).not.toBeNull()
    expect(view!.requests).toContain(evidenceUrl('loom'))
  })

  it('renders the shared inline error state with the status code on a 5xx', async () => {
    await open(testProject('loom', DJINN), () => ({ status: 503, body: { error: 'state dir unreadable' } }))
    await until(() => evidenceSection()?.querySelector('[data-state="error"]') != null)
    const error = evidenceSection()!.querySelector('[data-state="error"]')!
    expect(error.getAttribute('role')).toBe('alert')
    expect(error.textContent).toContain('HTTP 503')
  })

  it('renders no Evidence legend for {evidence: null}, empty criteria, or a 404', async () => {
    const replies = [
      () => ({ status: 200, body: { evidence: null } }),
      () => ({ status: 200, body: { evidence: { ...evidence, criteria: [] } } }),
      () => ({ status: 404, body: { error: 'project loom is not a djinn project' } }),
    ]
    for (const reply of replies) {
      await open(testProject('loom', DJINN), reply)
      await until(() => view!.requests.includes(evidenceUrl('loom')) && !document.querySelector('[data-state="loading"]'))
      expect(legends()).not.toContain('Evidence')
      // The rest of the sheet is untouched.
      expect(legends()).toContain('Comments')
      view!.unmount()
      view = undefined
    }
  })

  it('renders the report when a worker has written evidence', async () => {
    await open(testProject('loom', DJINN), () => ({ status: 200, body: { evidence } }))
    await until(() => legends().includes('Evidence'))
    expect(legends()).toContain('Evidence')
  })

  it('issues no /x/djinn/ request at all for a project without djinn', async () => {
    await open(testProject('plain'))
    await until(() => legends().includes('Comments'))
    expect(view!.requests).toContain(`/api/projects/plain/issues/${ID}`)
    expect(view!.requests.filter((r) => r.includes('/x/djinn/'))).toEqual([])
    expect(legends()).not.toContain('Evidence')
  })
})
