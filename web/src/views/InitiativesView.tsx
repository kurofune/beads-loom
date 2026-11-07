import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useVirtualizer } from '@tanstack/react-virtual'
import clsx from 'clsx'
import type { Initiative, InitiativeUpdateSummary, Issue, Project } from '@/api/types'
import { useInitiatives, useInitiativeUpdateSummaries } from '@/api/hooks'
import { TopBar } from '@/components/TopBar'
import { BacklogSkeleton, EmptyState, ErrorState, UnreachableState } from '@/components/states'
import { StaleBanner } from '@/components/StaleBanner'
import { InitiativeHealth } from '@/components/InitiativeHealth'
import { EpicTargetBadge, EpicTargetPanel } from '@/components/EpicTargetNudge'
import { useWorkspace } from '@/components/workspace'
import { useShortcuts } from '@/keyboard/useShortcuts'
import {
  loadSort,
  progressOf,
  saveSort,
  SORT_KEYS,
  sortInitiatives,
  type InitiativeSort,
  type SortKey,
} from '@/lib/initiatives'
import { latestWeekTone } from '@/lib/updates'
import {
  isTargetNudgeDismissed,
  loadTargetDismissals,
  saveTargetDismissals,
  storedTargetDismissals,
} from '@/lib/target-nudge'
import { absoluteTime, relativeTime } from '@/lib/time'
import { IssueDetail } from '@/views/IssueDetail'
import { InitiativeUpdatesSheet } from '@/views/InitiativeUpdatesSheet'

export const INITIATIVE_COLUMNS = [
  { id: 'title', label: 'Title', width: 'minmax(260px, 2.5fr)' },
  // Wide enough for the health chip AND the missing-target badge beside it:
  // "cannot be assessed, and here is the fix" belongs in the attention column.
  { id: 'health', label: 'Health', width: '150px' },
  { id: 'noReceipt', label: 'No receipt', width: '88px' },
  { id: 'progress', label: 'Progress', width: '156px' },
  // Pipeline order: Started → Ready → Blocked → Deferred.
  { id: 'started', label: 'Started', width: '72px' },
  { id: 'ready', label: 'Ready', width: '64px' },
  { id: 'blocked', label: 'Blocked', width: '72px' },
  { id: 'deferred', label: 'Deferred', width: '76px' },
  // Not a pipeline bucket: it overlaps all four, so it sits after them.
  { id: 'chained', label: 'In chain', width: '76px' },
  { id: 'due', label: 'Due', width: '88px' },
  { id: 'assignee', label: 'Assignee', width: '112px' },
  { id: 'activity', label: 'Activity', width: '72px' },
  // Weekly updates need a visible way in; a sheet reachable only by URL is a
  // feature nobody finds (SCARS: invisible affordances).
  { id: 'updates', label: 'Updates', width: '96px' },
] as const

type InitiativeColumn = (typeof INITIATIVE_COLUMNS)[number]

/** The grid's columns for one load. Updates appears only when the project
 *  stores at least one weekly update: a plain beads project, or a summaries
 *  read that failed, gets no column it could never fill. */
export function initiativeColumns(withUpdates: boolean): readonly InitiativeColumn[] {
  return withUpdates ? INITIATIVE_COLUMNS : INITIATIVE_COLUMNS.filter((column) => column.id !== 'updates')
}

const gridTemplate = (columns: readonly InitiativeColumn[]) => columns.map((column) => column.width).join(' ')

/** The receipt rule, shared by the column header and every row's chip so the
 *  two cannot drift apart. */
const NO_RECEIPT_COUNTED_ONCE = 'a bead missing both is counted once'
const NO_RECEIPT_RULE = `Closed descendants with no acceptance criteria or no close reason; ${NO_RECEIPT_COUNTED_ONCE}.`
const CHAINED_RULE = 'Descendants on either end of a blocks dependency, closed blockers included: a shape count, not what is blocked now.'

