import { describe, expect, it } from 'vitest'
import { testProject } from '@/test/projects'
import { integrationRoots, mountIntegrations, registeredIntegrations, routeProject, streamSlots } from '.'
import type { Integration, RootSlot, SseSlot } from './types'

const Noop = () => null

/** A registry of one integration whose slots each need a different capability. */
const fake: Integration = {
  id: 'fake',
  views: [{ id: 'v', label: 'V', path: 'v', routes: ['v'], element: Noop, capability: 'see' }],
  projectBadge: { component: Noop },
  detailSections: [
    { id: 'always', component: Noop },
    { id: 'gated', component: Noop, capability: 'inspect' },
  ],
  bulkActions: [{ id: 'b', component: Noop, capability: 'act' }],
  paletteCommands: [{ id: 'p', group: 'Actions', label: 'P', run: () => undefined, capability: 'act' }],
  workspace: { use: () => ({ shortcuts: {}, overlays: null, busy: false, isStaged: () => false }), capability: 'act' },
}

const Root: RootSlot['component'] = ({ children }) => children
const sse: SseSlot = { eventNames: ['fake-event'], onEvent: () => undefined }
/** The same integration with an app-wide root and stream slot that need `act`. */
const appWide: Integration = { ...fake, root: { component: Root, capability: 'act' }, sse: { ...sse, capability: 'act' } }

const ids = (items: { id: string }[]) => items.map((i) => i.id)

describe('mountIntegrations', () => {
  it('mounts nothing for a project without the integration, but keeps the workspace hook disabled', () => {
    const mounted = mountIntegrations(testProject('plain'), [fake])
    expect(mounted.ids).toEqual([])
    expect(mounted.views).toEqual([])
    expect(mounted.projectBadges).toEqual([])
    expect(mounted.detailSections).toEqual([])
    expect(mounted.bulkActions).toEqual([])
    expect(mounted.paletteCommands).toEqual([])
    // Hook order must not depend on the project, so the slot stays, disabled.
    expect(mounted.workspaces.map((w) => w.enabled)).toEqual([false])
  })

  it('drops each slot whose capability the project lacks and keeps ungated ones', () => {
    const mounted = mountIntegrations(testProject('p', { fake: { capabilities: ['see'] } }), [fake])
    expect(mounted.ids).toEqual(['fake'])
    expect(ids(mounted.views)).toEqual(['v'])
    expect(mounted.projectBadges).toHaveLength(1)
    expect(ids(mounted.detailSections)).toEqual(['always'])
    expect(mounted.bulkActions).toEqual([])
    expect(mounted.paletteCommands).toEqual([])
    expect(mounted.workspaces.map((w) => w.enabled)).toEqual([false])
  })

  it('mounts every slot when every capability is present', () => {
    const mounted = mountIntegrations(
      testProject('p', { fake: { capabilities: ['see', 'inspect', 'act'] } }),
      [fake],
    )
    expect(ids(mounted.detailSections)).toEqual(['always', 'gated'])
    expect(ids(mounted.bulkActions)).toEqual(['b'])
    expect(ids(mounted.paletteCommands)).toEqual(['p'])
    expect(mounted.workspaces.map((w) => w.enabled)).toEqual([true])
  })

  it('registers djinn, and only djinn, today', () => {
    expect(registeredIntegrations().map((i) => i.id)).toEqual(['djinn'])
  })
})

describe('app-wide slots: roots and stream', () => {
  const plain = testProject('plain')
  const seeOnly = testProject('see', { fake: { capabilities: ['see'] } })
  const acts = testProject('acts', { fake: { capabilities: ['act'] } })

  it('renders the root disabled and adds no stream slot while no project has the integration', () => {
    expect(integrationRoots([], [appWide])).toEqual([{ slot: appWide.root, enabled: false }])
    expect(integrationRoots([plain], [appWide])).toEqual([{ slot: appWide.root, enabled: false }])
    expect(streamSlots([plain], [appWide])).toEqual([])
  })

  it('keeps both off when the projects that have the integration lack the capability', () => {
    expect(integrationRoots([plain, seeOnly], [appWide])).toEqual([{ slot: appWide.root, enabled: false }])
    expect(streamSlots([plain, seeOnly], [appWide])).toEqual([])
  })

  it('mounts both once any one project has the capability', () => {
    expect(integrationRoots([plain, seeOnly, acts], [appWide])).toEqual([{ slot: appWide.root, enabled: true }])
    expect(streamSlots([plain, seeOnly, acts], [appWide])).toEqual([appWide.sse])
  })

  it('mounts an ungated root and stream for any project with the integration', () => {
    const ungated: Integration = { ...fake, root: { component: Root }, sse }
    expect(integrationRoots([seeOnly], [ungated])).toEqual([{ slot: ungated.root, enabled: true }])
    expect(streamSlots([seeOnly], [ungated])).toEqual([sse])
  })
})

describe('routeProject (loom-a7s)', () => {
  it('names the project of a route under one, decoded', () => {
    expect(routeProject('/alpha/graph/issue/shared-1')).toBe('alpha')
    expect(routeProject('/my%20project/backlog')).toBe('my project')
    expect(routeProject('/alpha/')).toBe('alpha')
  })

  it('names no project off a project route', () => {
    expect(routeProject('/')).toBe('')
    expect(routeProject('/now')).toBe('')
    expect(routeProject('/settings')).toBe('')
  })
})
