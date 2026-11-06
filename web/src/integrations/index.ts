import { createElement, useMemo, type ReactNode } from 'react'
import { useProjects } from '@/api/hooks'
import type { IntegrationState, Project } from '@/api/types'
import type { Shortcut } from '@/keyboard/registry'
import type { PaletteAction } from '@/lib/palette'
import { registeredIntegrations } from './registry'
import type {
  BulkActionSlot,
  DetailSectionSlot,
  Gated,
  Integration,
  IssueActionSlot,
  LabelSlot,
  PaletteCommandSlot,
  PaletteHost,
  ProjectBadgeSlot,
  RootSlot,
  ShortcutSlot,
  SseSlot,
  StatTileSlot,
  ViewSlot,
  WorkspaceSlot,
} from './types'

export { registeredIntegrations }
export type * from './types'

/** Every slot item one project mounts, flattened across integrations. */
export interface Mounted {
  /** Ids of the integrations the project has. */
  ids: string[]
  views: ViewSlot[]
  projectBadges: ProjectBadgeSlot[]
  issueActions: IssueActionSlot[]
  bulkActions: BulkActionSlot[]
  detailSections: DetailSectionSlot[]
  statTiles: StatTileSlot[]
  labels: LabelSlot[]
  paletteCommands: PaletteCommandSlot[]
  shortcuts: ShortcutSlot[]
  /** One flag per registered integration's workspace slot, in registry order. */
  workspaces: { slot: WorkspaceSlot; enabled: boolean }[]
}

/** The project's state for one integration, or undefined when it has none. */
export function integrationState(
  project: Pick<Project, 'integrations'> | undefined,
  id: string,
): IntegrationState | undefined {
  return project?.integrations?.[id]
}

/**
 * Whether one project mounts one slot item of one integration: the project has
 * the integration and, if the item names a capability, lists that capability.
 */
function mountsFor(
  project: Pick<Project, 'integrations'> | undefined,
  integration: Integration,
  item: Gated,
): boolean {
  const state = integrationState(project, integration.id)
  return state !== undefined && (item.capability === undefined || state.capabilities.includes(item.capability))
}

/** Whether at least one project mounts the item: the test for app-wide slots. */
function mountsForAny(
  projects: readonly Pick<Project, 'integrations'>[],
  integration: Integration,
  item: Gated,
): boolean {
  return projects.some((p) => mountsFor(p, integration, item))
}

/**
 * What one project mounts. A slot item mounts when the project has the
 * integration and, if the item names a capability, lists that capability.
 */
export function mountIntegrations(
  project: Pick<Project, 'integrations'> | undefined,
  registry: readonly Integration[] = registeredIntegrations(),
): Mounted {
  const mounted: Mounted = {
    ids: [],
    views: [],
    projectBadges: [],
    issueActions: [],
    bulkActions: [],
    detailSections: [],
    statTiles: [],
    labels: [],
    paletteCommands: [],
    shortcuts: [],
    workspaces: [],
  }
  for (const integration of registry) {
    const state = integrationState(project, integration.id)
    const allows = (item: Gated) => mountsFor(project, integration, item)
    const keep = <T extends Gated>(items: T[] | undefined): T[] => (items ?? []).filter(allows)

    if (integration.workspace) {
      mounted.workspaces.push({ slot: integration.workspace, enabled: allows(integration.workspace) })
    }
    if (!state) continue
    mounted.ids.push(integration.id)
    mounted.views.push(...keep(integration.views))
    if (integration.projectBadge && allows(integration.projectBadge)) {
      mounted.projectBadges.push(integration.projectBadge)
    }
    mounted.issueActions.push(...keep(integration.issueActions))
    mounted.bulkActions.push(...keep(integration.bulkActions))
    mounted.detailSections.push(...keep(integration.detailSections))
    mounted.statTiles.push(...keep(integration.statTiles))
    mounted.labels.push(...keep(integration.labels))
    mounted.paletteCommands.push(...keep(integration.paletteCommands))
    mounted.shortcuts.push(...keep(integration.shortcuts))
  }
  return mounted
}

/**
 * What one project mounts, by project or by name. A name resolves through the
 * projects query, so a component that knows only the name reads the same
 * capabilities the sidebar does.
 */
export function useIntegrations(project: Project | string | undefined): Mounted {
  const projects = useProjects()
  const resolved =
    typeof project === 'string' ? projects.data?.find((p) => p.name === project) : project
  const integrations = resolved?.integrations
  return useMemo(() => mountIntegrations(integrations ? { integrations } : undefined), [integrations])
}

/** Per project name, the paths of the views it mounts. */
export function useViewPaths(projects: readonly Project[]): Map<string, string[]> {
  return useMemo(
    () => new Map(projects.map((p) => [p.name, mountIntegrations(p).views.map((v) => v.path)])),
    [projects],
  )
}

/** Mounted palette commands as palette actions bound to the shell. */
export function commandActions(slots: readonly PaletteCommandSlot[], host: PaletteHost): PaletteAction[] {
  return slots.map((slot) => ({
    id: slot.id,
    group: slot.group,
    label: slot.label,
    ...(slot.hint ? { hint: slot.hint } : {}),
    ...(slot.keywords ? { keywords: slot.keywords } : {}),
    run: () => slot.run(host),
  }))
}

/** One integration's capabilities and reasons for a project, by name. */
export function useIntegrationState(project: string, id: string): IntegrationState | undefined {
  const projects = useProjects()
  return integrationState(
    projects.data?.find((p) => p.name === project),
    id,
  )
}

/**
 * Every registered integration's root, outermost first, each with whether it
 * mounted. A root sits above the router and outlives navigation, so it mounts
 * when at least one project mounts it. Every root renders whether or not it
 * mounted: dropping one from the tree would remount everything below it.
 */
export function integrationRoots(
  projects: readonly Pick<Project, 'integrations'>[],
  registry: readonly Integration[] = registeredIntegrations(),
): { slot: RootSlot; enabled: boolean }[] {
  return registry.flatMap((i) => (i.root ? [{ slot: i.root, enabled: mountsForAny(projects, i, i.root) }] : []))
}

/** Every registered integration's root around `children`, outermost first. */
export function IntegrationRoots({ children }: { children: ReactNode }): ReactNode {
  const projects = useProjects()
  const roots = useMemo(() => integrationRoots(projects.data ?? []), [projects.data])
  return roots.reduceRight<ReactNode>(
    (inner, { slot, enabled }) => createElement(slot.component, { enabled, children: inner }),
    children,
  )
}

/**
 * The stream slots at least one project mounts. The stream is app-wide, so an
 * integration no project mounts adds no listener.
 */
export function streamSlots(
  projects: readonly Pick<Project, 'integrations'>[],
  registry: readonly Integration[] = registeredIntegrations(),
): SseSlot[] {
  return registry.flatMap((i) => (i.sse && mountsForAny(projects, i, i.sse) ? [i.sse] : []))
}

/**
 * Every shortcut any registered integration can bind. The key matcher needs
 * the full list; a binding is live only while some handler is registered for
 * it, and handlers come from mounted slots alone.
 */
export function registeredShortcuts(registry: readonly Integration[] = registeredIntegrations()): Shortcut[] {
  return registry.flatMap((i) => (i.shortcuts ?? []).map((s) => s.shortcut))
}
