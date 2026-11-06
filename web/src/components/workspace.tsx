import { Fragment, useCallback, useEffect, useMemo, useReducer, useState, type ReactNode } from 'react'
import type { Issue } from '@/api/types'
import { emptySelection, selectionReducer, type SelectionState } from '@/lib/selection'
import { useIntegrations, type WorkspaceExtension, type WorkspaceHost } from '@/integrations'
import type { ShortcutHandlers } from '@/keyboard/registry'
import { BulkBar } from './BulkBar'
import { CloseDialog } from './CloseDialog'
import { EditMenu } from './EditMenu'
import { rectOf, type MenuKind, type MenuRequest } from './Menu'
import { QuickCreate } from './QuickCreate'
import { useInteractionLock } from './Live'

/**
 * The shared write surface for the backlog, the board and initiatives.
 *
 * Selection, inline-edit menus, the close dialog and quick create behave
 * identically in every view — so they live here once rather than being
 * implemented twice and drifting. A view supplies what only it knows: the
 * issues on screen, their visual order, and which one has focus. The mounted
 * integrations add their own state, shortcuts and overlays through their
 * `workspace` slot (djinn: the summon queue) and their `bulkActions`.
 */
export interface WorkspaceOptions {
  project: string
  /** Issues currently rendered — the source of truth for menus and ranges. */
  issues: Issue[]
  /** Visual order, for shift-ranges and select-all. */
  order: string[]
  /** The row/card under the cursor, for shortcuts that act on "this one". */
  focusedId?: string
  /** Element the keyboard-opened menus should anchor to. */
  focusedEl?: () => Element | null
  onOpenIssue: (id: string) => void
  /**
   * Writes are impossible right now — the project's dolt server is unreachable,
   * so bd could run but Loom could not read the result back. Gated here rather
   * than in each caller so no write path can forget.
   */
  readOnly?: boolean
  /** Let integrations float panels over the view. The board opts out. */
  floating?: boolean
}

export interface Workspace {
  readOnly: boolean
  selection: SelectionState
  isSelected: (id: string) => boolean
  toggle: (id: string, shift?: boolean) => void
  clear: () => void
  /** True when the workspace has a modal or menu up — views suspend j/k. */
  isBusy: boolean
  openMenu: (kind: MenuKind, anchor: DOMRect, ids?: string[]) => void
  requestClose: (ids: string[]) => void
  openCreate: () => void
  /** A mounted integration holds this id in a queue of its own (djinn: staged). */
  isStaged: (id: string) => boolean
  /** Shortcut handlers to merge into the view's useShortcuts map. */
  shortcuts: ShortcutHandlers
  overlays: ReactNode
}

