import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { CSSProperties } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useVirtualizer } from '@tanstack/react-virtual'
import type { ApiError } from '@/api/client'
import type { Project } from '@/api/types'
import { useBdQuery, useIssue, useIssues, useStale, useStats } from '@/api/hooks'
import { TopBar } from '@/components/TopBar'
import { BacklogRow, GroupHeader } from '@/components/BacklogRow'
import { BacklogSkeleton, EmptyState, ErrorState, UnreachableState } from '@/components/states'
import { IssueDetail } from '@/views/IssueDetail'
import {
  GROUP_MODES,
  groupIssues,
  quickFilter,
  toRows,
  type BacklogRow as Row,
  type GroupMode,
} from '@/lib/grouping'
import { useShortcuts } from '@/keyboard/useShortcuts'
import { useWorkspace } from '@/components/workspace'
import { StaleBanner } from '@/components/StaleBanner'
import { StatsStrip } from '@/components/StatsStrip'
import { SaveViewButton, useSavedViews } from '@/components/SavedViews'
import {
  HIERARCHY_MODES,
  filterByHierarchy,
  type HierarchyMode,
} from '@/lib/board'
import { countSummary, loadMoreLabel, nextPageSize } from '@/lib/paging'
import { idColumnChars } from '@/lib/idcolumn'

const GROUP_H = 26
const ROW_H = 30

