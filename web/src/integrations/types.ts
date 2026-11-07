import type { ComponentType, ReactNode } from 'react'
import type { Issue, IssueDetail, Project } from '@/api/types'
import type { LabelRule } from '@/lib/beads'
import type { ActionGroup } from '@/lib/palette'
import type { Shortcut, ShortcutHandlers } from '@/keyboard/registry'
import type { CacheAction } from '@/lib/sse-events'

/**
 * The frontend half of the integration seam.
 *
 * An integration is one object of optional contribution slots. Core never
 * imports an integration's modules; it reads what the registry
 * (`integrations/index.ts`) mounts for a project through `useIntegrations`.
 * Each slot item may name a capability; it mounts only when the project's
 * `integrations[id].capabilities` lists it. A slot item with no capability
 * mounts whenever the project has the integration at all. Absent means absent:
 * core never renders a disabled stand-in for a slot that did not mount.
 */
export interface Integration {
  /** The provider id, the key in `Project.integrations`. */
  id: string
  views?: ViewSlot[]
  projectBadge?: ProjectBadgeSlot
  issueActions?: IssueActionSlot[]
  bulkActions?: BulkActionSlot[]
  detailSections?: DetailSectionSlot[]
  statTiles?: StatTileSlot[]
  /** App-wide state, mounted above the router whatever project is open. */
  root?: RootSlot
  labels?: LabelSlot[]
  sse?: SseSlot
  paletteCommands?: PaletteCommandSlot[]
  shortcuts?: ShortcutSlot[]
  /** Per-view state and overlays shared by the backlog, board and initiatives. */
  workspace?: WorkspaceSlot
}

/** What a project must offer for one slot item to mount. */
export interface Gated {
  capability?: string
}

/** A top-bar tab with its routes under `/:project/`. */
export interface ViewSlot extends Gated {
  id: string
  label: string
  /** The first path segment after the project, e.g. `runs`. */
  path: string
  /** Route patterns relative to `/:project/`, e.g. `runs` and `runs/:runId`. */
  routes: string[]
  element: ComponentType<{ projects: Project[] }>
  /** Shown in the tab's tooltip, e.g. `g r`. */
  chord?: string
  /** The `?` sheet section for this view and the core shortcuts live in it. */
  surface?: { label: string; shortcuts: string[] }
}

export type BadgePlacement = 'topbar' | 'sidebar' | 'now'

export interface ProjectBadgeSlot extends Gated {
  component: ComponentType<{ project: Project; placement: BadgePlacement }>
}

/** A control in the issue sheet's action row. */
export interface IssueActionSlot extends Gated {
  id: string
  component: ComponentType<{ project: string; issue: IssueDetail }>
}

/** A control in the multi-select bar. */
export interface BulkActionSlot extends Gated {
  id: string
  component: ComponentType<{ project: string; ids: string[]; issues: Issue[] }>
}

/** A section of the issue sheet, below the dependency buckets. */
export interface DetailSectionSlot extends Gated {
  id: string
  component: ComponentType<{
    project: string
    issueId: string
    onOpenIssue: (id: string) => void
  }>
}

/** A fact in the backlog's stats strip. */
export interface StatTileSlot extends Gated {
  id: string
  component: ComponentType<{ project: string }>
}

export interface RootSlot extends Gated {
  /**
   * Rendered above the router in registry order whether or not it mounted, so
   * the tree and hook order stay stable. `enabled` is false until some project
   * mounts the slot; the component must then touch no state outside React.
   */
  component: ComponentType<{ children: ReactNode; enabled: boolean }>
  /**
   * Rendered inside the router on every route, below every root, with the
   * project the URL names ('' on a route outside any project, such as /now).
   * It mounts whatever project is open, so it sees every project switch even
   * where no other slot of this integration mounts.
   */
  route?: ComponentType<{ project: string }>
}

/** A label this integration gives meaning to; `operator` draws it as a warning. */
export interface LabelSlot extends Gated, LabelRule {}

/** What an integration's stream handler may do with a frame. */
export interface SseHost {
  /** Apply cache actions through the stream's interaction guard. */
  dispatch: (actions: CacheAction[]) => void
  toast: (toast: { tone: 'ok' | 'error' | 'info'; title: string; detail?: string }) => void
}

export interface SseSlot extends Gated {
  /** Event names this integration adds to the stream. */
  eventNames: string[]
  onEvent: (name: string, data: string, host: SseHost) => void
}

/** What a palette command can reach in the shell. */
export interface PaletteHost {
  currentProject: string
  currentView: string
  goView: (view: string) => void
}

export interface PaletteCommandSlot extends Gated {
  id: string
  group: ActionGroup
  label: string
  hint?: string
  keywords?: string
  run: (host: PaletteHost) => void
}

/** What a shell-level shortcut handler can reach. */
export interface ShortcutHost {
  currentProject: string
  goView: (view: string) => void
}

export interface ShortcutSlot extends Gated {
  shortcut: Shortcut
  /** Bound by the shell on every surface. Workspace shortcuts come from `workspace`. */
  run?: (host: ShortcutHost) => void
}

/** What the shared write surface tells an integration about the view. */
export interface WorkspaceHost {
  project: string
  /** The issues on screen. */
  issues: Issue[]
  /** The ids a "this one" verb acts on: the selection when focus is in it. */
  targets: () => string[]
  onOpenIssue: (id: string) => void
  /** The view may show floating panels; the board opts out. */
  floating: boolean
  readOnly: boolean
}

export interface WorkspaceExtension {
  shortcuts: ShortcutHandlers
  overlays: ReactNode
  /** An overlay is open, so stream refetches should wait. */
  busy: boolean
  /** The integration holds this id in a queue of its own (djinn: staged). */
  isStaged: (id: string) => boolean
}

export interface WorkspaceSlot extends Gated {
  /**
   * Called on every render of every workspace, in registry order, so hook
   * order is stable. `enabled` is false when the slot did not mount for the
   * project; the hook must then contribute nothing.
   */
  use: (host: WorkspaceHost, enabled: boolean) => WorkspaceExtension
}
