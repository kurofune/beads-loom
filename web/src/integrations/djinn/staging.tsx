import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useReducer,
  useState,
  type Dispatch,
  type ReactNode,
} from 'react'
import { useNavigate } from 'react-router-dom'
import type { Issue, IssueDetail } from '@/api/types'
import { useToast } from '@/components/Toast'
import type { WorkspaceExtension, WorkspaceHost } from '../types'
import { StagedTray } from './StagedTray'
import { SummonFlow } from './SummonFlow'
import {
  allStaged,
  emptyTray,
  partitionStageable,
  restoreTray,
  trayReducer,
  trayVisible,
  type TrayAction,
  type TrayState,
} from './tray'

/**
 * The summon queue's home. It is djinn's `root` slot, so it sits ABOVE the
 * router: React Router unmounts a view's element on navigation, and a queue
 * that dies on a tab switch betrays the deliberate build-up `q` exists for
 * (loom-p95). Backlog, board and initiatives read this one queue.
 */
const TrayContext = createContext<{
  tray: TrayState
  trayDispatch: Dispatch<TrayAction>
} | null>(null)

/**
 * One key, with the project embedded in the state, rather than a key per
 * project: a project switch must overwrite the stored queue with the reset,
 * exactly as the reducer's 'project' rule demands. Per-project keys would
 * resurrect project A's queue after a round trip through project B.
 */
const TRAY_KEY = 'loom.tray'

function readStoredTray(): TrayState {
  try {
    return restoreTray(sessionStorage.getItem(TRAY_KEY))
  } catch {
    // Safari in private mode throws on storage access.
    return emptyTray
  }
}

/**
 * Mounted above the router whether or not djinn mounted, so the queue's
 * lifetime never depends on the projects query. Storage is read the first
 * render djinn is enabled, during render so a workspace's effects see the
 * restored queue, and written only while djinn is enabled.
 */
export function TrayProvider({ children, enabled }: { children: ReactNode; enabled: boolean }) {
  const [tray, trayDispatch] = useReducer(trayReducer, enabled, (on) => (on ? readStoredTray() : emptyTray))
  const [restored, setRestored] = useState(enabled)
  if (enabled && !restored) {
    setRestored(true)
    trayDispatch({ type: 'restore', stored: readStoredTray() })
  }
  useEffect(() => {
    if (!enabled) return
    try {
      sessionStorage.setItem(TRAY_KEY, JSON.stringify(tray))
    } catch {
      // Private mode: reload survival is a nicety, not a requirement.
    }
  }, [enabled, tray])
  const value = useMemo(() => ({ tray, trayDispatch }), [tray])
  return <TrayContext.Provider value={value}>{children}</TrayContext.Provider>
}

function useTray() {
  const ctx = useContext(TrayContext)
  if (!ctx) throw new Error('djinn staging must be used inside <TrayProvider>')
  return ctx
}

/**
 * Stage ids into one project's queue. Closed issues are refused with a toast:
 * a closed bead in the queue is a summon that can never happen. Staging is
 * client-local state; it never writes to bd.
 */
export function useStageInto(project: string, issues: readonly Pick<Issue, 'id' | 'status'>[]) {
  const toast = useToast()
  const { trayDispatch } = useTray()
  return useCallback(
    (ids: string[]) => {
      const { stageable, closed } = partitionStageable(ids, (id) => issues.find((i) => i.id === id)?.status)
      if (closed.length > 0) {
        toast.push({
          tone: 'info',
          title:
            closed.length === 1
              ? `${closed[0]} is closed and can't be staged for djinn`
              : `${closed.length} closed issues can't be staged for djinn`,
          ...(closed.length > 1 ? { detail: closed.join(', ') } : {}),
        })
      }
      if (stageable.length > 0) trayDispatch({ type: 'stage', project, ids: stageable })
    },
    [issues, project, toast, trayDispatch],
  )
}

/**
 * The tray as `project` sees it. Switching projects starts a fresh tray: ids
 * mean nothing in another project's id space. Every surface that shows the
 * queue binds it here, so a route with no workspace (the graph's issue sheet)
 * resets it as a workspace does, and the render before that reset lands shows
 * the other project's queue as empty rather than as this project's.
 */
function useProjectTray(project: string): { tray: TrayState; trayDispatch: Dispatch<TrayAction> } {
  const { tray, trayDispatch } = useTray()
  useEffect(() => {
    trayDispatch({ type: 'project', project })
  }, [project, trayDispatch])
  const scoped = useMemo(
    () => (tray.project === project ? tray : { ...emptyTray, project }),
    [project, tray],
  )
  return { tray: scoped, trayDispatch }
}

/** One project's queue, read-only, for components that only show it. */
export function useStaged(project: string): readonly string[] {
  return useProjectTray(project).tray.staged
}

/* ---------------------------------------------------------------------------
 * A summon asked for from outside a workspace — the command palette.
 * ------------------------------------------------------------------------- */

