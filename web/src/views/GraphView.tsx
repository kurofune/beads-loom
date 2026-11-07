import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import clsx from 'clsx'
import { api, ApiError } from '@/api/client'
import { qk, useIssues } from '@/api/hooks'
import type { GraphEdge, GraphNode, GraphResponse, Project } from '@/api/types'
import { TopBar } from '@/components/TopBar'
import { BacklogSkeleton, EmptyState, ErrorState, UnreachableState } from '@/components/states'
import { IssueDetail } from '@/views/IssueDetail'
import { Menu, type MenuItem } from '@/components/Menu'
import { TypeGlyph, typeColor } from '@/components/TypeGlyph'
import { PriorityBadge } from '@/components/primitives'
import { isActive } from '@/lib/beads'
import { useShortcuts } from '@/keyboard/useShortcuts'
import {
  blockChain,
  blockersView,
  crossesEpic,
  edgePath,
  epicIndex,
  epicsIn,
  type Band,
} from '@/lib/graph'

/**
 * The dependency graph: what is blocked, and by what.
 *
 * The view draws only issues that touch a `blocks` edge. Why that is the whole
 * canvas rather than one mode of several is argued in `lib/graph.ts` — briefly,
 * three quarters of the edges in a real project are hierarchy, and drawing them
 * produced a row 15,000 px wide that answered nothing a list could not.
 *
 * Layout is the browser's: each band is a wrapping row, so a band too wide for
 * the pane wraps instead of extending the canvas. This file measures the cards
 * once they are laid out and draws the edges over them, which is the only reason
 * it needs geometry at all.
 */

/** Space between bands, and the room the edge curves need to read. */
const BAND_GAP = 'mb-12'

interface Geometry {
  width: number
  height: number
  at: Map<string, { cx: number; top: number; bottom: number }>
}

