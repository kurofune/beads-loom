import { afterEach, describe, expect, it } from 'vitest'
import { DJINN, DJINN_NO_DISPATCH, NO_DISPATCH_REASON, testProject } from '@/test/projects'
import { renderWithProjects, settle, type Rendered } from '@/test/render'
import { SummonFlow } from './SummonFlow'

let view: Rendered | undefined
afterEach(() => {
  view?.unmount()
  view = undefined
})

const launch = () =>
  [...document.querySelectorAll<HTMLButtonElement>('button')].find((b) => b.textContent === 'Continue…')

describe('SummonFlow', () => {
  it('disables the launch button with reasons.dispatch as its title when djinn cannot dispatch', async () => {
    view = await renderWithProjects(
      <SummonFlow project="loom" staged={['loom-a']} issues={[]} onClose={() => undefined} onDispatched={() => undefined} />,
      { projects: [testProject('loom', DJINN_NO_DISPATCH)] },
    )
    await settle()
    const button = launch()
    expect(button?.disabled).toBe(true)
    expect(button?.title).toBe(NO_DISPATCH_REASON)
    expect(document.body.textContent).toContain(NO_DISPATCH_REASON)
    // Even the dry run needs summoner, so none is attempted.
    expect(view.requests.filter((r) => r.includes('/summon'))).toEqual([])
  })

  it('runs the dry-run preview when djinn can dispatch', async () => {
    view = await renderWithProjects(
      <SummonFlow project="loom" staged={['loom-a']} issues={[]} onClose={() => undefined} onDispatched={() => undefined} />,
      {
        projects: [testProject('loom', DJINN)],
        routes: {
          '/api/projects/loom/x/djinn/summon/preview': () => ({
            status: 200,
            body: { ordered: ['loom-a'], warnings: [], needsOperator: [], preflight: false },
          }),
        },
      },
    )
    await settle(20)
    expect(view.requests).toContain('/api/projects/loom/x/djinn/summon/preview')
    expect(launch()?.title).not.toBe(NO_DISPATCH_REASON)
  })
})
