import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Project } from '@/api/types'
import { renderWithProjects, settle, type Rendered } from '@/test/render'
import { testProject } from '@/test/projects'
import { TopBar } from './TopBar'

vi.mock('./Live', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./Live')>()),
  ConnectionIndicator: () => null,
}))

let rendered: Rendered | undefined
afterEach(() => {
  rendered?.unmount()
  rendered = undefined
})

async function heading(project: Project) {
  rendered = await renderWithProjects(<TopBar project={project} />, {
    projects: [project],
    current: project.name,
    path: `/${encodeURIComponent(project.name)}/backlog`,
  })
  await settle()
  const h1 = rendered.container.querySelector('header h1')
  if (!(h1 instanceof HTMLElement)) throw new Error('no project name heading in the top bar')
  return h1
}

describe('TopBar project name', () => {
  const long = 'djinn-orchestration-workspace-long-name1'

  it('carries its full text in a title, so a truncated name can be read on hover', async () => {
    const h1 = await heading(testProject(long))
    expect(h1.textContent).toBe(long)
    expect(h1.title).toBe(long)
  })

  it('keeps room for the first ten characters and the ellipsis of a long name', async () => {
    const h1 = await heading(testProject(long))
    expect(h1.style.minWidth).toBe('12ch')
  })

  it('sets no floor on a name short enough to never need one', async () => {
    const h1 = await heading(testProject('djinn'))
    expect(h1.style.minWidth).toBe('')
  })

  it('lets the toolbar wrap instead of squeezing the name or clipping controls', async () => {
    const h1 = await heading(testProject('djinn'))
    expect(h1.closest('header')?.className).toContain('flex-wrap')
  })
})
