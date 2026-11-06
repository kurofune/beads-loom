import { describe, expect, it } from 'vitest'
import type { GraphEdge, GraphNode, GraphResponse } from '@/api/types'
import { blockChain, blockersView, crossesEpic, edgePath, epicIndex, epicsIn } from './graph'

const node = (id: string, over: Partial<GraphNode> = {}): GraphNode => ({
  id,
  title: `Issue ${id}`,
  status: 'open',
  priority: 2,
  type: 'task',
  ready: false,
  labels: [],
  ...over,
})

/** `from` depends on `to`. For a blocks row that means `to` is the blocker. */
const edge = (from: string, to: string, type = 'blocks'): GraphEdge => ({ from, to, type })
const kid = (child: string, parent: string): GraphEdge => edge(child, parent, 'parent-child')

const graph = (nodes: GraphNode[], edges: GraphEdge[]): GraphResponse => ({ nodes, edges })

const levelOf = (view: ReturnType<typeof blockersView>, id: string) =>
  view.bands.find((b) => b.nodes.some((n) => n.id === id))?.level

describe('blockersView', () => {
  it('keeps only the issues that touch a blocks edge, and says how many it left out', () => {
    const out = blockersView(
      graph(
        [node('work'), node('blocker'), node('bystander'), node('other')],
        [edge('work', 'blocker'), kid('bystander', 'other')],
      ),
    )
    expect(out.bands.flatMap((b) => b.nodes.map((n) => n.id)).sort()).toEqual(['blocker', 'work'])
    expect(out.shown).toBe(2)
    expect(out.total).toBe(4)
  })

  it('puts the blocker in a band above the work it blocks', () => {
    const out = blockersView(graph([node('work'), node('blocker')], [edge('work', 'blocker')]))
    expect(levelOf(out, 'blocker')).toBe(0)
    expect(levelOf(out, 'work')).toBe(1)
  })

  it('takes the longest path, so a diamond puts the join below both arms', () => {
    const out = blockersView(
      graph(
        [node('top'), node('left'), node('right'), node('join')],
        [edge('left', 'top'), edge('right', 'top'), edge('join', 'left'), edge('join', 'top')],
      ),
    )
    expect(levelOf(out, 'top')).toBe(0)
    expect(levelOf(out, 'left')).toBe(1)
    expect(levelOf(out, 'join')).toBe(2)
  })

  it('places every node of a cycle and names the ones it could not resolve', () => {
    // bd does not forbid a blocks cycle. Dropping those nodes would hide the
    // very problem someone opened this view to find.
    const out = blockersView(
      graph([node('a'), node('b'), node('c')], [edge('a', 'b'), edge('b', 'c'), edge('c', 'a')]),
    )
    expect(out.bands.flatMap((b) => b.nodes.map((n) => n.id)).sort()).toEqual(['a', 'b', 'c'])
    expect(out.cyclic.length).toBeGreaterThan(0)
  })

  it('reports empty bands rather than throwing when nothing blocks anything', () => {
    const out = blockersView(graph([node('a'), node('b')], [kid('a', 'b')]))
    expect(out.bands).toEqual([])
    expect(out.shown).toBe(0)
    expect(out.total).toBe(2)
  })

  it('drops a blocks edge whose other end did not survive filtering', () => {
    const out = blockersView(graph([node('a')], [edge('a', 'ghost'), edge('ghost', 'a')]))
    expect(out.edges).toHaveLength(0)
    expect(out.shown).toBe(0)
  })

  it('ignores every dependency kind that is not blocks', () => {
    const out = blockersView(
      graph(
        [node('a'), node('b')],
        [kid('a', 'b'), edge('a', 'b', 'discovered-from'), edge('a', 'b', 'related')],
      ),
    )
    expect(out.edges).toHaveLength(0)
    expect(out.shown).toBe(0)
  })

  it('carries node data through untouched, so a card can render from a band', () => {
    const out = blockersView(
      graph(
        [node('work'), node('blocker', { ready: true, external: true, priority: 0 })],
        [edge('work', 'blocker')],
      ),
    )
    const blocker = out.bands[0]!.nodes[0]!
    expect(blocker).toMatchObject({ id: 'blocker', ready: true, external: true, priority: 0 })
  })

  it('orders a band by priority then id, so the band is scannable', () => {
    const out = blockersView(
      graph(
        [node('work'), node('b-p4', { priority: 4 }), node('a-p1', { priority: 1 }), node('c-p1', { priority: 1 })],
        [edge('work', 'b-p4'), edge('work', 'a-p1'), edge('work', 'c-p1')],
      ),
    )
    expect(out.bands[0]!.nodes.map((n) => n.id)).toEqual(['a-p1', 'c-p1', 'b-p4'])
  })

  it('numbers bands contiguously, with no gap a renderer would draw as empty', () => {
    const out = blockersView(
      graph([node('a'), node('b'), node('c')], [edge('b', 'a'), edge('c', 'b')]),
    )
    expect(out.bands.map((b) => b.level)).toEqual([0, 1, 2])
  })
})

