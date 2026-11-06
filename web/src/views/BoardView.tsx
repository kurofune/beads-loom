import { useCallback, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import {
  DndContext,
  DragOverlay,
  PointerSensor,
  pointerWithin,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragStartEvent,
} from '@dnd-kit/core'
import clsx from 'clsx'
import type { Issue, IssueStatus, Project } from '@/api/types'
import { useInitiatives, useIssues } from '@/api/hooks'
import { usePatchIssue, useReopenIssue } from '@/api/mutations'
import { TopBar } from '@/components/TopBar'
import { BoardCard } from '@/components/BoardCard'
import { BacklogSkeleton, EmptyState, ErrorState, UnreachableState } from '@/components/states'
import { IssueDetail } from '@/views/IssueDetail'
import { useWorkspace } from '@/components/workspace'
import { CloseDialog } from '@/components/CloseDialog'
import { useToast } from '@/components/Toast'
import { useInteractionLock } from '@/components/Live'
import { StaleBanner } from '@/components/StaleBanner'
import { StatusDot } from '@/components/primitives'
import { useShortcuts } from '@/keyboard/useShortcuts'
import { quickFilter } from '@/lib/grouping'
import { reveal } from '@/lib/reveal'
import { boardDropIntent, dragSource, type DropIntent } from '@/lib/optimistic'
import { EpicCloseNudge } from '@/components/EpicCloseNudge'
import {
  buildLanes,
  boardOrder,
  BOARD_PAGE_SIZE,
  boardScope,
  countLabel,
  filterByHierarchy,
  HIERARCHY_MODES,
  laneShowsColumns,
  sampleSummary,
  showsReady,
  stepColumn,
  stepRow,
  SWIMLANE_MODES,
  visibleStatuses,
  type BoardScope,
  type HierarchyMode,
  type SwimlaneMode,
} from '@/lib/board'
import {
  CLOSED_WINDOWS,
  DEFAULT_CLOSED_WINDOW,
  filterByCloseWindow,
  type ClosedWindow,
} from '@/lib/closed-window'

/**
 * The narrowest a board column goes — and, below the width where every column
 * fits at it, the width they all sit at.
 *
 * This was the column's fixed width. As a floor it keeps every narrow-window
 * behavior exactly as it was: the columns stop shrinking here and the board
 * scrolls horizontally instead. What changed is above that width, where the
 * columns used to stay 268px and leave the rest of the window empty — about
 * 680px of it on a 1999px five-column board, while the cards inside truncated
 * their titles.
 */
const COLUMN_MIN_W = 268

/**
 * The board.
 *
 * A drop is not a status assignment — it is a *verb*. Into `closed` means
 * `bd close` with a reason (so it opens the reason dialog first); out of
 * `closed` means `bd reopen`; anything else is `bd update -s`. `dropIntent()`
 * owns that decision so it is one tested function rather than a branch buried
 * in a drag handler.
 */
export function BoardView({ projects }: { projects: Project[] }) {
  const params = useParams<{ project: string; id?: string }>()
  const navigate = useNavigate()
  const projectName = params.project ?? ''
  const project = projects.find((p) => p.name === projectName)
  const openId = params.id
  const toast = useToast()

  const [lanesMode, setLanesMode] = useState<SwimlaneMode>('none')
  const [hierarchy, setHierarchy] = useState<HierarchyMode>('all')
  const [showClosed, setShowClosed] = useState(true)
  /** How far back the Closed column reaches. Board-level, because every
   *  swimlane's Closed column answers the same question. */
  const [closedWindow, setClosedWindow] = useState<ClosedWindow>(DEFAULT_CLOSED_WINDOW)
  const [query, setQuery] = useState('')
  const [focusId, setFocusId] = useState<string | null>(null)
  /** Which card is in flight. Every board drag is a card moving status. */
  const [drag, setDrag] = useState<string | null>(null)
  /** A drop onto `closed` parks here until the reason dialog resolves. */
  const [pendingClose, setPendingClose] = useState<string[] | null>(null)

  const searchRef = useRef<HTMLInputElement>(null)
  const scrollRef = useRef<HTMLDivElement>(null)

  const reachable = project?.reachable ?? true
  const { issues, total, isLoading, isFetching, error, refetch } = useIssues(projectName, {
    includeClosed: showClosed,
    enabled: reachable,
    filter: { limit: BOARD_PAGE_SIZE },
  })
  // What the board holds vs what the project holds. Every count below is
  // computed from `issues`, so this is what keeps those counts from claiming to
  // describe a project the board only sampled.
  const scope = boardScope(issues.length, total)
  const { data: initiatives } = useInitiatives(projectName, false, reachable)
  const eligible = useMemo(
    () => (initiatives?.initiatives ?? []).filter((initiative) => initiative.closeEligible),
    [initiatives],
  )
  const stale = !reachable && issues.length > 0

  const patch = usePatchIssue(projectName)
  const reopen = useReopenIssue(projectName)

  const filtered = useMemo(() => quickFilter(issues, query), [issues, query])
  const scoped = useMemo(
    () => filterByHierarchy(filtered, hierarchy),
    [filtered, hierarchy],
  )
  // Last filter in the chain, and deliberately upstream of buildLanes: the
  // column count, the lane totals and the keyboard walk order are all derived
  // from these rows, so windowing here is what makes the header read 10 rather
  // than 57 without a second count computed anywhere else.
  const windowed = useMemo(
    () => filterByCloseWindow(scoped, closedWindow),
    [scoped, closedWindow],
  )
  const statuses = useMemo(() => visibleStatuses(windowed), [windowed])
  const lanes = useMemo(
    () => buildLanes(windowed, lanesMode, statuses),
    [windowed, lanesMode, statuses],
  )
  const order = useMemo(() => boardOrder(lanes), [lanes])

  const toRoute = useCallback(
    (id?: string) =>
      navigate(
        id
          ? `/${encodeURIComponent(projectName)}/board/issue/${encodeURIComponent(id)}`
          : `/${encodeURIComponent(projectName)}/board`,
      ),
    [navigate, projectName],
  )

  /** The DOM node of a card — what a menu anchors to, and what gets revealed. */
  const cardEl = useCallback(
    (id: string | null) => (id ? document.querySelector<HTMLElement>(`[data-card="${id}"]`) : null),
    [],
  )

  const ws = useWorkspace({
    project: projectName,
    // What the workspace calls "the issues on screen" — so the close window
    // narrows it exactly as the quick filter and the hierarchy filter do.
    issues: windowed,
    order,
    ...(focusId ? { focusedId: focusId } : {}),
    focusedEl: () => cardEl(focusId),
    onOpenIssue: toRoute,
    readOnly: !reachable,
    // The board carries no summon queue panel (loom-ihl): it is a kanban of bd
    // status, and dispatch is decided in the views that own it. Staged cards
    // still read as staged here.
    floating: false,
  })

  // Staging is deliberately absent from a drop: moving a card into `open` used
  // to stage it as a side effect, which made staging undiscoverable (loom-0n6).
  // Staging is now always an explicit gesture — `q`, the bulk bar, or the
  // detail panel.
  const move = useCallback(
    (id: string, intent: DropIntent) => {
      switch (intent.kind) {
        case 'none':
          return
        case 'close':
          // Reason first: a close without one is rejected by the backend, and
          // dragging is not consent to lose the "why".
          setPendingClose([id])
          return
        case 'reopen': {
          // Two verbs when the target is not `open`: bd reopen has no target
          // status, so the follow-up patch is what actually honours the drop.
          const then = intent.then
          reopen.mutate(
            { id, ...(then?.status ? { landsIn: then.status } : {}) },
            {
              onError: (err) => toast.error(`Could not reopen ${id}`, err),
              onSuccess: () => {
                if (!then) return
                patch.mutate(
                  { id, patch: then },
                  {
                    onError: (err) =>
                      // Be precise about what happened: the issue *is* reopened,
                      // it just did not make it to the column they aimed at.
                      toast.error(`${id} reopened, but could not move it to ${then.status}`, err),
                  },
                )
              },
            },
          )
          return
        }
        case 'patch':
          patch.mutate(
            { id, patch: intent.patch },
            {
              onError: (err) =>
                toast.error(`Could not move ${id} to ${intent.patch.status ?? 'that column'}`, err),
            },
          )
      }
    },
    [patch, reopen, toast],
  )

  const sensors = useSensors(
    // 4px of slop keeps a click a click: cards are also the primary way to
    // open the detail sheet.
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
  )

  const onDragStart = (event: DragStartEvent) => {
    const source = dragSource(String(event.active.id), ws.readOnly)
    if (source) setDrag(source.beadId)
  }

  const onDragEnd = (event: DragEndEvent) => {
    setDrag(null)
    const { active, over } = event
    const source = dragSource(String(active.id), ws.readOnly)
    if (!source || !over) return

    const from = String(active.data.current?.status ?? '')
    // Column droppable ids are `lane::status` so the same status can appear in
    // every swimlane; only the status half drives a write.
    move(source.beadId, boardDropIntent(String(over.id), from))
  }

  useShortcuts(
    {
      ...ws.shortcuts,
      'row.next': () => setFocusId((id) => stepRow(lanes, id, 1)),
      'row.prev': () => setFocusId((id) => stepRow(lanes, id, -1)),
      'card.nextColumn': () => setFocusId((id) => stepColumn(lanes, id, 1)),
      'card.prevColumn': () => setFocusId((id) => stepColumn(lanes, id, -1)),
      'row.first': () => setFocusId(order[0] ?? null),
      'row.last': () => setFocusId(order[order.length - 1] ?? null),
      'issue.open': () => {
        if (focusId) toRoute(focusId)
      },
      'issue.close': () => {
        if (openId) return toRoute()
        if (document.activeElement === searchRef.current) return searchRef.current?.blur()
        if (query) return setQuery('')
        if (ws.selection.ids.size > 0) return ws.clear()
        setFocusId(null)
      },
      'search.focus': () => searchRef.current?.focus(),
      'view.backlog': () => navigate(`/${encodeURIComponent(projectName)}/backlog`),
    },
    !ws.isBusy,
  )

  // Focus the viewport does not follow is focus the user cannot see: `j` past
  // the fold used to highlight a card off screen, so the board read as if the
  // key had done nothing (loom-reg). Every focus move now scrolls the card back
  // into whichever containers clip it — its column list vertically, the board
  // horizontally and (with swimlanes) vertically too — by the nearest edge, so
  // a card already on screen never moves.
  //
  // Deliberately keyed on `focusId` alone: keying it on the lanes as well would
  // let a background refetch drag the viewport back to a card the reader had
  // scrolled away from. A layout effect, not an effect, so the scroll lands in
  // the same paint as the focus ring.
  useLayoutEffect(() => {
    reveal(cardEl(focusId))
  }, [focusId, cardEl])

  // A refetch mid-drag would rebuild the card under the cursor and drop it.
  useInteractionLock(drag !== null || pendingClose !== null)

  const dragged = drag ? issues.find((i) => i.id === drag) : undefined

  const controls = (
    <>
      <EpicCloseNudge project={projectName} eligible={eligible} readOnly={ws.readOnly} />

      <label className="relative flex items-center">
        <span className="sr-only">Filter issues</span>
        <input
          ref={searchRef}
          className="field w-[150px]"
          type="search"
          value={query}
          placeholder="Filter…"
          onChange={(e) => setQuery(e.target.value)}
        />
      </label>

      <div className="seg" role="group" aria-label="Swimlanes">
        {SWIMLANE_MODES.map((m) => (
          <button
            key={m.id}
            type="button"
            aria-current={m.id === lanesMode ? 'true' : undefined}
            onClick={() => setLanesMode(m.id)}
            title={m.id === 'none' ? 'No swimlanes' : `Swimlane by ${m.label.toLowerCase()}`}
          >
            {m.label}
          </button>
        ))}
      </div>

      <div className="seg" role="group" aria-label="Hierarchy">
        {HIERARCHY_MODES.map((m) => (
          <button
            key={m.id}
            type="button"
            aria-current={m.id === hierarchy ? 'true' : undefined}
            onClick={() => setHierarchy(m.id)}
            title={
              m.id === 'all'
                ? 'Show every issue'
                : m.id === 'top'
                  ? 'Only issues with no parent'
                  : 'Only issues with no children'
            }
          >
            {m.label}
          </button>
        ))}
      </div>

      <button
        type="button"
        className="btn btn-quiet h-[26px] text-[11.5px]"
        aria-pressed={showClosed}
        onClick={() => setShowClosed((v) => !v)}
      >
        Closed
      </button>

      <button
        type="button"
        className="btn h-[26px] text-[11.5px]"
        onClick={ws.openCreate}
        disabled={ws.readOnly}
        style={ws.readOnly ? { opacity: 0.45, cursor: 'not-allowed' } : undefined}
        title={ws.readOnly ? 'Writes are disabled while the server is unreachable' : 'New issue (c)'}
      >
        New <kbd className="kbd ml-1">c</kbd>
      </button>
    </>
  )

  const body = () => {
    if (project && !project.reachable && !stale) {
      return (
        <UnreachableState project={project.name} reason={project.reason} workdir={project.workdir} />
      )
    }
    if (isLoading) return <BacklogSkeleton />
    if (error) return <ErrorState error={error} onRetry={refetch} />

    // Deliberately `scoped`, not `windowed`: a project whose every issue is
    // closed would otherwise trip the empty state under a narrow window, and
    // the empty state has no column header — so it would swallow the very
    // chips needed to widen back out.
    const emptyContent =
      scoped.length === 0 ? (
        issues.length === 0 ? (
          <EmptyState
            title="Nothing on the board"
            detail={<>No issues in <span className="mono">{projectName}</span> yet.</>}
            action={
              <button type="button" className="btn" onClick={ws.openCreate}>
                New issue
              </button>
            }
          />
        ) : filtered.length > 0 ? (
          <EmptyState
            title="No issues at this level"
            detail={<>The hierarchy filter hides every matching issue.</>}
            action={
              <button type="button" className="btn" onClick={() => setHierarchy('all')}>
                Show all issues
              </button>
            }
          />
        ) : (
          <EmptyState
            title="No issues match"
            detail={<>Nothing matches <span className="mono">{query}</span>.</>}
            action={
              <button type="button" className="btn" onClick={() => setQuery('')}>
                Clear filter
              </button>
            }
          />
        )
      ) : null

    return (
      <DndContext
        sensors={sensors}
        collisionDetection={pointerWithin}
        onDragStart={onDragStart}
        onDragEnd={onDragEnd}
        onDragCancel={() => setDrag(null)}
      >
        {emptyContent ? (
          <div className="flex h-full min-h-0 px-3 py-2">
            <div className="min-w-0 flex-1">{emptyContent}</div>
          </div>
        ) : (
          /* Without swimlanes the columns fill the height and scroll
             individually, so all of them stay on screen — the entire point of a
             board is seeing the columns side by side. With swimlanes the lanes
             stack and the page scrolls, with each lane's columns capped so one
             enormous lane cannot bury the others. */
          <div
            ref={scrollRef}
            /* The outer half of the reveal chain: it owns the horizontal scroll
               in both layouts, and the lane scroll when swimlanes are on. */
            data-scroll="board"
            className={clsx(
              'h-full px-3 pt-2',
              lanesMode === 'none'
                ? 'overflow-x-auto overflow-y-hidden pb-3'
                : 'overflow-auto pb-6',
            )}
          >
            {lanes.map((lane) => (
              <section
                key={lane.key}
                className={clsx('flex flex-col', lanesMode === 'none' ? 'h-full' : 'mb-4')}
              >
                {lanesMode !== 'none' && (
                  <header className="sticky left-0 mb-1.5 flex items-baseline gap-2 pl-0.5">
                    <h3 className="text-[11.5px] font-semibold text-ink">{lane.title}</h3>
                    {lane.subtitle && (
                      <span className="mono text-[10px] text-faint">{lane.subtitle}</span>
                    )}
                    <span className="mono text-[10.5px] text-faint">
                      {countLabel(lane.count, scope)}
                    </span>
                    <span className="hairline ml-1 flex-1 opacity-60" aria-hidden="true" />
                  </header>
                )}

                {laneShowsColumns(lanesMode, lane) && (
                  <div
                    className={clsx(
                      'flex gap-2.5',
                      lanesMode === 'none' ? 'min-h-0 flex-1 items-stretch' : 'items-start',
                    )}
                  >
                    {lane.columns.map((column) => (
                      <Column
                        key={column.status}
                        laneKey={lane.key}
                        fill={lanesMode === 'none'}
                        status={column.status}
                        label={column.label}
                        ready={column.ready}
                        scope={scope}
                        closedWindow={closedWindow}
                        onClosedWindow={setClosedWindow}
                        issues={column.issues}
                        focusId={focusId}
                        dragId={drag}
                        isSelected={ws.isSelected}
                        isStaged={ws.isStaged}
                        onOpen={toRoute}
                        onFocus={setFocusId}
                        onToggleSelect={ws.toggle}
                        onOpenMenu={(kind, anchor, id) => ws.openMenu(kind, anchor, [id])}
                      />
                    ))}
                  </div>
                )}
              </section>
            ))}
          </div>
        )}

        {/* The card that rides the cursor. Rendered outside the columns so it
            is never clipped by a column's own scroll container. */}
        <DragOverlay dropAnimation={null}>
          {dragged && (
            <div className="w-[248px] rotate-[1.5deg] opacity-95" style={{ boxShadow: 'var(--shadow-overlay)' }}>
              <BoardCard issue={dragged} staged={ws.isStaged(dragged.id)} />
            </div>
          )}
        </DragOverlay>
      </DndContext>
    )
  }

  return (
    <div className="flex h-full min-w-0 flex-1 flex-col">
      <TopBar project={project} current="board">
        {controls}
      </TopBar>

      {stale && project && <StaleBanner project={project.name} reason={project.reason} />}

      {/* Past the page size the board is a sample of the project, and it says
          so before the reader trusts a single number below it. */}
      {scope.truncated && (
        <SampleNotice
          scope={scope}
          showClosed={showClosed}
          onHideClosed={() => setShowClosed(false)}
        />
      )}

      <div className="relative h-[1px] shrink-0 bg-line">
        {isFetching && !isLoading && (
          <span
            className="absolute inset-y-0 left-0 w-1/3 bg-accent opacity-70"
            style={{ animation: 'loom-sweep 1.1s var(--ease-out) infinite' }}
            aria-hidden="true"
          />
        )}
      </div>

      <div className="min-h-0 flex-1">{body()}</div>

      {ws.overlays}

      {/* A drag into `closed` resolves here: reason first, write second. */}
      {pendingClose && (
        <CloseDialog
          project={projectName}
          ids={pendingClose}
          onClose={() => setPendingClose(null)}
          onClosed={() => setPendingClose(null)}
        />
      )}

      {openId && (
        <IssueDetail
          project={projectName}
          id={openId}
          onClose={() => toRoute()}
          onNavigate={(id) => toRoute(id)}
        />
      )}
    </div>
  )
}

/**
 * The board admitting it is showing a sample.
 *
 * Deliberately not a "load more": a kanban of 6,030 cards is not a useful
 * artifact at any page size, so the honest end state is a board that names the
 * subset and hands over the controls that choose a better one. Every control
 * this names is visible in the top bar a few pixels above it (SCARS.md,
 * "invisible affordances") — and `hide closed` is here as a button because it
 * is the one that changes the *request*: closed work is the bulk of a mature
 * project, and dropping it is what can bring the fetch back under the page.
 */
function SampleNotice({
  scope,
  showClosed,
  onHideClosed,
}: {
  scope: BoardScope
  showClosed: boolean
  onHideClosed: () => void
}) {
  return (
    <div
      className="flex shrink-0 items-center gap-2.5 border-b border-line px-4 py-1.5"
      data-notice="sample"
      data-tone="warn"
      style={{ background: 'color-mix(in oklab, var(--warn) 12%, transparent)' }}
      role="status"
    >
      <span className="dot" data-status="blocked" data-filled="true" aria-hidden="true" />

      <span className="text-[11.5px] font-medium text-ink">{sampleSummary(scope)}</span>

      <span className="min-w-0 flex-1 truncate text-[10.5px] text-muted">
        Column and lane counts describe this sample, not the project.
      </span>

      <span className="shrink-0 text-[10.5px] text-faint">
        narrow it with Filter, or Top-level / Leaves
      </span>

      {showClosed && (
        <button
          type="button"
          className="btn btn-quiet h-5 shrink-0 px-2 text-[10.5px]"
          onClick={onHideClosed}
          title="Ask the server for open work only — the one control here that fetches fewer rows rather than hiding rows already fetched."
        >
          hide closed
        </button>
      )}
    </div>
  )
}

function Column({
  laneKey,
  fill,
  status,
  label,
  ready,
  scope,
  closedWindow,
  onClosedWindow,
  issues,
  focusId,
  dragId,
  isSelected,
  isStaged,
  onOpen,
  onFocus,
  onToggleSelect,
  onOpenMenu,
}: {
  laneKey: string
  /** Fill the available height and scroll internally (no-swimlane layout). */
  fill: boolean
  status: IssueStatus
  label: string
  ready: number
  /** Whether the board's rows are the project or a page of it — the column's
   *  count and its "N ready" figure are both derived from `issues`. */
  scope: BoardScope
  /** Closed-only: how far back this column reaches, and how to change it. */
  closedWindow: ClosedWindow
  onClosedWindow: (window: ClosedWindow) => void
  issues: Issue[]
  focusId: string | null
  dragId: string | null
  isSelected: (id: string) => boolean
  isStaged: (id: string) => boolean
  onOpen: (id: string) => void
  onFocus: (id: string) => void
  onToggleSelect: (id: string, shift?: boolean) => void
  onOpenMenu: (
    kind: 'status' | 'priority' | 'assignee' | 'labels',
    anchor: DOMRect,
    id: string,
  ) => void
}) {
  const { setNodeRef, isOver } = useDroppable({ id: `${laneKey}::${status}` })

  return (
    <div
      ref={setNodeRef}
      data-status={status}
      className={clsx(
        'flex flex-col rounded-md transition-colors duration-[--dur-fast]',
        fill ? 'h-full min-h-0' : 'max-h-[64vh]',
        isOver ? 'bg-hover' : 'bg-sunken',
      )}
      style={{
        // Equal shares of whatever the window gives, never below the floor.
        // `basis: 0` rather than `auto` is what makes the shares equal: with
        // `auto` a column holding a long card would start wider and stay wider,
        // and the board would stop reading as a grid.
        flex: `1 1 0`,
        minWidth: COLUMN_MIN_W,
        boxShadow: isOver ? 'inset 0 0 0 1px var(--accent)' : 'inset 0 0 0 1px var(--border)',
      }}
    >
      <header className="flex shrink-0 items-center gap-2 px-2.5 py-2">
        <StatusDot status={status} />
        <span className="text-[11px] font-semibold tracking-[0.04em]" style={{ color: 'var(--c)' }}>
          {label}
        </span>
        <span className="mono text-[10.5px] text-faint">{countLabel(issues.length, scope)}</span>
        {status === 'open' && showsReady(ready, scope) && (
          <span className="mono ml-auto text-[10.5px]" style={{ color: 'var(--accent)' }}>
            {ready} ready
          </span>
        )}

        {/* Closed is the one column with no natural ceiling, so it is the one
            column that gets a window — and it goes in the slot the "N ready"
            affordance already occupies on Open, in the same bare-mono
            treatment, so this costs no header row and introduces no new
            visual language. */}
        {status === 'closed' && (
          <div className="ml-auto flex items-center gap-1.5" role="group" aria-label="Closed since">
            {CLOSED_WINDOWS.map((w) => {
              const active = w.id === closedWindow
              return (
                <button
                  key={w.id}
                  type="button"
                  aria-pressed={active}
                  onClick={() => onClosedWindow(w.id)}
                  title={
                    w.id === 'today'
                      ? 'Closed today'
                      : w.id === '7d'
                        ? 'Closed in the last seven days'
                        : 'Every closed issue the board holds'
                  }
                  className={clsx(
                    'mono text-[10.5px] leading-none transition-colors duration-[--dur-fast]',
                    // Weight as well as colour: which chip is active has to be
                    // readable without hovering, and colour alone is not.
                    active ? 'font-semibold' : 'text-faint hover:text-ink',
                  )}
                  {...(active ? { style: { color: 'var(--accent)' } } : {})}
                >
                  {w.label}
                </button>
              )
            })}
          </div>
        )}
      </header>

      {/* The card list is the droppable's scroll container, so a drop lands
          on the column even when its cards overflow — and, marked, the inner
          half of the reveal chain that keeps keyboard focus on screen. */}
      <div
        data-scroll="column"
        className="flex min-h-0 flex-1 flex-col gap-1.5 overflow-y-auto px-1.5 pb-2"
      >
        {issues.map((issue) => (
          <BoardCard
            key={issue.id}
            issue={issue}
            focused={issue.id === focusId}
            selected={isSelected(issue.id)}
            staged={isStaged(issue.id)}
            dragging={issue.id === dragId}
            onOpen={() => onOpen(issue.id)}
            onFocus={() => onFocus(issue.id)}
            onToggleSelect={(shift) => onToggleSelect(issue.id, shift)}
            onOpenMenu={(kind, anchor) => onOpenMenu(kind, anchor, issue.id)}
          />
        ))}

        {issues.length === 0 && (
          <p className="px-1.5 py-6 text-center text-[11px] text-faint">
            {/* An empty column is a drop target, so say so rather than
                leaving a void that looks broken. */}
            Drop here
          </p>
        )}
      </div>
    </div>
  )
}