const ROW_H = 42

export function InitiativesView({ projects }: { projects: Project[] }) {
  const params = useParams<{ project: string; id?: string; updatesId?: string }>()
  const navigate = useNavigate()
  const projectName = params.project ?? ''
  const project = projects.find((candidate) => candidate.name === projectName)
  const openId = params.id
  const openUpdatesFor = params.updatesId
  const reachable = project?.reachable ?? true

  const [includeClosed, setIncludeClosed] = useState(false)
  const [sort, setSort] = useState<InitiativeSort>(() => loadSort(projectName))
  const [focusIndex, setFocusIndex] = useState(-1)
  // The dismissal list carries the project it was loaded for. Without that tag
  // the pruning effect below cannot tell "this project has no dismissals left"
  // from "this list belongs to the project we just left", and the two demand
  // opposite actions.
  const [dismissedTargets, setDismissedTargets] = useState<{ project: string; ids: string[] }>(
    () => ({ project: projectName, ids: loadTargetDismissals(projectName) }),
  )
  const [targetPanelFor, setTargetPanelFor] = useState<string | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)

  // Reloaded during render rather than in the effect below on purpose: an
  // effect leaves one committed render where `ids` are the previous project's
  // while `projectName` and `needingTarget` are the new project's, and the
  // prune would then persist the empty intersection over the destination
  // project's stored dismissals (loom-o6m).
  if (dismissedTargets.project !== projectName) {
    setDismissedTargets({ project: projectName, ids: loadTargetDismissals(projectName) })
  }

  useEffect(() => {
    setSort(loadSort(projectName))
    setFocusIndex(-1)
    setTargetPanelFor(null)
  }, [projectName])

  const query = useInitiatives(projectName, includeClosed, reachable)
  // One request for the whole table: a project with 30 epics must not open 30
  // connections to draw 30 chips.
  const updateSummaries = useInitiativeUpdateSummaries(projectName, reachable)
  const updatesByEpic = useMemo(() => {
    const byEpic = new Map<string, InitiativeUpdateSummary>()
    for (const summary of updateSummaries.data?.updates ?? []) byEpic.set(summary.epic, summary)
    return byEpic
  }, [updateSummaries.data?.updates])
  // A failed refetch keeps the last good data, so the error state decides:
  // chips from a read that just failed would show updates nobody can open.
  const showUpdates = !updateSummaries.isError && updatesByEpic.size > 0
  const columns = initiativeColumns(showUpdates)
  const grid = gridTemplate(columns)
  const initiatives = useMemo(
    () => sortInitiatives(query.data?.initiatives ?? [], sort),
    [query.data?.initiatives, sort],
  )
  const staleProject = !reachable && initiatives.length > 0

  const needingTarget = useMemo(
    () => initiatives.filter((initiative) => initiative.needsTarget).map((initiative) => initiative.id),
    [initiatives],
  )
  // A dismissal answers one question — "this epic has no date" — so it expires
  // with the question. Pruning here is what makes a *cleared* due date nag
  // again rather than stay silent on a stale dismissal. Gated on a settled
  // response: an empty list mid-fetch is not evidence that anything was fixed.
  useEffect(() => {
    if (!query.data) return
    const next = storedTargetDismissals(dismissedTargets.ids, needingTarget)
    if (next.length === dismissedTargets.ids.length) return
    // Keyed by the list's own project, never by the current route: the two are
    // the same at commit time, and writing through the list's project is what
    // makes a mismatch impossible to persist rather than merely unlikely.
    saveTargetDismissals(dismissedTargets.project, next)
    setDismissedTargets({ project: dismissedTargets.project, ids: next })
  }, [dismissedTargets, needingTarget, query.data])

  const dismissTarget = useCallback(
    (epic: string) => {
      if (dismissedTargets.ids.includes(epic)) return
      const next = [...dismissedTargets.ids, epic].sort()
      saveTargetDismissals(dismissedTargets.project, next)
      setDismissedTargets({ project: dismissedTargets.project, ids: next })
    },
    [dismissedTargets],
  )

  const virtualizer = useVirtualizer({
    count: initiatives.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW_H,
    overscan: 12,
    getItemKey: (index) => initiatives[index]?.id ?? index,
  })

  const toIssue = useCallback(
    (id?: string) =>
      navigate(
        id
          ? `/${encodeURIComponent(projectName)}/initiatives/issue/${encodeURIComponent(id)}`
          : `/${encodeURIComponent(projectName)}/initiatives`,
      ),
    [navigate, projectName],
  )

  const toUpdates = useCallback(
    (id?: string) =>
      navigate(
        id
          ? `/${encodeURIComponent(projectName)}/initiatives/${encodeURIComponent(id)}/updates`
          : `/${encodeURIComponent(projectName)}/initiatives`,
      ),
    [navigate, projectName],
  )

  const toBlockers = useCallback(
    (id: string) => {
      // closed=1 because the count keeps pairs whose blocker has closed; the
      // graph hides closed issues by default and would draw fewer of them.
      const query = new URLSearchParams({ epic: id, closed: '1' })
      navigate(`/${encodeURIComponent(projectName)}/graph?${query.toString()}`)
    },
    [navigate, projectName],
  )

  const toBacklog = useCallback(
    (id: string, noReceipt = false) => {
      const query = new URLSearchParams({ epic: id })
      if (noReceipt) query.set('noReceipt', 'true')
      navigate(`/${encodeURIComponent(projectName)}/backlog?${query.toString()}`)
    },
    [navigate, projectName],
  )

  const moveFocus = useCallback(
    (delta: number) => {
      if (initiatives.length === 0) return
      const next = focusIndex < 0
        ? delta > 0 ? 0 : initiatives.length - 1
        : Math.min(Math.max(focusIndex + delta, 0), initiatives.length - 1)
      setFocusIndex(next)
      virtualizer.scrollToIndex(next, { align: 'auto' })
    },
    [focusIndex, initiatives.length, virtualizer],
  )

  const jumpTo = useCallback(
    (edge: 'first' | 'last') => {
      if (initiatives.length === 0) return
      const next = edge === 'first' ? 0 : initiatives.length - 1
      setFocusIndex(next)
      virtualizer.scrollToIndex(next, { align: edge === 'first' ? 'start' : 'end' })
    },
    [initiatives.length, virtualizer],
  )

  const workspaceIssues = useMemo(
    () => initiatives.map((initiative) => ({ id: initiative.id, status: initiative.status }) as Issue),
    [initiatives],
  )
  const focused = initiatives[focusIndex]
  const targetPanel = targetPanelFor
    ? initiatives.find((initiative) => initiative.id === targetPanelFor)
    : undefined
  const ws = useWorkspace({
    project: projectName,
    issues: workspaceIssues,
    order: initiatives.map((initiative) => initiative.id),
    ...(focused ? { focusedId: focused.id } : {}),
    focusedEl: () => focused ? document.querySelector(`[data-row="${focused.id}"]`) : null,
    onOpenIssue: toIssue,
    readOnly: !reachable,
  })

  useShortcuts(
    {
      'row.next': () => moveFocus(1),
      'row.prev': () => moveFocus(-1),
      'row.first': () => jumpTo('first'),
      'row.last': () => jumpTo('last'),
      'issue.open': () => focused && toIssue(focused.id),
      'issue.close': () => {
        if (openUpdatesFor) return toUpdates()
        if (openId) return toIssue()
        setFocusIndex(-1)
      },
      'view.backlog': () => navigate(`/${encodeURIComponent(projectName)}/backlog`),
      'view.board': () => navigate(`/${encodeURIComponent(projectName)}/board`),
    },
    !ws.isBusy,
  )

  const changeSort = (key: SortKey) => {
    const next: InitiativeSort = sort.key === key
      ? { key, dir: sort.dir === 'asc' ? 'desc' : 'asc' }
      : { key, dir: key === 'activity' ? 'desc' : 'asc' }
    setSort(next)
    saveSort(projectName, next)
  }

  const controls = (
    <>
      <div className="seg" role="group" aria-label="Sort initiatives">
        {SORT_KEYS.map((key) => (
          <button
            key={key.id}
            type="button"
            aria-current={sort.key === key.id ? 'true' : undefined}
            onClick={() => changeSort(key.id)}
            title={sort.key === key.id ? `Reverse ${key.label.toLowerCase()} order` : `Sort by ${key.label.toLowerCase()}`}
          >
            {key.label}{sort.key === key.id ? (sort.dir === 'asc' ? ' ↑' : ' ↓') : ''}
          </button>
        ))}
      </div>
      <button
        type="button"
        className="btn btn-quiet h-[26px] text-[11.5px]"
        aria-pressed={includeClosed}
        onClick={() => setIncludeClosed((value) => !value)}
        title="Closed epics are hidden by default; descendant rollups always include closed work"
      >
        Closed
      </button>
    </>
  )

  const body = () => {
    if (project && !project.reachable && !staleProject) {
      return <UnreachableState project={project.name} reason={project.reason} workdir={project.workdir} />
    }
    if (query.isLoading) return <BacklogSkeleton />
    if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
    if (initiatives.length === 0) {
      return (
        <EmptyState
          title="This project has no epics"
          detail={<>Create one with <span className="mono">bd create --type=epic</span>.</>}
        />
      )
    }

    return (
      <div ref={scrollRef} className="h-full overflow-auto" role="grid" aria-label={`${projectName} initiatives`} aria-rowcount={initiatives.length}>
        <div className="sticky top-0 z-10 grid min-w-[1402px] border-b border-line bg-surface px-4" style={{ gridTemplateColumns: grid }} role="row">
          {columns.map((column) => (
            <span key={column.id} className="legend flex h-7 items-center" role="columnheader">
              {column.id === 'noReceipt' ? (
                <HintedHeader label={column.label} id="no-receipt-rule" hint={NO_RECEIPT_RULE} />
              ) : column.id === 'chained' ? (
                <HintedHeader label={column.label} id="chained-rule" hint={CHAINED_RULE} />
              ) : (
                column.label
              )}
            </span>
          ))}
        </div>
        <div className="relative min-w-[1402px]" style={{ height: virtualizer.getTotalSize() }}>
          {virtualizer.getVirtualItems().map((item) => {
            const initiative = initiatives[item.index]
            if (!initiative) return null
            return (
              <InitiativeRow
                key={item.key}
                initiative={initiative}
                focused={item.index === focusIndex}
                open={initiative.id === openId}
                style={{ transform: `translateY(${item.start}px)`, height: item.size }}
                onOpen={() => {
                  setFocusIndex(item.index)
                  toIssue(initiative.id)
                }}
                onDrillDown={() => toBacklog(initiative.id)}
                onNoReceiptDrillDown={() => toBacklog(initiative.id, true)}
                onChainedDrillDown={() => toBlockers(initiative.id)}
                grid={grid}
                showUpdates={showUpdates}
                updates={updatesByEpic.get(initiative.id)}
                onOpenUpdates={() => {
                  setFocusIndex(item.index)
                  toUpdates(initiative.id)
                }}
                targetDismissed={isTargetNudgeDismissed(dismissedTargets.ids, initiative.id)}
                readOnly={!reachable}
                onOpenTarget={() => {
                  setFocusIndex(item.index)
                  setTargetPanelFor(initiative.id)
                }}
              />
            )
          })}
        </div>
      </div>
    )
  }

  return (
    <div className="flex h-full min-w-0 flex-1 flex-col">
      <TopBar project={project} current="initiatives">{controls}</TopBar>
      {staleProject && project && <StaleBanner project={project.name} reason={project.reason} />}
      <div className="relative h-[1px] shrink-0 bg-line">
        {query.isFetching && !query.isLoading && (
          <span className="absolute inset-y-0 left-0 w-1/3 bg-accent opacity-70" style={{ animation: 'loom-sweep 1.1s var(--ease-out) infinite' }} aria-hidden="true" />
        )}
      </div>
      <div className="min-h-0 flex-1">{body()}</div>
      <footer className="flex shrink-0 items-center gap-3 border-t border-line px-4 py-1.5">
        <span className="mono text-[10.5px] text-faint">{initiatives.length} initiatives</span>
        <span className="mono ml-auto text-[10.5px] text-faint">{focused?.id ?? 'j k to move'}</span>
      </footer>
      {ws.overlays}
      {/* Rendered at the view root, not inside the row: virtualized rows carry
          a transform, which would make the panel's fixed scrim resolve against
          the row instead of the viewport. */}
      {targetPanel && (
        <EpicTargetPanel
          project={projectName}
          initiative={targetPanel}
          onClose={() => setTargetPanelFor(null)}
          onDismiss={dismissTarget}
        />
      )}
      {openId && (
        <IssueDetail
          project={projectName}
          id={openId}
          onClose={() => toIssue()}
          onNavigate={(id) => toIssue(id)}
        />
      )}
      {openUpdatesFor && (
        <InitiativeUpdatesSheet
          project={projectName}
          epic={openUpdatesFor}
          onClose={() => toUpdates()}
          onOpenIssue={(id) => toIssue(id)}
          onOpenRun={(id) =>
            navigate(`/${encodeURIComponent(projectName)}/runs/${encodeURIComponent(id)}`)
          }
        />
      )}
    </div>
  )
}