describe('blockChain', () => {
  const edges = [
    edge('mid', 'root'),
    edge('leaf', 'mid'),
    edge('sibling', 'mid'),
    edge('elsewhere', 'unrelated'),
  ]

  it('walks transitively upward to every blocker', () => {
    expect([...blockChain('leaf', edges).blockers].sort()).toEqual(['mid', 'root'])
  })

  it('walks transitively downward to everything the issue unblocks', () => {
    expect([...blockChain('root', edges).blocked].sort()).toEqual(['leaf', 'mid', 'sibling'])
  })

  it('excludes the issue itself from both directions', () => {
    const { blockers, blocked } = blockChain('mid', edges)
    expect(blockers.has('mid')).toBe(false)
    expect(blocked.has('mid')).toBe(false)
  })

  it('terminates on a cycle instead of spinning', () => {
    const cyclic = [edge('a', 'b'), edge('b', 'c'), edge('c', 'a')]
    const { blockers, blocked } = blockChain('a', cyclic)
    expect([...blockers].sort()).toEqual(['a', 'b', 'c'].filter((id) => id !== 'a'))
    expect(blocked.size).toBe(2)
  })

  it('reads blocks edges only — a parent is not a blocker', () => {
    const { blockers } = blockChain('child', [kid('child', 'epic')])
    expect(blockers.size).toBe(0)
  })

  it('is empty for an issue with no blocks edge', () => {
    const { blockers, blocked } = blockChain('elsewhere', [edge('mid', 'root')])
    expect(blockers.size).toBe(0)
    expect(blocked.size).toBe(0)
  })
})

describe('epicIndex', () => {
  it('resolves an issue to its nearest epic ancestor', () => {
    const index = epicIndex(
      graph([node('e', { type: 'epic' }), node('f', { type: 'feature' }), node('t')], [
        kid('f', 'e'),
        kid('t', 'f'),
      ]),
    )
    expect(index.get('t')).toBe('e')
    expect(index.get('f')).toBe('e')
  })

  it('makes a nested epic its own epic, matching the Initiatives rollup', () => {
    const index = epicIndex(
      graph([node('outer', { type: 'epic' }), node('inner', { type: 'epic' }), node('t')], [
        kid('inner', 'outer'),
        kid('t', 'inner'),
      ]),
    )
    expect(index.get('inner')).toBe('inner')
    expect(index.get('t')).toBe('inner')
  })

  it('yields null for an issue with no epic above it', () => {
    const index = epicIndex(graph([node('a'), node('b')], [kid('a', 'b')]))
    expect(index.get('a')).toBeNull()
    expect(index.get('b')).toBeNull()
  })

  it('yields null rather than following a parent that is not in scope', () => {
    const index = epicIndex(graph([node('orphan')], [kid('orphan', 'missing-parent')]))
    expect(index.get('orphan')).toBeNull()
  })

  it('terminates on a parent-child cycle', () => {
    const index = epicIndex(graph([node('a'), node('b')], [kid('a', 'b'), kid('b', 'a')]))
    expect(index.get('a')).toBeNull()
    expect(index.get('b')).toBeNull()
  })

  it('covers every node in scope, so a lookup never has to guess', () => {
    const index = epicIndex(graph([node('e', { type: 'epic' }), node('lonely')], []))
    expect(index.has('lonely')).toBe(true)
    expect(index.get('e')).toBe('e')
  })
})

describe('crossesEpic', () => {
  const data = graph(
    [
      node('e1', { type: 'epic' }),
      node('e2', { type: 'epic' }),
      node('a'),
      node('b'),
      node('c'),
      node('loose'),
    ],
    [kid('a', 'e1'), kid('b', 'e1'), kid('c', 'e2')],
  )
  const index = epicIndex(data)

  it('is false for two issues under the same epic', () => {
    expect(crossesEpic(edge('a', 'b'), index)).toBe(false)
  })

  it('is true for issues under different epics', () => {
    expect(crossesEpic(edge('a', 'c'), index)).toBe(true)
  })

  it('is true when only one end has an epic', () => {
    expect(crossesEpic(edge('a', 'loose'), index)).toBe(true)
  })

  it('is false when neither end has an epic — no boundary is crossed', () => {
    expect(crossesEpic(edge('loose', 'loose2'), index)).toBe(false)
  })
})

describe('edgePath', () => {
  it('starts with a move and produces a curve per segment', () => {
    const d = edgePath([
      { x: 0, y: 0 },
      { x: 10, y: 20 },
      { x: 20, y: 40 },
    ])
    expect(d.startsWith('M 0 0')).toBe(true)
    expect(d.match(/C/g)).toHaveLength(2)
  })

  it('is empty for a degenerate polyline', () => {
    expect(edgePath([])).toBe('')
    expect(edgePath([{ x: 1, y: 1 }])).toBe('')
  })
})

describe('epicsIn', () => {
  it('lists epics in id order for the scoping picker', () => {
    const epics = epicsIn([
      node('loom-b', { type: 'epic' }),
      node('loom-a', { type: 'epic' }),
      node('loom-c', { type: 'task' }),
    ])
    expect(epics.map((e) => e.id)).toEqual(['loom-a', 'loom-b'])
  })
})