export function useWorkspace({
  project,
  issues,
  order,
  focusedId,
  focusedEl,
  onOpenIssue,
  readOnly = false,
  floating = true,
}: WorkspaceOptions): Workspace {
  const [selection, dispatch] = useReducer(selectionReducer, emptySelection)
  const [menu, setMenu] = useState<MenuRequest | null>(null)
  const [closing, setClosing] = useState<string[] | null>(null)
  const [creating, setCreating] = useState(false)
  const mounted = useIntegrations(project)

  // Switching projects starts a fresh selection: it means nothing in another
  // project's id space.
  useEffect(() => {
    dispatch({ type: 'clear' })
  }, [project])

  // Drop selected ids that have been filtered away, so the bulk bar can never
  // claim to be acting on rows nobody can see.
  useEffect(() => {
    dispatch({ type: 'retain', order })
  }, [order])

  const isSelected = useCallback((id: string) => selection.ids.has(id), [selection])

  const toggle = useCallback(
    (id: string, shift = false) => {
      dispatch(shift ? { type: 'range', id, order } : { type: 'toggle', id })
    },
    [order],
  )

  const clear = useCallback(() => dispatch({ type: 'clear' }), [])

  /** Menus act on the selection when the focused row is part of it, and on the
   *  focused row alone otherwise — the rule every list app uses. */
  const targetsFor = useCallback(
    (explicit?: string[]): string[] => {
      if (explicit?.length) return explicit
      const selected = [...selection.ids]
      if (focusedId && selected.includes(focusedId)) return selected
      if (focusedId) return [focusedId]
      return selected
    },
    [focusedId, selection.ids],
  )

  const openMenu = useCallback(
    (kind: MenuKind, anchor: DOMRect, ids?: string[]) => {
      if (readOnly) return
      const targets = targetsFor(ids)
      if (targets.length === 0) return
      setMenu({ kind, anchor, ids: targets })
    },
    [readOnly, targetsFor],
  )

  const requestClose = useCallback(
    (ids: string[]) => {
      if (readOnly) return
      const targets = targetsFor(ids)
      if (targets.length > 0) setClosing(targets)
    },
    [readOnly, targetsFor],
  )

  // Every registered integration's workspace hook runs on every render, in
  // registry order, so hook order is stable; `enabled` says whether it mounted.
  const targets = useCallback(() => targetsFor(), [targetsFor])
  const host: WorkspaceHost = {
    project,
    issues,
    targets,
    onOpenIssue,
    floating,
    readOnly,
  }
  const extensions: WorkspaceExtension[] = mounted.workspaces.map(({ slot, enabled }) => slot.use(host, enabled))
  const isStaged = useCallback(
    (id: string) => extensions.some((e) => e.isStaged(id)),
    extensions.map((e) => e.isStaged),
  )

  const menuShortcut = useCallback(
    (kind: MenuKind) => () => openMenu(kind, rectOf(focusedEl?.())),
    [focusedEl, openMenu],
  )

  const shortcuts: ShortcutHandlers = useMemo(
    () => ({
      'select.toggle': () => {
        if (focusedId) toggle(focusedId)
      },
      'select.all': () => dispatch({ type: 'all', order }),
      'edit.status': menuShortcut('status'),
      'edit.priority': menuShortcut('priority'),
      'edit.assignee': menuShortcut('assignee'),
      'edit.labels': menuShortcut('labels'),
      'issue.create': () => {
        if (!readOnly) setCreating(true)
      },
      ...Object.assign({}, ...extensions.map((e) => e.shortcuts)),
    }),
    [focusedId, menuShortcut, order, readOnly, toggle, ...extensions.map((e) => e.shortcuts)],
  )

  const selectedList = useMemo(() => [...selection.ids], [selection.ids])

  // While a menu or modal is open, SSE invalidations queue instead of
  // refetching under the user's hands. This is the loom-323 guard.
  const overlayOpen = Boolean(menu || closing || creating || extensions.some((e) => e.busy))
  useInteractionLock(overlayOpen)

  const overlays = (
    <>
      {menu && (
        <EditMenu
          project={project}
          request={menu}
          issues={issues}
          onClose={() => setMenu(null)}
          onRequestIssueClose={(ids) => setClosing(ids)}
        />
      )}

      {closing && (
        <CloseDialog
          project={project}
          ids={closing}
          onClose={() => setClosing(null)}
          onClosed={() => {
            setClosing(null)
            clear()
          }}
        />
      )}

      {creating && (
        <QuickCreate
          project={project}
          issues={issues}
          onClose={() => setCreating(false)}
          onCreated={(id) => {
            setCreating(false)
            onOpenIssue(id)
          }}
        />
      )}

      {selectedList.length > 1 && !readOnly && (
        <BulkBar
          project={project}
          count={selectedList.length}
          onOpenMenu={(kind, anchor) => openMenu(kind, anchor, selectedList)}
          actions={mounted.bulkActions.map((action) => (
            <action.component key={action.id} project={project} ids={selectedList} issues={issues} />
          ))}
          onClose={() => requestClose(selectedList)}
          onClear={clear}
        />
      )}

      {extensions.map((e, i) => (
        <Fragment key={i}>{e.overlays}</Fragment>
      ))}
    </>
  )

  return {
    readOnly,
    selection,
    isSelected,
    toggle,
    clear,
    isBusy: overlayOpen,
    openMenu,
    requestClose,
    openCreate: () => {
      if (!readOnly) setCreating(true)
    },
    isStaged,
    shortcuts,
    overlays,
  }
}