export function GraphView({ projects }: { projects: Project[] }) {
  const params = useParams<{ project: string; id?: string }>()
  const [search, setSearch] = useSearchParams()
  const navigate = useNavigate()
  const projectName = params.project ?? ''
  const project = projects.find((p) => p.name === projectName)
  const openId = params.id
  const epic = search.get('epic') ?? ''

  // In the URL so a drill from Initiatives can land with closed issues shown.
  const includeClosed = search.get('closed') === '1'
  const [focus, setFocus] = useState<string | null>(null)
  const [epicMenu, setEpicMenu] = useState<DOMRect | null>(null)

  const reachable = project?.reachable ?? true
  const graph = useQuery<GraphResponse, ApiError>({
    queryKey: qk.graph(projectName, epic, includeClosed),
    queryFn: () => api.graph(projectName, { ...(epic ? { epic } : {}), includeClosed }),
    enabled: Boolean(projectName) && reachable,
    staleTime: 10_000,
  })

  const view = useMemo(() => (graph.data ? blockersView(graph.data) : null), [graph.data])
  const epics = useMemo(
    () => (graph.data ? epicIndex(graph.data) : new Map<string, string | null>()),
    [graph.data],
  )
  const titleOf = useMemo(
    () => new Map((graph.data?.nodes ?? []).map((n) => [n.id, n.title])),
    [graph.data],
  )
  const cyclicSet = useMemo(() => new Set(view?.cyclic ?? []), [view])

  // A scope change can leave a focused id that is no longer on screen, which
  // would dim every card and light none.
  const onScreen = useMemo(
    () => new Set((view?.bands ?? []).flatMap((b) => b.nodes.map((n) => n.id))),
    [view],
  )
  useEffect(() => {
    if (focus && !onScreen.has(focus)) setFocus(null)
  }, [focus, onScreen])

  const chain = useMemo(
    () => (focus && view ? blockChain(focus, view.edges) : null),
    [focus, view],
  )
  const lit = useMemo(() => {
    if (!focus || !chain) return null
    return new Set([focus, ...chain.blockers, ...chain.blocked])
  }, [focus, chain])

  /* --- geometry for the edge overlay ------------------------------------- */

  const stageRef = useRef<HTMLDivElement>(null)
  const cards = useRef(new Map<string, HTMLElement>())
  const [geometry, setGeometry] = useState<Geometry | null>(null)

  const setCard = useCallback((id: string, el: HTMLElement | null) => {
    if (el) cards.current.set(id, el)
    else cards.current.delete(id)
  }, [])

  const measure = useCallback(() => {
    const stage = stageRef.current
    if (!stage) return
    const box = stage.getBoundingClientRect()
    const at = new Map<string, { cx: number; top: number; bottom: number }>()
    for (const [id, el] of cards.current) {
      const rect = el.getBoundingClientRect()
      at.set(id, {
        cx: rect.left - box.left + rect.width / 2,
        top: rect.top - box.top,
        bottom: rect.top - box.top + rect.height,
      })
    }
    setGeometry({ width: stage.clientWidth, height: stage.clientHeight, at })
  }, [])

  // The overlay is absolutely positioned and does not affect the stage's size,
  // so observing the stage cannot feed back into another resize.
  useLayoutEffect(() => {
    measure()
    const stage = stageRef.current
    if (!stage || typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(() => measure())
    observer.observe(stage)
    return () => observer.disconnect()
  }, [measure, view])

  /* --- navigation --------------------------------------------------------- */

  const scopeTo = (id: string) => {
    const next = new URLSearchParams(search)
    if (id) next.set('epic', id)
    else next.delete('epic')
    setSearch(next, { replace: true })
  }

  const toIssue = (id?: string) => {
    const query = search.toString()
    const suffix = query ? `?${query}` : ''
    navigate(
      id
        ? `/${encodeURIComponent(projectName)}/graph/issue/${encodeURIComponent(id)}${suffix}`
        : `/${encodeURIComponent(projectName)}/graph${suffix}`,
    )
  }

  // Epics for the scoping picker come from the issue list, not the graph: on a
  // 413 the graph returns no nodes at all, and that is exactly the moment the
  // user most needs somewhere to scope to.
  const { issues } = useIssues(projectName, { enabled: reachable })
  const epicOptions = useMemo(
    () =>
      epicsIn(
        issues.map((i) => ({
          id: i.id,
          title: i.title,
          status: i.status,
          priority: i.priority,
          type: i.type,
          ready: i.ready,
          labels: i.labels,
        })),
      ),
    [issues],
  )

  useShortcuts({
    'issue.close': () => (openId ? toIssue() : focus ? setFocus(null) : undefined),
    'view.backlog': () => navigate(`/${encodeURIComponent(projectName)}/backlog`),
    'view.board': () => navigate(`/${encodeURIComponent(projectName)}/board`),
    'view.now': () => navigate('/now'),
  })

  // A 413 is not a failed scope — it is the server saying "scope down", and the
  // picker is the fix, so it stays. Any other error means there is no graph to
  // describe, and a control reading "loom-g1v" over an error claims a scope the
  // data never got (loom-9st). It hides; ErrorState carries the way out.
  const scopeBroken = Boolean(graph.error) && graph.error?.status !== 413

  const controls = (
    <>
      {!scopeBroken && (
        <button
          type="button"
          className="btn btn-quiet h-[26px] text-[11.5px]"
          onClick={(e) => setEpicMenu(e.currentTarget.getBoundingClientRect())}
          title="Scope the graph to one epic's subtree"
        >
          {epic ? <span className="mono">{epic}</span> : 'Whole project'}
        </button>
      )}
      <button
        type="button"
        className="btn btn-quiet h-[26px] text-[11.5px]"
        aria-pressed={includeClosed}
        onClick={() => {
          const next = new URLSearchParams(search)
          if (includeClosed) next.delete('closed')
          else next.set('closed', '1')
          setSearch(next, { replace: true })
        }}
      >
        Closed
      </button>
    </>
  )

  const body = () => {
    if (project && !project.reachable) {
      return <UnreachableState project={project.name} reason={project.reason} workdir={project.workdir} />
    }
    if (graph.isLoading) return <BacklogSkeleton />

    // 413 — too many nodes to lay out usefully. The backend hands back the
    // count and the limit, so the message can be specific and the fix is one
    // click away rather than a shrug.
    if (graph.error?.status === 413) {
      return <TooLarge error={graph.error} epics={epicOptions} onScope={scopeTo} projectName={projectName} />
    }
    if (graph.error) {
      return (
        <ErrorState
          error={graph.error}
          onRetry={() => void graph.refetch()}
          action={
            epic && (
              <button type="button" className="btn" onClick={() => scopeTo('')}>
                Show the whole project
              </button>
            )
          }
        />
      )
    }

    if (!view || view.shown === 0) {
      return (
        <EmptyState
          title={epic ? 'Nothing blocks anything in this epic' : 'Nothing blocks anything here'}
          detail={
            <>
              {epic ? (
                <>
                  <span className="mono">{epic}</span> has{' '}
                </>
              ) : (
                <>
                  <span className="mono">{projectName}</span> has{' '}
                </>
              )}
              {view?.total ?? 0} {view?.total === 1 ? 'issue' : 'issues'} in scope and no{' '}
              <span className="mono">blocks</span> dependency between any of them. The graph
              draws blocking only — for everything else, Backlog and Initiatives carry the
              shape.
            </>
          }
          {...(epic
            ? {
                action: (
                  <button type="button" className="btn" onClick={() => scopeTo('')}>
                    Show the whole project
                  </button>
                ),
              }
            : {})}
        />
      )
    }

    return (
      <div className="relative h-full overflow-y-auto">
        <Summary view={view} epic={epic} />

        <div ref={stageRef} className="relative px-4 pt-3 pb-24">
          <svg
            className="pointer-events-none absolute top-0 left-0 z-0"
            width={geometry?.width ?? 0}
            height={geometry?.height ?? 0}
            role="img"
            aria-label={`Blocking dependencies, ${view.shown} of ${view.total} issues, ${view.edges.length} blocks edges`}
          >
            <defs>
              <marker
                id="arrow-blocks"
                viewBox="0 0 8 8"
                refX="7"
                refY="4"
                markerWidth="7"
                markerHeight="7"
                orient="auto-start-reverse"
              >
                <path d="M0 0 L8 4 L0 8 z" fill="var(--text-faint)" />
              </marker>
              <marker
                id="arrow-blocks-lit"
                viewBox="0 0 8 8"
                refX="7"
                refY="4"
                markerWidth="7"
                markerHeight="7"
                orient="auto-start-reverse"
              >
                <path d="M0 0 L8 4 L0 8 z" fill="var(--accent)" />
              </marker>
            </defs>
            {geometry &&
              view.edges.map((edge, i) => (
                <Wire
                  key={`${edge.from}-${edge.to}-${i}`}
                  edge={edge}
                  geometry={geometry}
                  cross={crossesEpic(edge, epics)}
                  lit={lit ? lit.has(edge.from) && lit.has(edge.to) : null}
                />
              ))}
          </svg>

          {view.bands.map((band) => (
            <BandRow
              key={band.level}
              band={band}
              last={band.level === view.bands.length - 1}
              cyclic={cyclicSet}
              epics={epics}
              titleOf={titleOf}
              focus={focus}
              lit={lit}
              selected={openId}
              onTrace={(id) => setFocus((current) => (current === id ? null : id))}
              onOpen={(id) => toIssue(id)}
              setCard={setCard}
            />
          ))}
        </div>

        {focus && chain && (
          <FocusBar
            id={focus}
            blockers={chain.blockers.size}
            blocked={chain.blocked.size}
            epicLabel={labelFor(epics.get(focus) ?? null, titleOf)}
            onOpen={() => toIssue(focus)}
            onClear={() => setFocus(null)}
          />
        )}
      </div>
    )
  }

  return (
    <div className="flex h-full min-w-0 flex-1 flex-col">
      <TopBar project={project} current="graph">
        {controls}
      </TopBar>

      <div className="min-h-0 flex-1">{body()}</div>

      {epicMenu && (
        <Menu
          anchor={epicMenu}
          title="Scope to an epic"
          placeholder="Filter epics…"
          items={[
            { id: '', label: 'Whole project', selected: !epic },
            ...epicOptions.map<MenuItem>((e) => ({
              id: e.id,
              label: e.title,
              hint: e.id,
              keywords: e.id,
              selected: e.id === epic,
            })),
          ]}
          onPick={(id) => scopeTo(id)}
          onClose={() => setEpicMenu(null)}
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
    </div>
  )
}

/**
 * What the view is showing and what it is not.
 *
 * The dropped issues are the majority, so leaving the count implicit would make
 * the view look like the whole project when it is a fifth of it.
 */
function Summary({ view, epic }: { view: ReturnType<typeof blockersView>; epic: string }) {
  const ready = view.bands.reduce((n, b) => n + b.nodes.filter((x) => x.ready).length, 0)
  const deps = view.edges.length
  return (
    <div className="sticky top-0 z-20 flex flex-wrap items-center gap-x-5 gap-y-1 border-b border-line bg-surface/95 px-4 py-2 text-[11.5px] backdrop-blur-[2px]">
      <p className="text-muted">
        <span className="mono text-ink">{view.shown}</span> of{' '}
        <span className="mono">{view.total}</span> issues block or are blocked
        {epic ? ' in this epic' : ''} · <span className="mono">{deps}</span>{' '}
        {deps === 1 ? 'dependency' : 'dependencies'}
      </p>
      <p style={{ color: 'var(--accent)' }}>
        <span className="mono">{ready}</span> ready
      </p>
      <p className="flex items-center gap-2 text-faint">
        <Swatch cross={false} /> within an epic
        <Swatch cross /> crosses an epic
      </p>
      <p className="ml-auto text-faint">
        {view.total - view.shown > 0 && (
          <>
            <span className="mono">{view.total - view.shown}</span> issues have no blocking
            dependency — see Backlog
          </>
        )}
      </p>
      {view.cyclic.length > 0 && (
        <p className="chip" data-tone="danger" data-outline="true" title={view.cyclic.join(', ')}>
          {view.cyclic.length} in a dependency cycle
        </p>
      )}
    </div>
  )
}

function BandRow({
  band,
  last,
  cyclic,
  epics,
  titleOf,
  focus,
  lit,
  selected,
  onTrace,
  onOpen,
  setCard,
}: {
  band: Band
  last: boolean
  cyclic: ReadonlySet<string>
  epics: Map<string, string | null>
  titleOf: Map<string, string>
  focus: string | null
  lit: Set<string> | null
  selected: string | undefined
  onTrace: (id: string) => void
  onOpen: (id: string) => void
  setCard: (id: string, el: HTMLElement | null) => void
}) {
  const cyclicHere = cyclic.size > 0 && band.nodes.every((n) => cyclic.has(n.id))
  const say = cyclicHere
    ? 'in a dependency cycle — no order exists'
    : band.level === 0
      ? 'nothing above blocks these'
      : band.level === 1
        ? 'waiting on the row above'
        : `waiting on ${band.level} rows above`

  return (
    <section className={clsx('relative z-10', !last && BAND_GAP)}>
      <div className="mb-2 flex items-baseline gap-3 border-b border-dashed border-line pb-1">
        <span className="mono text-[10px] tracking-[0.14em] text-faint uppercase">
          Level {band.level}
        </span>
        <span className="text-[11.5px] text-muted">{say}</span>
        <span className="mono ml-auto text-[11px] text-faint">
          {band.nodes.length} {band.nodes.length === 1 ? 'issue' : 'issues'}
        </span>
      </div>
      <div className="flex flex-wrap gap-3">
        {band.nodes.map((node) => (
          <BlockerCard
            key={node.id}
            node={node}
            epicLabel={labelFor(epics.get(node.id) ?? null, titleOf)}
            focused={focus === node.id}
            selected={node.id === selected}
            dimmed={lit !== null && !lit.has(node.id)}
            onTrace={() => onTrace(node.id)}
            onOpen={() => onOpen(node.id)}
            setCard={setCard}
          />
        ))}
      </div>
    </section>
  )
}

function BlockerCard({
  node,
  epicLabel,
  focused,
  selected,
  dimmed,
  onTrace,
  onOpen,
  setCard,
}: {
  node: GraphNode
  epicLabel: string
  focused: boolean
  selected: boolean
  dimmed: boolean
  onTrace: () => void
  onOpen: () => void
  setCard: (id: string, el: HTMLElement | null) => void
}) {
  return (
    <button
      type="button"
      ref={(el) => setCard(node.id, el)}
      data-status={node.status}
      data-graph-node={node.id}
      data-dimmed={dimmed ? 'true' : undefined}
      onClick={onTrace}
      onDoubleClick={(e) => {
        e.stopPropagation()
        onOpen()
      }}
      title={`${node.title} — click to trace its chain, double-click to open`}
      className={clsx(
        'flex w-[188px] cursor-pointer flex-col justify-center gap-1 rounded-md px-2.5 py-2 text-left transition-opacity duration-[--dur-fast]',
        node.external ? 'bg-sunken' : 'bg-raised',
      )}
      style={{
        // The ready frontier gets the accent ring; a traced card gets the solid
        // one. That contrast IS the view's argument.
        boxShadow:
          focused || selected
            ? 'inset 0 0 0 2px var(--accent)'
            : node.ready
              ? 'inset 0 0 0 1.5px var(--accent), 0 0 12px -2px color-mix(in oklab, var(--accent) 45%, transparent)'
              : 'inset 0 0 0 1px var(--border)',
        opacity: dimmed ? 0.22 : node.ready ? 1 : 0.86,
        borderLeft: `2px solid color-mix(in oklab, ${typeColor(node.type)} 50%, transparent)`,
      }}
    >
      <span className="flex items-center gap-1.5">
        <PriorityBadge priority={node.priority} />
        <span className="dot" data-status={node.status} data-filled={isActive(node.status)} />
        <span className="mono flex-1 truncate text-[10px] text-faint">{node.id}</span>
        {node.external && (
          <span className="chip" data-tone="neutral" data-outline="true" title="Outside this epic; pulled in by an edge">
            ext
          </span>
        )}
        <span className="text-faint">
          <TypeGlyph type={node.type} />
        </span>
      </span>
      <span className="line-clamp-2 text-[11px] leading-[1.3] text-ink">{node.title}</span>
      <span className="truncate text-[9.5px] tracking-[0.04em] text-faint uppercase">
        {epicLabel}
      </span>
    </button>
  )
}

/**
 * One blocking edge, drawn from the blocker's bottom edge to the blocked card's
 * top edge so the arrowhead lands on the work rather than on the blocker.
 *
 * Cross-epic edges are separated by dash and weight, never by colour: a project
 * carries more epics than a categorical palette can hold, and Loom reserves the
 * accent for the system talking.
 */
function Wire({
  edge,
  geometry,
  cross,
  lit,
}: {
  edge: GraphEdge
  geometry: Geometry
  cross: boolean
  lit: boolean | null
}) {
  const blocker = geometry.at.get(edge.to)
  const blocked = geometry.at.get(edge.from)
  if (!blocker || !blocked) return null

  const mid = (blocker.bottom + blocked.top) / 2
  const d = edgePath([
    { x: blocker.cx, y: blocker.bottom },
    { x: blocked.cx, y: mid },
    { x: blocked.cx, y: blocked.top },
  ])
  const on = lit === null || lit

  return (
    <path
      d={d}
      fill="none"
      stroke={lit ? 'var(--accent)' : 'var(--text-faint)'}
      strokeWidth={lit ? 1.8 : cross ? 1.4 : 1}
      strokeDasharray={cross ? undefined : '2 3'}
      opacity={lit === null ? (cross ? 0.7 : 0.5) : on ? 0.95 : 0.05}
      markerEnd={`url(#arrow-blocks${lit ? '-lit' : ''})`}
    />
  )
}

function FocusBar({
  id,
  blockers,
  blocked,
  epicLabel,
  onOpen,
  onClear,
}: {
  id: string
  blockers: number
  blocked: number
  epicLabel: string
  onOpen: () => void
  onClear: () => void
}) {
  return (
    <div
      role="status"
      data-focus-bar={id}
      className="absolute bottom-4 left-1/2 z-30 flex -translate-x-1/2 items-center gap-3 rounded-full bg-overlay px-4 py-2 text-[12px] ring-1 ring-strong shadow-lg"
    >
      <span className="mono font-semibold">{id}</span>
      <span className="text-muted">
        blocked by <span className="mono">{blockers}</span> · unblocks{' '}
        <span className="mono">{blocked}</span> · {epicLabel}
      </span>
      <button type="button" className="btn btn-quiet h-[24px] text-[11px]" onClick={onOpen}>
        Open
      </button>
      <button type="button" className="btn btn-quiet h-[24px] text-[11px]" onClick={onClear}>
        Clear
      </button>
    </div>
  )
}

function Swatch({ cross }: { cross?: boolean }) {
  return (
    <svg width="24" height="8" aria-hidden="true" className="shrink-0">
      <path
        d="M1 4 H19"
        stroke="currentColor"
        strokeWidth={cross ? 1.4 : 1}
        strokeDasharray={cross ? undefined : '2 3'}
        opacity={cross ? 0.9 : 0.55}
      />
      <path d="M18 1.5 L22 4 L18 6.5 z" fill="currentColor" opacity="0.85" />
    </svg>
  )
}

/**
 * An epic's short label. Bead titles are prefixed `[Epic name] …` by convention,
 * so the bracket is the name the author already chose; the id is the fallback so
 * the slot is never blank.
 */
function labelFor(epicId: string | null, titleOf: Map<string, string>): string {
  if (!epicId) return 'no epic'
  const title = titleOf.get(epicId)
  if (!title) return epicId
  const bracketed = title.match(/^\[([^\]]+)\]/)
  return bracketed ? bracketed[1]! : title
}

function TooLarge({
  error,
  epics,
  onScope,
  projectName,
}: {
  error: ApiError
  epics: GraphNode[]
  onScope: (id: string) => void
  projectName: string
}) {
  const [menu, setMenu] = useState<DOMRect | null>(null)
  const nodeCount = error.detailNumber('nodeCount')
  const limit = error.detailNumber('limit')
  return (
    <div className="flex h-full flex-col items-center justify-center gap-4 px-8 text-center">
      <span className="chip" data-tone="warn" data-solid="true">
        too many nodes
      </span>
      <div className="max-w-[46ch] space-y-2">
        <p className="text-[13px] font-medium text-ink">This graph is too big to read</p>
        <p className="text-[12px] leading-relaxed text-faint">
          {nodeCount !== undefined && limit !== undefined ? (
            <>
              <span className="mono text-muted">{projectName}</span> has{' '}
              <span className="mono text-muted">{nodeCount.toLocaleString()}</span> nodes in
              scope; the layout caps at{' '}
              <span className="mono text-muted">{limit.toLocaleString()}</span>.
            </>
          ) : (
            error.message
          )}{' '}
          Scope it to one epic and the blocking shape becomes legible.
        </p>
      </div>
      <button
        type="button"
        className="btn"
        onClick={(e) => setMenu(e.currentTarget.getBoundingClientRect())}
        disabled={epics.length === 0}
        title={epics.length === 0 ? 'No epics available to scope to' : undefined}
      >
        Scope to an epic…
      </button>
      {menu && (
        <Menu
          anchor={menu}
          title="Scope to an epic"
          placeholder="Filter epics…"
          items={epics.map<MenuItem>((e) => ({ id: e.id, label: e.title, hint: e.id, keywords: e.id }))}
          onPick={onScope}
          onClose={() => setMenu(null)}
        />
      )}
    </div>
  )
}