function InitiativeRow({
  initiative,
  focused,
  open,
  style,
  onOpen,
  onDrillDown,
  onNoReceiptDrillDown,
  onChainedDrillDown,
  grid,
  showUpdates,
  updates,
  onOpenUpdates,
  targetDismissed,
  readOnly,
  onOpenTarget,
}: {
  initiative: Initiative
  focused: boolean
  open: boolean
  style: React.CSSProperties
  onOpen: () => void
  onDrillDown: () => void
  onNoReceiptDrillDown: () => void
  onChainedDrillDown: () => void
  grid: string
  showUpdates: boolean
  /** Absent when this epic has no stored weekly updates. */
  updates?: InitiativeUpdateSummary
  onOpenUpdates: () => void
  targetDismissed: boolean
  readOnly: boolean
  onOpenTarget: () => void
}) {
  const hasChildren = initiative.subtree.total > 0
  const progress = progressOf(initiative)
  // Its own share of the bar, laid after the closed segment — never added to it.
  const startedShare = hasChildren ? initiative.subtree.started / initiative.subtree.total : 0
  const noReceipt = initiative.noReceipt
  const overdue = initiative.due !== null && Date.parse(initiative.due) < Date.now() && initiative.status !== 'closed'
  const drill = (event: React.MouseEvent) => {
    event.stopPropagation()
    onDrillDown()
  }
  const drillNoReceipt = (event: React.MouseEvent) => {
    event.stopPropagation()
    onNoReceiptDrillDown()
  }
  const drillChained = (event: React.MouseEvent) => {
    event.stopPropagation()
    onChainedDrillDown()
  }
  const chained = initiative.subtree.chained
  const receiptBreakdown = `${noReceipt.noAcceptance} closed with no acceptance criteria; ${noReceipt.noCloseReason} closed with no close reason; ${NO_RECEIPT_COUNTED_ONCE}.`
  const receiptTooltipID = `no-receipt-${initiative.id}`

  return (
    <div
      className={clsx('absolute top-0 left-0 grid w-full cursor-pointer items-center border-b border-line px-4 text-[12px] transition-colors hover:bg-hover', focused && 'bg-selected', open && 'ring-inset ring-1 ring-accent')}
      style={{ ...style, gridTemplateColumns: grid }}
      role="row"
      data-row={initiative.id}
      aria-selected={focused}
      onClick={onOpen}
    >
      <span className="flex min-w-0 items-baseline gap-2 pr-4" role="gridcell">
        <button type="button" className="min-w-0 truncate text-left font-medium text-ink hover:text-accent" onClick={(event) => { event.stopPropagation(); onOpen() }} title={`Open ${initiative.title}`}>
          {initiative.title}
        </button>
        <span className="mono shrink-0 text-[10px] text-faint">{initiative.id}</span>
      </span>

      <span className="flex min-w-0 items-center gap-1.5 pr-3" role="gridcell">
        <InitiativeHealth health={initiative.health} onClick={onOpen} />
        <EpicTargetBadge
          initiative={initiative}
          dismissed={targetDismissed}
          readOnly={readOnly}
          onOpen={onOpenTarget}
        />
      </span>

      <span role="gridcell">
        {noReceipt.total > 0 && (
          <button
            type="button"
            className="group relative cursor-pointer"
            onClick={drillNoReceipt}
            title={receiptBreakdown}
            aria-label={`${noReceipt.total} closed descendants with no receipt. ${receiptBreakdown}`}
            aria-describedby={receiptTooltipID}
          >
            <span className="chip" data-tone="warn" data-outline="true">
              {noReceipt.total}
            </span>
            <span
              id={receiptTooltipID}
              role="tooltip"
              className="pointer-events-none absolute top-full left-0 z-20 mt-1 w-60 rounded-md bg-overlay px-2.5 py-2 text-left text-[11px] leading-relaxed text-ink opacity-0 shadow-lg ring-1 ring-line transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100"
            >
              {receiptBreakdown}
            </span>
          </button>
        )}
      </span>

      <span role="gridcell">
        {hasChildren ? (
          <button type="button" className="group flex w-[140px] cursor-pointer items-center gap-2 rounded-sm px-1 py-1 text-left hover:bg-active" onClick={drill} title={`${initiative.subtree.closed} of ${initiative.subtree.total} closed and ${initiative.subtree.started} started across the full subtree (all descendants, not just direct children); open this subtree in Backlog`}>
            <span className="mono w-[38px] shrink-0 text-[10.5px] text-muted">{initiative.subtree.closed}/{initiative.subtree.total}</span>
            <span className="flex h-[4px] flex-1 overflow-hidden rounded-full bg-active" role="img" aria-label={`${initiative.subtree.closed} of ${initiative.subtree.total} descendants closed, ${initiative.subtree.started} started`}>
              <span style={{ width: `${(progress ?? 0) * 100}%`, background: 'var(--accent)' }} data-segment="closed" />
              <span style={{ width: `${startedShare * 100}%`, background: 'var(--status-in-progress)' }} data-segment="started" />
            </span>
          </button>
        ) : (
          <span className="text-[11px] text-faint">no children</span>
        )}
      </span>

      <span role="gridcell">
        {hasChildren && initiative.subtree.started > 0 && <button type="button" className="mono cursor-pointer rounded-sm px-1 py-1 hover:bg-active" style={{ color: 'var(--status-in-progress)' }} onClick={drill} title="Descendants already under way (in_progress or hooked); open this epic's full subtree in Backlog">{initiative.subtree.started}</button>}
      </span>
      <span role="gridcell">
        {hasChildren && <button type="button" className="mono cursor-pointer rounded-sm px-1 py-1 hover:bg-active" style={{ color: 'var(--accent)' }} onClick={drill} title="Open this epic's full subtree in Backlog">{initiative.subtree.ready}</button>}
      </span>
      <span role="gridcell">
        {hasChildren && initiative.subtree.depBlocked > 0 && <button type="button" className="mono cursor-pointer rounded-sm px-1 py-1 hover:bg-active" style={{ color: 'var(--status-blocked)' }} onClick={drill} title="Open this epic's full subtree in Backlog">{initiative.subtree.depBlocked}</button>}
      </span>
      <span className="mono text-muted" role="gridcell" title={initiative.subtree.deferScheduled > 0 ? 'held by schedule, not dependencies' : undefined}>
        {hasChildren && initiative.subtree.deferScheduled > 0 ? initiative.subtree.deferScheduled : ''}
      </span>
      <span role="gridcell">
        {hasChildren && chained > 0 && (
          <button
            type="button"
            className="mono cursor-pointer rounded-sm px-1 py-1 text-ink hover:bg-active"
            onClick={drillChained}
            title={`${chained} of ${initiative.subtree.total} descendants sit on either end of a blocks dependency, closed blockers included; open this epic in the blockers graph`}
            aria-label={`${chained} ${chained === 1 ? 'descendant' : 'descendants'} in a blocking chain; open the blockers graph for ${initiative.id}`}
          >
            {chained}
          </button>
        )}
      </span>
      <span className="text-[11px]" role="gridcell" title={absoluteTime(initiative.due)} style={overdue ? { color: 'var(--danger)' } : undefined}>
        {relativeTime(initiative.due)}
      </span>
      <span className="truncate pr-3 text-muted" role="gridcell" title={initiative.assignee || 'Unassigned'}>{initiative.assignee || '—'}</span>
      <span className="text-[11px] text-muted" role="gridcell" title={absoluteTime(initiative.lastActivity)}>{relativeTime(initiative.lastActivity)}</span>
      {showUpdates && <span role="gridcell">
        {updates && updates.total > 0 && (
          <button
            type="button"
            className="chip cursor-pointer"
            data-tone={latestWeekTone(updates.latestStatus)}
            data-outline={updates.latestStatus === 'draft' ? 'true' : undefined}
            onClick={(event) => {
              event.stopPropagation()
              onOpenUpdates()
            }}
            aria-label={`Weekly updates for ${initiative.id}: ${updatesTooltip(updates)}`}
            title={updatesTooltip(updates)}
          >
            {updates.latestWeek.replace(/^\d{4}-/, '')}
            {updates.latestStatus === 'draft' ? ' draft' : ''}
          </button>
        )}
      </span>}
    </div>
  )
}

