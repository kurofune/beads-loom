import type { GraphEdge, GraphNode, GraphResponse } from '@/api/types'

/**
 * The dependency graph's layout.
 *
 * ## Why this draws blockers and nothing else
 *
 * The view used to lay every issue out on one canvas with a Sugiyama layout, and
 * it was unreadable for a measurable reason rather than a matter of taste. On
 * Project-Pheonix, 166 of 225 edges are `parent-child` and only 43 of 207 issues
 * touch a `blocks` edge — so the layered DAG was spending all its effort drawing
 * a tree, and one epic with 67 children became a single row 15,408 px wide. The
 * other 79% of nodes were on the canvas only because they belong to an epic,
 * where a graph says nothing a list does not.
 *
 * So the graph answers one question: what is blocked, and by what. Hierarchy is
 * not drawn at all — the epic rides on the card as a label, and the per-epic
 * rollups live on Initiatives, which already computes them.
 *
 * 27 of those 38 `blocks` edges cross an epic boundary, which is why this is a
 * global layout rather than chains nested inside epic containers: an epic box
 * cannot hold the structure.
 *
 * ## Why there is no layout engine here any more
 *
 * Bands are laid out by the browser: each band is a wrapping row, so a band too
 * wide for the pane wraps instead of extending the canvas. That is the actual
 * fix for the wide-row problem, and it needs no coordinate solver — which is why
 * `d3-dag` and the pan/zoom canvas are gone with it. Everything below is pure:
 * given nodes and edges it returns bands, chains and lookups, and the view turns
 * those into DOM.
 */

/** One layer of the blocks DAG. Level 0 is the top: nothing above blocks it. */
export interface Band {
  level: number
  nodes: GraphNode[]
}

export interface BlockersView {
  bands: Band[]
  /** The `blocks` edges between issues that survived filtering. */
  edges: GraphEdge[]
  /** Issues drawn. */
  shown: number
  /** Issues the response carried, drawn or not — the view states both. */
  total: number
  /** Ids a `blocks` cycle left unresolvable. Placed, never dropped. */
  cyclic: string[]
}

const BLOCKS = 'blocks'
const PARENT = 'parent-child'

/**
 * Build the blockers view from a graph response.
 *
 * An issue earns a card by touching a `blocks` edge; everything else is counted
 * and reported rather than drawn, because a view that silently shows a fifth of
 * the backlog is worse than one that says which fifth.
 */
export function blockersView(data: GraphResponse): BlockersView {
  const present = new Set(data.nodes.map((n) => n.id))
  // An edge to an issue that did not survive filtering — a closed one, or an
  // infra bead — would be an arrow to nowhere.
  const edges = data.edges.filter(
    (e) => e.type === BLOCKS && present.has(e.from) && present.has(e.to),
  )

  const involved = new Set<string>()
  for (const e of edges) {
    involved.add(e.from)
    involved.add(e.to)
  }

  const nodes = data.nodes.filter((n) => involved.has(n.id))
  const { level, cyclic } = levels(involved, edges)

  const byLevel = new Map<number, GraphNode[]>()
  for (const n of nodes) {
    const l = level.get(n.id) ?? 0
    const list = byLevel.get(l)
    if (list) list.push(n)
    else byLevel.set(l, [n])
  }

  const bands = [...byLevel.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([l, group]) => ({
      level: l,
      // Priority first, then id: a band is scanned, so the urgent end reads first.
      nodes: group.sort((a, b) => a.priority - b.priority || a.id.localeCompare(b.id)),
    }))

  return { bands, edges, shown: nodes.length, total: data.nodes.length, cyclic }
}

/**
 * Longest-path levels with the blocker above the work it blocks.
 *
 * A `blocks` row means `from` is blocked BY `to`, so the layout edge runs
 * `to` → `from` and a node sits one level below its deepest blocker. Levels come
 * out contiguous by construction: a node at level k has a blocker at k-1.
 */
function levels(
  ids: ReadonlySet<string>,
  edges: readonly GraphEdge[],
): { level: Map<string, number>; cyclic: string[] } {
  const down = new Map<string, string[]>()
  const remaining = new Map<string, number>()
  for (const id of ids) {
    down.set(id, [])
    remaining.set(id, 0)
  }
  for (const e of edges) {
    down.get(e.to)!.push(e.from)
    remaining.set(e.from, remaining.get(e.from)! + 1)
  }

  const level = new Map<string, number>([...ids].map((id) => [id, 0]))
  const queue = [...ids].filter((id) => remaining.get(id) === 0)
  for (let i = 0; i < queue.length; i++) {
    const at = queue[i]!
    for (const next of down.get(at)!) {
      level.set(next, Math.max(level.get(next)!, level.get(at)! + 1))
      remaining.set(next, remaining.get(next)! - 1)
      if (remaining.get(next) === 0) queue.push(next)
    }
  }

  // A blocks cycle leaves its members with an unsatisfied in-degree forever.
  // They land one level below the deepest node the walk did settle, so the cycle
  // is visible at the bottom of the graph instead of missing from it.
  const cyclic = [...ids].filter((id) => remaining.get(id)! > 0)
  if (cyclic.length > 0) {
    const settled = [...ids].filter((id) => remaining.get(id) === 0)
    const deepest = settled.length > 0 ? Math.max(...settled.map((id) => level.get(id)!)) : -1
    for (const id of cyclic) level.set(id, deepest + 1)
  }

  return { level, cyclic }
}