let summonPending = false
const summonListeners = new Set<() => void>()

/** Open the summon flow in the next workspace that is (or becomes) mounted. */
export function requestSummon(): void {
  summonPending = true
  for (const fn of summonListeners) fn()
}

function takeSummonRequest(): boolean {
  const pending = summonPending
  summonPending = false
  return pending
}

/* ---------------------------------------------------------------------------
 * The workspace slot.
 * ------------------------------------------------------------------------- */

const NOTHING: WorkspaceExtension = {
  shortcuts: {},
  overlays: null,
  busy: false,
  isStaged: () => false,
}

export function useDjinnWorkspace(host: WorkspaceHost, enabled: boolean): WorkspaceExtension {
  const { project, issues, targets, onOpenIssue, floating } = host
  const navigate = useNavigate()
  // Bound with or without djinn, so a queue never survives a trip through a
  // project that cannot show it.
  const { tray, trayDispatch } = useProjectTray(project)
  const stage = useStageInto(project, issues)
  const [summoning, setSummoning] = useState(false)

  // stage() refuses closed beads at the door; this catches the ids that arrive
  // around that gate — a queue restored from a previous sitting whose beads
  // have since been closed, or a bead closed after it was staged. Ids whose
  // status this view cannot resolve are left alone, same as partitionStageable.
  useEffect(() => {
    for (const id of tray.staged) {
      if (issues.find((i) => i.id === id)?.status === 'closed') trayDispatch({ type: 'unstage', id })
    }
  }, [issues, tray.staged, trayDispatch])

  useEffect(() => {
    if (!enabled) return
    const take = () => {
      if (takeSummonRequest()) setSummoning(true)
    }
    take()
    summonListeners.add(take)
    return () => {
      summonListeners.delete(take)
    }
  }, [enabled])

  const unstage = useCallback((id: string) => trayDispatch({ type: 'unstage', id }), [trayDispatch])

  const shortcuts = useMemo(
    () => ({
      // `q` is a toggle, so the key that queued a bead is also the key that
      // takes it back — the alternative left an accidental press with no
      // keyboard undo at all.
      'issue.stage': () => {
        const ids = targets()
        if (ids.length === 0) return
        if (allStaged(tray.staged, ids)) ids.forEach(unstage)
        else stage(ids)
      },
    }),
    [stage, targets, tray.staged, unstage],
  )

  const isStaged = useCallback((id: string) => tray.staged.includes(id), [tray.staged])

  if (!enabled) return NOTHING

  const overlays = (
    <>
      {/* The board opts out of the floating tray; the summon flow is unaffected. */}
      {floating && trayVisible(tray) && (
        <StagedTray
          state={tray}
          onDismiss={() => trayDispatch({ type: 'dismiss' })}
          onClear={() => trayDispatch({ type: 'clear' })}
          onUnstage={unstage}
          onOpenIssue={onOpenIssue}
          onSummon={() => setSummoning(true)}
        />
      )}
      {summoning && (
        <SummonFlow
          project={project}
          staged={[...tray.staged]}
          issues={issues}
          onClose={() => setSummoning(false)}
          onDispatched={(runId) => {
            setSummoning(false)
            // The tray's job is done the moment the run exists.
            trayDispatch({ type: 'clear' })
            navigate(`/${encodeURIComponent(project)}/runs/${encodeURIComponent(runId)}`)
          }}
        />
      )}
    </>
  )

  return { shortcuts, overlays, busy: summoning, isStaged }
}

/* ---------------------------------------------------------------------------
 * Stage controls.
 * ------------------------------------------------------------------------- */

/** The bulk bar's Stage button. */
export function StageBulkAction({ project, ids, issues }: { project: string; ids: string[]; issues: Issue[] }) {
  const stage = useStageInto(project, issues)
  return (
    <button
      type="button"
      className="btn btn-quiet h-6 text-[11.5px]"
      onClick={() => stage(ids)}
      title="Add the selection to the summon queue (closed issues are skipped)"
    >
      Stage <kbd className="kbd ml-1">q</kbd>
    </button>
  )
}

/** The issue sheet's queue state and Stage button. */
export function StageIssueAction({ project, issue }: { project: string; issue: IssueDetail }) {
  const issues = useMemo(() => [issue], [issue])
  const stage = useStageInto(project, issues)
  const staged = useStaged(project).includes(issue.id)
  if (staged) {
    return (
      <span
        className="chip"
        data-tone="review"
        title="In the djinn queue — client-local, the bead's status is untouched"
      >
        queued for djinn
      </span>
    )
  }
  if (issue.status === 'closed') return null
  return (
    <button
      type="button"
      className="btn h-6 text-[11.5px]"
      onClick={() => stage([issue.id])}
      title="Add to the djinn queue (q) — nothing changes on the bead"
    >
      Stage for djinn
    </button>
  )
}