/** The column header states the receipt rule itself, so the count means the
 *  same thing before a reader hovers any one row. */
function HintedHeader({ label, id, hint }: { label: string; id: string; hint: string }) {
  return (
    <span className="group relative cursor-help rounded-sm outline-offset-2" tabIndex={0} aria-describedby={id}>
      <span className="underline decoration-dotted underline-offset-2">{label}</span>
      <span
        id={id}
        role="tooltip"
        className="pointer-events-none absolute top-full left-0 z-20 mt-1 w-60 rounded-md bg-overlay px-2.5 py-2 text-left text-[11px] leading-relaxed font-normal tracking-normal text-ink normal-case opacity-0 shadow-lg ring-1 ring-line transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100"
      >
        {hint}
      </span>
    </span>
  )
}

/** "12 updates, 2 drafts; newest 2026-W32 (published)" — counts first, because
 *  the number of unpublished drafts is the thing a manager acts on. */
function updatesTooltip(summary: InitiativeUpdateSummary): string {
  const updates = `${summary.total} ${summary.total === 1 ? 'update' : 'updates'}`
  const drafts = summary.draftCount > 0 ? `, ${summary.draftCount} draft${summary.draftCount === 1 ? '' : 's'}` : ''
  return `${updates}${drafts}; newest ${summary.latestWeek} (${summary.latestStatus || 'unknown status'})`
}