/**
 * Everything that blocks an issue, and everything it unblocks, transitively.
 *
 * This is the view's payoff: it turns "where does the work open up if I unblock
 * this one thing" into a highlight. Both walks are visited-guarded, because bd
 * does not forbid a cycle.
 */
export function blockChain(
  id: string,
  edges: readonly GraphEdge[],
): { blockers: Set<string>; blocked: Set<string> } {
  const up = new Map<string, string[]>()
  const down = new Map<string, string[]>()
  for (const e of edges) {
    if (e.type !== BLOCKS) continue
    push(up, e.from, e.to)
    push(down, e.to, e.from)
  }

  const walk = (adjacency: Map<string, string[]>) => {
    const found = new Set<string>()
    const stack = [id]
    while (stack.length > 0) {
      for (const next of adjacency.get(stack.pop()!) ?? []) {
        if (found.has(next)) continue
        found.add(next)
        stack.push(next)
      }
    }
    // A cycle through the start node would otherwise report the issue as its own
    // blocker, which reads as a rendering bug rather than as the cycle it is.
    found.delete(id)
    return found
  }

  return { blockers: walk(up), blocked: walk(down) }
}

function push(map: Map<string, string[]>, key: string, value: string): void {
  const list = map.get(key)
  if (list) list.push(value)
  else map.set(key, [value])
}

/**
 * Each issue's nearest epic ancestor, or null.
 *
 * A `parent-child` row is stored child-first, so walking up means following
 * `from` → `to`. A nested epic is its own epic, which is how the Initiatives
 * rollup already treats it (`internal/queries/initiatives.go`). Every id in the
 * response gets an entry, so a lookup never has to distinguish "no epic" from
 * "not indexed".
 */
export function epicIndex(data: GraphResponse): Map<string, string | null> {
  const kind = new Map(data.nodes.map((n) => [n.id, n.type]))
  const parent = new Map<string, string>()
  for (const e of data.edges) {
    if (e.type === PARENT && kind.has(e.from) && kind.has(e.to)) parent.set(e.from, e.to)
  }

  const index = new Map<string, string | null>()
  for (const node of data.nodes) {
    const path: string[] = []
    const seen = new Set<string>()
    let at: string | undefined = node.id
    let found: string | null = null
    while (at !== undefined && !seen.has(at)) {
      seen.add(at)
      path.push(at)
      const cached = index.get(at)
      if (cached !== undefined) {
        found = cached
        break
      }
      if (kind.get(at) === 'epic') {
        found = at
        break
      }
      at = parent.get(at)
    }
    // Memoize the whole path, not just the node asked for: on a deep tree this
    // is what keeps the index linear.
    for (const id of path) if (!index.has(id)) index.set(id, found)
  }
  return index
}

/**
 * Whether an edge spans two epics.
 *
 * The distinction earns its own rendering: on Project-Pheonix 27 of 38 `blocks`
 * edges cross an epic, which is the fact that rules out drawing epics as
 * containers. Two issues with no epic at all cross no boundary.
 */
export function crossesEpic(edge: GraphEdge, epics: Map<string, string | null>): boolean {
  return (epics.get(edge.from) ?? null) !== (epics.get(edge.to) ?? null)
}

/* --- drawing -------------------------------------------------------------- */

/**
 * An SVG path through a polyline, with a light curve at the bends.
 *
 * There is no per-kind dash table here any more. Only `blocks` reaches the
 * canvas, so the one distinction the drawing makes is cross-epic vs not, and
 * the detail sheet styles its own dependency lists inline
 * (`views/IssueDetail.tsx`) rather than sharing a mapping with the graph.
 */
export function edgePath(points: readonly { x: number; y: number }[]): string {
  if (points.length < 2) return ''
  const [first, ...rest] = points
  let d = `M ${first!.x} ${first!.y}`
  for (let i = 0; i < rest.length; i++) {
    const p = rest[i]!
    const prev = i === 0 ? first! : rest[i - 1]!
    const midY = (prev.y + p.y) / 2
    d += ` C ${prev.x} ${midY}, ${p.x} ${midY}, ${p.x} ${p.y}`
  }
  return d
}

/**
 * Epics, for the scoping dropdown.
 *
 * Readiness has no helper: the card reads `node.ready` directly, which is the
 * same rule as everywhere else in Loom — the field, never the status string.
 */
export function epicsIn(nodes: readonly GraphNode[]): GraphNode[] {
  return nodes.filter((n) => n.type === 'epic').sort((a, b) => a.id.localeCompare(b.id))
}
