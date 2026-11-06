import { afterEach, describe, expect, it } from 'vitest'
import { DJINN, testProject } from '@/test/projects'
import { renderWithProjects, settle, type Rendered } from '@/test/render'
import { RunLog } from './RunLog'

let view: Rendered | undefined
afterEach(() => {
  view?.unmount()
  view = undefined
})

const LOG = '/api/projects/loom/x/djinn/runs/run-1/log'

describe('RunLog', () => {
  it('asks for no capture of an external run and says why the pane is empty', async () => {
    view = await renderWithProjects(
      <RunLog project="loom" runId="run-1" source="external" incoming={[]} live={false} />,
      { projects: [testProject('loom', DJINN)] },
    )
    await settle()
    expect(view.requests.filter((r) => r.includes('/log'))).toEqual([])
    expect(document.body.textContent).toContain('No console capture for this run')
  })

  it('loads the scrollback of a run Loom started', async () => {
    view = await renderWithProjects(
      <RunLog project="loom" runId="run-1" source="loom" incoming={[]} live={false} />,
      {
        projects: [testProject('loom', DJINN)],
        routes: { [LOG]: () => ({ status: 200, body: { text: 'hello from the worker\n', offset: 22 } }) },
      },
    )
    await settle()
    expect(view.requests).toContain(LOG)
    expect(document.body.textContent).toContain('hello from the worker')
  })
})
