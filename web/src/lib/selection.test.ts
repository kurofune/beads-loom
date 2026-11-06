import { describe, expect, it } from 'vitest'
import { emptySelection, selectionReducer, type SelectionState } from './selection'

const order = ['a', 'b', 'c', 'd', 'e']
const ids = (s: SelectionState) => [...s.ids].sort()

const run = (actions: Parameters<typeof selectionReducer>[1][]) =>
  actions.reduce(selectionReducer, emptySelection)

describe('selectionReducer', () => {
  it('toggles on and off', () => {
    expect(ids(run([{ type: 'toggle', id: 'b' }]))).toEqual(['b'])
    expect(ids(run([{ type: 'toggle', id: 'b' }, { type: 'toggle', id: 'b' }]))).toEqual([])
  })

  it('moves the anchor to the last hand-touched row, including a deselect', () => {
    const state = run([{ type: 'toggle', id: 'b' }, { type: 'toggle', id: 'd' }])
    expect(state.anchor).toBe('d')
    const after = selectionReducer(state, { type: 'toggle', id: 'd' })
    expect(after.anchor).toBe('d')
  })

  it('selects a downward range from the anchor', () => {
    const state = run([{ type: 'toggle', id: 'b' }, { type: 'range', id: 'd', order }])
    expect(ids(state)).toEqual(['b', 'c', 'd'])
  })

  it('selects an upward range just as well', () => {
    const state = run([{ type: 'toggle', id: 'd' }, { type: 'range', id: 'b', order }])
    expect(ids(state)).toEqual(['b', 'c', 'd'])
  })

  it('keeps the anchor put so a second shift-click re-measures from the origin', () => {
    const first = run([{ type: 'toggle', id: 'b' }, { type: 'range', id: 'd', order }])
    expect(first.anchor).toBe('b')
    const second = selectionReducer(first, { type: 'range', id: 'e', order })
    expect(ids(second)).toEqual(['b', 'c', 'd', 'e'])
  })

  it('degrades a range to a toggle when the anchor is no longer visible', () => {
    // e.g. the anchor row was filtered away between the two clicks.
    const state = run([{ type: 'toggle', id: 'z' }, { type: 'range', id: 'c', order }])
    expect(ids(state)).toEqual(['c', 'z'])
  })

  it('treats a range with no anchor as a plain toggle', () => {
    expect(ids(run([{ type: 'range', id: 'c', order }]))).toEqual(['c'])
  })

  it('selects and clears everything visible', () => {
    expect(ids(run([{ type: 'all', order }]))).toEqual(['a', 'b', 'c', 'd', 'e'])
    expect(ids(run([{ type: 'all', order }, { type: 'clear' }]))).toEqual([])
  })

  it('collapses to a single row with `only`', () => {
    const state = run([{ type: 'all', order }, { type: 'only', id: 'c' }])
    expect(ids(state)).toEqual(['c'])
    expect(state.anchor).toBe('c')
  })

  describe('retain', () => {
    it('drops ids that are no longer on screen', () => {
      const state = run([{ type: 'all', order }, { type: 'retain', order: ['a', 'c'] }])
      expect(ids(state)).toEqual(['a', 'c'])
    })

    it('clears an anchor that scrolled out of the visible set', () => {
      const state = run([{ type: 'toggle', id: 'e' }, { type: 'retain', order: ['a', 'b'] }])
      expect(state.anchor).toBeNull()
    })

    it('preserves object identity when nothing changed, so React does not re-render', () => {
      const before = run([{ type: 'toggle', id: 'a' }])
      const after = selectionReducer(before, { type: 'retain', order })
      expect(after).toBe(before)
    })
  })
})