export function BacklogView({ projects }: { projects: Project[] }) {
  const params = useParams<{ project: string; id?: string }>()
  const navigate = useNavigate()
  const projectName = params.project ?? ''
  const project = projects.find((p) => p.name === projectName)
  const openId = params.id

  const [groupMode, setGroupMode] = useState<GroupMode>('status')
  const [showClosed, setShowClosed] = useState(false)
  const [query, setQuery] = useState('')
  const [staleOnly, setStaleOnly] = useState(false)
  const [hierarchy, setHierarchy] = useState<HierarchyMode>('all')
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set())
  const [focusIndex, setFocusIndex] = useState(-1)

  const [urlParams, setUrlParams] = useSearchParams()
  const epicId = urlParams.get('epic') ?? ''
  const noReceipt = urlParams.get('noReceipt') === 'true'
  const savedViews = useSavedViews(projectName)
  const searchRef = useRef<HTMLInputElement>(null)
  const scrollRef = useRef<HTMLDivElement>(null)

  const reachable = project?.reachable ?? true
  const {
    issues,
    total,
    hasMore,
    stalled,
    loadMore,
    isLoadingMore,
    isLoading,
    isFetching,
    error,
    loadMoreError,
    refetch,
  } = useIssues(projectName, {
    includeClosed: showClosed,
    epic: epicId || undefined,
    filter: noReceipt ? { noReceipt: true } : undefined,
    // Keep serving whatever we already have while the server is away; the
    // query simply stops refetching.
    enabled: reachable,
  })
  const epic = useIssue(projectName, epicId || undefined)
  // Data we fetched before the outage is still worth showing.
  const staleProject = !reachable && issues.length > 0
  /**
   * The failure that has actually left the view with nothing — the only one
   * allowed to take the whole screen.
   *
   * A page-2 failure sets `error` too, but it touched none of the rows page 1
   * already delivered: taking the screen there would swap 200 real rows for an
   * error whose Retry then refetches every page. That failure is reported
   * beside the load-more control instead, which is both where the click was
   * and the control that retries exactly the page that failed.
   */
  const fatalError = issues.length === 0 ? error : null
  /**
   * The third failure, and the quietest one: a refresh of rows we already have
   * that came back empty-handed.
   *
   * A staleTime expiry, a window-focus refetch, or an invalidation after an
   * external `bd` write can all fail with rows on screen. `isFetchNextPageError`
   * is false — nobody clicked load-more — so the line beside that control never
   * sees it, and the rows are real, so the error screen must not take them.
   * Which leaves it reported nowhere: a screen of hours-old rows that reads
   * exactly like a screen of fresh ones. So it is said the way the outage is
   * said — a bar above the table that costs the reader none of it.
   *
   * The outage banner outranks it: when the project itself is unreachable that
   * banner already names this failure, more specifically and with its own
   * re-check, and two bars for one outage is just noise.
   */
  const refetchError = issues.length > 0 && !loadMoreError && !staleProject ? error : null
  const stats = useStats(projectName, reachable)
  const stale = useStale(projectName, 14, reachable)

  /**
   * `bd query` escape hatch.
   *
   * A leading `>` switches the filter box into raw mode. It is the same box on
   * purpose: the manager is already typing there, and a separate "advanced
   * search" panel would be one more thing to find. bd's own parse error is
   * rendered verbatim underneath.
   */
  const raw = query.startsWith('>')
  const expression = raw ? query.slice(1).trim() : ''
  const bdQuery = useBdQuery(projectName, expression, reachable)

  const staleIds = useMemo(
    () => new Set((stale.data?.issues ?? []).map((i) => i.id)),
    [stale.data],
  )
  const staleDaysById = useMemo(
    () => new Map((stale.data?.issues ?? []).map((i) => [i.id, i.staleDays])),
    [stale.data],
  )

  /** What the table is showing: the backlog, or a bd query's result set. */
  const source = raw ? (bdQuery.data?.issues ?? []) : issues

  const filtered = useMemo(() => {
    const base = staleOnly ? source.filter((i) => staleIds.has(i.id)) : source
    // In raw mode bd has already answered; a substring filter on top would be
    // a second, invisible predicate.
    return raw ? base : quickFilter(base, query)
  }, [source, raw, query, staleOnly, staleIds])
  const scoped = useMemo(
    () => filterByHierarchy(filtered, hierarchy),
    [filtered, hierarchy],
  )
  const rows: Row[] = useMemo(
    // `source`, not `scoped`, is the second argument: epic grouping resolves each
    // row's epic from the whole page, and every filter above this line can remove
    // the epic rows while leaving their children on screen — "Leaves" removes
    // every epic by definition.
    () => toRows(groupIssues(scoped, groupMode, source), collapsed),
    [scoped, source, groupMode, collapsed],
  )
  /**
   * The three numbers the status line reconciles.
   *
   * `loaded` and `total` are the same in raw `bd query` mode — bd answers in
   * one shot and this view does not page it — so the summary collapses to the
   * plain "shown of matched" sentence there without a second code path.
   */
  const loaded = raw ? source.length : issues.length
  const matched = raw ? source.length : total

  const issueIndexes = useMemo(
    () => rows.map((r, i) => (r.kind === 'issue' ? i : -1)).filter((i) => i >= 0),
    [rows],
  )

  /** Ids in visual order — what shift-ranges and select-all measure against. */
  const orderedIds = useMemo(
    () => rows.flatMap((r) => (r.kind === 'issue' ? [r.issue.id] : [])),
    [rows],
  )

  /**
   * The id column sizes itself to the ids actually loaded, and everything the
   * window gives beyond that goes to the title (the row's only `flex-1` cell).
   *
   * Measured from `source` — the rows in hand — rather than from `scoped`, so
   * typing in the quick filter never resizes a column and shifts the table
   * under the reader. It widens only when a longer id genuinely arrives.
   */
  const idChars = useMemo(() => idColumnChars(source.map((i) => i.id)), [source])
  const focusedRow = rows[focusIndex]
  const focusedId = focusedRow?.kind === 'issue' ? focusedRow.issue.id : undefined

  const ws = useWorkspace({
    project: projectName,
    issues,
    order: orderedIds,
    ...(focusedId ? { focusedId } : {}),
    focusedEl: () => document.querySelector(`[data-row="${focusedId}"]`),
    onOpenIssue: (id) => toRoute(id),
    readOnly: !reachable,
  })

  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: (i) => (rows[i]?.kind === 'group' ? GROUP_H : ROW_H),
    overscan: 14,
    getItemKey: (i) => rows[i]?.key ?? i,
  })

  /* — keyboard —————————————————————————————————————————————————————————— */

  const moveFocus = useCallback(
    (delta: number) => {
      if (issueIndexes.length === 0) return
      const current = issueIndexes.indexOf(focusIndex)
      // Nothing focused yet: j starts at the top, k starts at the bottom.
      const next =
        current === -1
          ? delta > 0
            ? 0
            : issueIndexes.length - 1
          : Math.min(Math.max(current + delta, 0), issueIndexes.length - 1)
      const target = issueIndexes[next]!
      setFocusIndex(target)
      virtualizer.scrollToIndex(target, { align: 'auto' })
    },
    [focusIndex, issueIndexes, virtualizer],
  )

  const jumpTo = useCallback(
    (edge: 'first' | 'last') => {
      const target = edge === 'first' ? issueIndexes[0] : issueIndexes[issueIndexes.length - 1]
      if (target === undefined) return
      setFocusIndex(target)
      virtualizer.scrollToIndex(target, { align: edge === 'first' ? 'start' : 'end' })
    },
    [issueIndexes, virtualizer],
  )

  const toRoute = useCallback(
    (id?: string) =>
      navigate(
        id
          ? `/${encodeURIComponent(projectName)}/issue/${encodeURIComponent(id)}`
          : `/${encodeURIComponent(projectName)}/backlog`,
      ),
    [navigate, projectName],
  )

  const toggleGroup = useCallback((key: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }, [])

  useShortcuts(
    {
    ...ws.shortcuts,
    'row.next': () => moveFocus(1),
    'row.prev': () => moveFocus(-1),
    'row.first': () => jumpTo('first'),
    'row.last': () => jumpTo('last'),
    'issue.open': () => {
      const row = rows[focusIndex]
      if (row?.kind === 'issue') toRoute(row.issue.id)
    },
    'issue.close': () => {
      // One key, three sensible meanings — closest context wins.
      if (openId) return toRoute()
      if (document.activeElement === searchRef.current) return searchRef.current?.blur()
      if (query) return setQuery('')
      if (ws.selection.ids.size > 0) return ws.clear()
      setFocusIndex(-1)
    },
    'group.toggle': () => {
      const row = rows[focusIndex]
      if (row?.kind === 'issue') toggleGroup(row.groupKey)
    },
    'search.focus': () => searchRef.current?.focus(),
    'view.backlog': () => toRoute(),
    'view.board': () => navigate(`/${encodeURIComponent(projectName)}/board`),
    },
    // A menu or modal owns the keyboard while it is up.
    !ws.isBusy,
  )

  /**
   * Apply a saved view arriving as `?view=<id>`.
   *
   * The palette navigates rather than reaching into this component's state, so
   * a view applied from the palette, from the sidebar, or from a pasted URL all
   * take the same path. The parameter is consumed immediately so a later
   * refresh does not silently re-apply it over whatever the user has since
   * typed.
   */
  useEffect(() => {
    const id = urlParams.get('view')
    if (!id) return
    const view = savedViews.find((v) => v.id === id)
    if (view) {
      setQuery(view.query)
      setGroupMode(view.group)
      setShowClosed(view.includeClosed)
      setStaleOnly(view.staleOnly)
      setHierarchy(view.hierarchy ?? 'all')
    }
    const next = new URLSearchParams(urlParams)
    next.delete('view')
    setUrlParams(next, { replace: true })
  }, [urlParams, savedViews, setUrlParams])

  // A collapsed or filtered-away focus target would strand the focus ring
  // off-screen; snap it back to the top of what is actually visible.
  useEffect(() => {
    if (focusIndex >= 0 && rows[focusIndex]?.kind !== 'issue') setFocusIndex(-1)
  }, [rows, focusIndex])

  /* — render ———————————————————————————————————————————————————————————— */

  // Dropping back to the unscoped backlog. Shared by the scope chip, the empty
  // state, and the error state's escape hatch so the three cannot drift.
  const clearScope = () => {
    const next = new URLSearchParams(urlParams)
    next.delete('epic')
    next.delete('noReceipt')
    setUrlParams(next, { replace: true })
  }

  const controls = (
    <>
      {/*
        The scope chip is a claim about the rows below it: "these are only this
        epic's subtree". When the query failed outright there are no such rows,
        so the chip would be asserting a filter over an error — the exact
        silently-wrong affordance loom-9st is about. It goes away with the data,
        and the way out of the scope reappears inside ErrorState below. It stays
        for a failed *later* page, where the rows below it are real and scoped.
      */}
      {!fatalError && (epicId || noReceipt) && (
        <button
          type="button"
          className="chip max-w-[220px] cursor-pointer gap-1.5 hover:bg-hover"
          title={noReceipt
            ? `No receipt · ${(epic.data?.title ?? epicId) || 'all epics'} — closed issues missing acceptance criteria or a close reason`
            : `Showing only ${(epic.data?.title ?? epicId) || epicId}'s subtree`}
          onClick={clearScope}
        >
          <span className="truncate">
            {noReceipt && 'No receipt · '}{epicId ? (epic.data?.title ?? epicId) : 'all epics'}
          </span>
          <span aria-hidden="true">✕</span>
          <span className="sr-only">Show the whole backlog</span>
        </button>
      )}

      <label className="relative flex items-center">
        <span className="sr-only">Filter issues</span>
        <SearchGlyph />
        <input
          ref={searchRef}
          className="field w-[168px] pl-6"
          type="search"
          value={query}
          placeholder="Filter…  › for bd query"
          onChange={(e) => setQuery(e.target.value)}
          style={raw ? { boxShadow: 'inset 0 0 0 1px var(--accent)' } : undefined}
          aria-label={raw ? 'bd query expression' : 'Filter issues'}
        />
        {raw && (
          <span
            className="mono pointer-events-none absolute -bottom-4 left-0 text-[9.5px]"
            style={{ color: 'var(--accent)' }}
          >
            bd query
          </span>
        )}
      </label>

      <div className="seg" role="group" aria-label="Group by">
        {GROUP_MODES.map((m) => (
          <button
            key={m.id}
            type="button"
            aria-current={m.id === groupMode ? 'true' : undefined}
            onClick={() => setGroupMode(m.id)}
            title={`Group by ${m.label.toLowerCase()}`}
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
        aria-pressed={staleOnly}
        onClick={() => setStaleOnly((v) => !v)}
        title={`Only issues untouched for ${stale.data?.days ?? 14}+ days`}
        style={staleOnly ? { color: 'var(--warn)' } : undefined}
      >
        Stale
      </button>

      <button
        type="button"
        className="btn btn-quiet h-[26px] text-[11.5px]"
        aria-pressed={showClosed}
        onClick={() => setShowClosed((v) => !v)}
        title="Closed issues are hidden by default"
      >
        Closed
      </button>

      <SaveViewButton
        project={projectName}
        current={{
          name: '',
          query,
          raw,
          group: groupMode,
          includeClosed: showClosed,
          staleOnly,
          hierarchy,
        }}
      />

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
    // Only take the whole screen when there is genuinely nothing to show;
    // otherwise the cached rows stay put behind the stale banner.
    if (project && !project.reachable && !staleProject) {
      return (
        <UnreachableState
          project={project.name}
          reason={project.reason}
          workdir={project.workdir}
        />
      )
    }
    if (raw && bdQuery.error) {
      // bd explains its own syntax better than we could, so its stderr is
      // shown verbatim rather than paraphrased.
      return (
        <div className="px-4 py-6">
          <p className="mb-2 flex items-center gap-2">
            <span className="chip" data-tone="danger" data-solid="true">
              bd query
            </span>
            <span className="text-[12.5px] text-ink">
              bd could not parse that expression
            </span>
          </p>
          <pre className="mono max-w-[70ch] rounded-md bg-sunken px-3 py-2 text-[11.5px] leading-[1.55] whitespace-pre-wrap text-muted ring-1 ring-line">
            {bdQuery.error.stderr ?? bdQuery.error.message}
          </pre>
          <p className="mt-2 max-w-[70ch] text-[11.5px] leading-relaxed text-faint">
            Fields: status, priority, type, assignee, owner, label, title, description,
            notes, created, updated, closed, id, parent, pinned. Operators:{' '}
            <span className="mono">= != &gt; &gt;= &lt; &lt;=</span>, AND / OR / NOT and
            parentheses; relative dates like <span className="mono">7d</span>.
          </p>
        </div>
      )
    }
    if (raw && bdQuery.isLoading) return <BacklogSkeleton />
    if (isLoading) return <BacklogSkeleton />
    if (fatalError) {
      return (
        <ErrorState
          error={fatalError}
          onRetry={refetch}
          action={
            (epicId || noReceipt) && (
              <button type="button" className="btn" onClick={clearScope}>
                Show the whole backlog
              </button>
            )
          }
        />
      )
    }

    if (rows.length === 0 && noReceipt) {
      return (
        <EmptyState
          title={epicId ? 'No closes without receipts in this epic' : 'No closes without receipts'}
          detail={epicId
            ? <>No closed descendants of <span className="mono">{epic.data?.title ?? epicId}</span> are missing acceptance criteria or a close reason.</>
            : <>No closed issues in this project are missing acceptance criteria or a close reason.</>}
          action={
            <button
              type="button"
              className="btn"
              onClick={clearScope}
            >
              Show the whole backlog
            </button>
          }
        />
      )
    }

    if (rows.length === 0 && epicId) {
      return (
        <EmptyState
          title="No issues in this epic scope"
          detail={<>This epic has no visible descendants matching the current filters: <span className="mono">{epicId}</span>.</>}
          action={
            <button
              type="button"
              className="btn"
              onClick={() => {
                const next = new URLSearchParams(urlParams)
                next.delete('epic')
                setUrlParams(next, { replace: true })
              }}
            >
              Show the whole backlog
            </button>
          }
        />
      )
    }

    if (rows.length === 0 && hierarchy !== 'all' && filtered.length > 0) {
      return (
        <EmptyState
          title="No issues at this level"
          detail={<>The hierarchy filter hides every matching issue.</>}
          action={
            <button type="button" className="btn" onClick={() => setHierarchy('all')}>
              Show all issues
            </button>
          }
        />
      )
    }

    if (rows.length === 0 && raw) {
      return (
        <EmptyState
          title="No issues match that query"
          detail={
            <>
              <span className="mono">bd query {expression}</span> returned nothing.
            </>
          }
          action={
            <button type="button" className="btn" onClick={() => setQuery('')}>
              Back to the backlog
            </button>
          }
        />
      )
    }

    if (rows.length === 0) {
      return issues.length === 0 ? (
        <EmptyState
          title={showClosed ? 'This project has no issues' : 'Nothing open in this project'}
          detail={
            showClosed ? (
              <>
                Nothing has been filed against <span className="mono">{projectName}</span> yet.
                Create the first bead with <span className="mono">bd create</span>.
              </>
            ) : (
              <>Everything is closed or deferred. Toggle <em>Closed</em> to see the archive.</>
            )
          }
        />
      ) : (
        <EmptyState
          title="No issues match"
          detail={
            <>
              Nothing in this backlog matches <span className="mono">{query}</span>.
            </>
          }
          action={
            <button type="button" className="btn" onClick={() => setQuery('')}>
              Clear filter
            </button>
          }
        />
      )
    }

    return (
      <div
        ref={scrollRef}
        className="h-full overflow-y-auto overflow-x-hidden"
        role="grid"
        aria-label={`${projectName} backlog`}
        aria-rowcount={issueIndexes.length}
        style={{ '--id-col': `${idChars}ch` } as CSSProperties}
      >
        <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
          {virtualizer.getVirtualItems().map((item) => {
            const row = rows[item.index]
            if (!row) return null
            return (
              <div
                key={item.key}
                className="absolute top-0 left-0 w-full"
                style={{ transform: `translateY(${item.start}px)`, height: item.size }}
              >
                {row.kind === 'group' ? (
                  <GroupHeader
                    group={row.group}
                    collapsed={collapsed.has(row.group.key)}
                    onToggle={() => toggleGroup(row.group.key)}
                  />
                ) : (
                  <BacklogRow
                    issue={row.issue}
                    focused={item.index === focusIndex}
                    open={row.issue.id === openId}
                    selected={ws.isSelected(row.issue.id)}
                    staged={ws.isStaged(row.issue.id)}
                    onOpen={() => {
                      setFocusIndex(item.index)
                      toRoute(row.issue.id)
                    }}
                    onFocus={() => setFocusIndex(item.index)}
                    onToggleSelect={(shift) => ws.toggle(row.issue.id, shift)}
                    onOpenMenu={(kind, anchor) => ws.openMenu(kind, anchor, [row.issue.id])}
                    {...(staleDaysById.has(row.issue.id)
                      ? { staleDays: staleDaysById.get(row.issue.id)! }
                      : {})}
                  />
                )}
              </div>
            )
          })}
        </div>
      </div>
    )
  }

  return (
    <div className="flex h-full min-w-0 flex-1 flex-col">
      <TopBar project={project}>{controls}</TopBar>

      {staleProject && project && <StaleBanner project={project.name} reason={project.reason} />}

      {/*
        Gated on `!raw` exactly as the load-more affordances below are: the
        error belongs to the *backlog* query, and in raw mode the table under
        this bar is showing `bd query` results the failure never touched. A
        notice that outlives the rows it describes is the same failure class
        loom-9st names (loom-rz0).
      */}
      {!raw && refetchError && <RefetchNotice error={refetchError} onRetry={refetch} />}

      {reachable && (
        <StatsStrip
          project={projectName}
          stats={stats.data}
          error={stats.error}
          onRetry={() => void stats.refetch()}
          staleCount={stale.data?.issues.length}
          onFilterStale={() => setStaleOnly((v) => !v)}
          onFilterReady={() => setQuery('')}
        />
      )}

      {/* A refetch must never blank the table — a hairline says "working". */}
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

      <StatusLine
        shown={issueIndexes.length}
        loaded={loaded}
        total={matched}
        {...(!raw && hasMore ? { onLoadMore: loadMore } : {})}
        {...(!raw && !hasMore && stalled ? { onRefetch: refetch } : {})}
        loadingMore={isLoadingMore}
        {...(!raw && loadMoreError ? { loadMoreError } : {})}
        focused={
          focusIndex >= 0 && rows[focusIndex]?.kind === 'issue'
            ? (rows[focusIndex] as Extract<Row, { kind: 'issue' }>).issue.id
            : undefined
        }
      />

      {ws.overlays}

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
 * A refresh that failed over rows we already had.
 *
 * Deliberately shaped like `StaleBanner`: same bar, same place, same closing
 * "what you are looking at" clause, same re-check on the right. The two say
 * the same thing about the same screen — these rows are the last answer, not
 * the current one — so a reader who has learned one has learned the other.
 * It is a bar rather than the error screen because the rows below it are
 * still true; the failure cost the reader nothing, and neither should the
 * report of it.
 */
function RefetchNotice({ error, onRetry }: { error: ApiError; onRetry: () => void }) {
  return (
    <div
      className="flex shrink-0 items-center gap-2.5 border-b border-line px-4 py-1.5"
      data-tone="danger"
      data-error="refetch"
      style={{ background: 'color-mix(in oklab, var(--danger) 12%, transparent)' }}
      role="status"
    >
      <span className="dot" data-status="blocked" data-filled="true" aria-hidden="true" />

      <span className="text-[11.5px] font-medium text-ink">Refresh failed</span>

      <span className="mono min-w-0 flex-1 truncate text-[10.5px] text-muted" title={error.message}>
        {error.message}
      </span>

      <span className="shrink-0 text-[10.5px] text-faint">showing the rows we already had</span>

      <button
        type="button"
        className="btn btn-quiet h-5 shrink-0 px-2 text-[10.5px]"
        onClick={onRetry}
        data-action="refetch"
        title="Ask the server for these rows again"
      >
        retry
      </button>
    </div>
  )
}

/**
 * Bottom rule: counts on the left, the focused bead id on the right — the
 * thing you're about to paste into `bd show`.
 *
 * It is also the only place the reader can reach the rows the server did not
 * send (loom-7qh). The control lives beside the count that motivates it, is
 * labelled with the exact number of rows it will pull, and is the *only* way
 * more rows arrive — nothing here fetches on scroll, because these rows are
 * grouped client-side and an appended page lands interleaved above the
 * viewport rather than below it (SCARS.md, "refetch under the user's hands").
 */
function StatusLine({
  shown,
  loaded,
  total,
  onLoadMore,
  onRefetch,
  loadingMore,
  loadMoreError,
  focused,
}: {
  shown: number
  loaded: number
  total: number
  onLoadMore?: () => void
  onRefetch?: () => void
  loadingMore?: boolean
  loadMoreError?: ApiError | null
  focused?: string
}) {
  /*
    Every sentence on this control names one number, and it is the number of
    rows the click brings back. The tooltip used to name `total - loaded`
    unclamped — on the unscoped 6,030-row project that read "the same 5,830
    outstanding rows" over a button labelled "Load 1,000 more", the promise the
    label's own doc comment forbids (loom-rz0). What is still outstanding is
    not missing from the footer: `countSummary` prints it two elements to the
    left, as "200 of 6,030 loaded".
  */
  const perPage = nextPageSize(loaded, total)
  const perClick = `${perPage.toLocaleString('en-US')} ${perPage === 1 ? 'row' : 'rows'}`
  const left = Math.max(0, total - loaded)
  const outstanding = `${left.toLocaleString('en-US')} ${left === 1 ? 'row' : 'rows'}`

  return (
    <footer className="flex shrink-0 items-center gap-3 border-t border-line px-4 py-1.5">
      <span className="mono text-[10.5px] text-faint">{countSummary({ shown, loaded, total })}</span>

      {onLoadMore && (
        <button
          type="button"
          className="btn btn-quiet h-5 px-2 text-[10.5px]"
          onClick={onLoadMore}
          disabled={loadingMore}
          data-action="load-more"
          title={
            loadMoreError
              ? `That request failed — click to ask for the same ${perClick} again`
              : `Fetch the next ${perClick} of this result from the server`
          }
        >
          {loadingMore
            ? 'Loading…'
            : loadMoreError
              ? `Retry — ${loadMoreLabel(loaded, total).toLowerCase()}`
              : loadMoreLabel(loaded, total)}
        </button>
      )}

      {/*
        Forward paging gave up short of the total — a page boundary handed back
        a row already in hand, so the row it displaced sits behind the offset
        where no further request reaches it. The honest move is to say so and
        keep a way to get there (a refetch re-reads the pages from the start),
        not to let the control vanish and leave the footer arguing with the
        table (SCARS.md, "invisible affordances"; loom-rz0).
      */}
      {onRefetch && (
        <button
          type="button"
          className="btn btn-quiet h-5 px-2 text-[10.5px]"
          onClick={onRefetch}
          data-action="reach-rest"
          title={`${outstanding} the server counts for this result never arrived — a page boundary handed back a row already in hand, or a page came back short. Re-reading the pages from the start is what reaches them.`}
        >
          Refresh to reach {outstanding}
        </button>
      )}

      {/*
        The failure that cost the reader nothing, said where the click was.
        Sitting beside the still-live control is the whole point: the sentence
        names what failed, and the thing that retries exactly that page — and
        only that page — is the button immediately to its left.
      */}
      {loadMoreError && !loadingMore && (
        <span
          className="text-[10.5px]"
          style={{ color: 'var(--danger)' }}
          data-error="load-more"
          role="status"
        >
          Could not load more — {loadMoreError.message}
        </span>
      )}

      <span className="ml-auto flex items-center gap-2 text-[10.5px] text-faint">
        {focused ? (
          <span className="mono">{focused}</span>
        ) : (
          <>
            <kbd className="kbd">j</kbd>
            <kbd className="kbd">k</kbd>
            <span>to move</span>
            <kbd className="kbd">?</kbd>
            <span>for keys</span>
          </>
        )}
      </span>
    </footer>
  )
}

function SearchGlyph() {
  return (
    <svg
      viewBox="0 0 12 12"
      width="11"
      height="11"
      aria-hidden="true"
      className="pointer-events-none absolute left-2 text-faint"
    >
      <circle cx="5" cy="5" r="3.2" fill="none" stroke="currentColor" strokeWidth="1.2" />
      <path d="M7.4 7.4 10 10" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
    </svg>
  )
}
