import { describe, expect, it } from 'vitest'
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

const run = (actions: TrayAction[], from: TrayState = emptyTray) =>
  actions.reduce(trayReducer, from)

describe('trayReducer', () => {
  it('stages ids in the order they arrived', () => {
    const state = run([
      { type: 'stage', project: 'loom', ids: ['a'] },
      { type: 'stage', project: 'loom', ids: ['b', 'c'] },
    ])
    expect(state.staged).toEqual(['a', 'b', 'c'])
    expect(trayVisible(state)).toBe(true)
  })

  it('ignores duplicates and empty batches', () => {
    const state = run([
      { type: 'stage', project: 'loom', ids: ['a'] },
      { type: 'stage', project: 'loom', ids: ['a', ''] },
    ])
    expect(state.staged).toEqual(['a'])
  })

  it('hides when dismissed but keeps what was staged', () => {
    const state = run([
      { type: 'stage', project: 'loom', ids: ['a', 'b'] },
      { type: 'dismiss' },
    ])
    expect(trayVisible(state)).toBe(false)
    expect(state.staged).toEqual(['a', 'b'])
  })

  it('reappears when something new is staged after a dismiss', () => {
    const state = run([
      { type: 'stage', project: 'loom', ids: ['a'] },
      { type: 'dismiss' },
      { type: 'stage', project: 'loom', ids: ['b'] },
    ])
    expect(trayVisible(state)).toBe(true)
    expect(state.staged).toEqual(['a', 'b'])
  })

  it('stays dismissed when the same ids are staged again', () => {
    const before = run([
      { type: 'stage', project: 'loom', ids: ['a'] },
      { type: 'dismiss' },
    ])
    const after = trayReducer(before, { type: 'stage', project: 'loom', ids: ['a'] })
    expect(after).toBe(before)
    expect(trayVisible(after)).toBe(false)
  })

  it('unstages one id, and hides once the last one goes', () => {
    const state = run([
      { type: 'stage', project: 'loom', ids: ['a', 'b'] },
      { type: 'unstage', id: 'a' },
    ])
    expect(state.staged).toEqual(['b'])
    expect(trayVisible(trayReducer(state, { type: 'unstage', id: 'b' }))).toBe(false)
  })

  it('ignores unstaging something that was never staged', () => {
    const before = run([{ type: 'stage', project: 'loom', ids: ['a'] }])
    expect(trayReducer(before, { type: 'unstage', id: 'zzz' })).toBe(before)
  })

  it('replaces rather than merges when staging into a different project', () => {
    // A tray mixing two projects could not be summoned as one run.
    const state = run([
      { type: 'stage', project: 'loom', ids: ['a'] },
      { type: 'stage', project: 'djinn', ids: ['x'] },
    ])
    expect(state.staged).toEqual(['x'])
    expect(state.project).toBe('djinn')
  })

  it('empties on a project switch, and no-ops on the same project', () => {
    const staged = run([{ type: 'stage', project: 'loom', ids: ['a'] }])
    expect(trayReducer(staged, { type: 'project', project: 'loom' })).toBe(staged)
    const switched = trayReducer(staged, { type: 'project', project: 'djinn' })
    expect(switched.staged).toEqual([])
    expect(trayVisible(switched)).toBe(false)
  })

  it('restores the stored queue into an unbound tray or one on the same project', () => {
    const stored: TrayState = { staged: ['a'], dismissed: false, project: 'loom' }
    expect(trayReducer(emptyTray, { type: 'restore', stored })).toBe(stored)
    const sameProject = trayReducer(emptyTray, { type: 'project', project: 'loom' })
    expect(trayReducer(sameProject, { type: 'restore', stored })).toBe(stored)
  })

  it('keeps a tray already bound to another project, as a switch would reset the stored one', () => {
    const stored: TrayState = { staged: ['a'], dismissed: false, project: 'loom' }
    const elsewhere = trayReducer(emptyTray, { type: 'project', project: 'djinn' })
    expect(trayReducer(elsewhere, { type: 'restore', stored })).toBe(elsewhere)
  })

  it('splits closed ids from stageable ids', () => {
    const status = new Map([
      ['a', 'open'],
      ['b', 'closed'],
      ['c', 'in_progress'],
    ])
    const result = partitionStageable(['a', 'b', 'c'], (id) => status.get(id))
    expect(result.stageable).toEqual(['a', 'c'])
    expect(result.closed).toEqual(['b'])
  })

  it('treats ids with an unknown status as stageable', () => {
    const result = partitionStageable(['ghost'], () => undefined)
    expect(result.stageable).toEqual(['ghost'])
    expect(result.closed).toEqual([])
  })

  it('partitions nothing into nothing', () => {
    const result = partitionStageable([], () => 'open')
    expect(result.stageable).toEqual([])
    expect(result.closed).toEqual([])
  })

  it('clears on demand but remembers which project it belongs to', () => {
    const state = run([
      { type: 'stage', project: 'loom', ids: ['a', 'b'] },
      { type: 'clear' },
    ])
    expect(state.staged).toEqual([])
    expect(state.project).toBe('loom')
    expect(trayVisible(state)).toBe(false)
  })
})

describe('restoreTray — what a reload gets back', () => {
  it('returns the empty tray when nothing was stored', () => {
    expect(restoreTray(null)).toEqual(emptyTray)
  })

  it('round-trips a serialized tray, order intact', () => {
    const state = run([
      { type: 'stage', project: 'loom', ids: ['b', 'a', 'c'] },
      { type: 'dismiss' },
    ])
    expect(restoreTray(JSON.stringify(state))).toEqual({
      staged: ['b', 'a', 'c'],
      dismissed: true,
      project: 'loom',
    })
  })

  it('returns the empty tray for unparsable JSON', () => {
    expect(restoreTray('{not json')).toEqual(emptyTray)
  })

  it('sanitizes malformed shapes instead of trusting the store', () => {
    // Not an object at all.
    expect(restoreTray('42')).toEqual(emptyTray)
    expect(restoreTray('null')).toEqual(emptyTray)
    // staged must be unique strings; junk entries are dropped, not fatal.
    expect(
      restoreTray(
        JSON.stringify({ staged: ['a', 7, 'a', null, 'b'], dismissed: 0, project: 'loom' }),
      ),
    ).toEqual({ staged: ['a', 'b'], dismissed: false, project: 'loom' })
    // staged not an array at all: drop the queue, keep what else is valid.
    expect(restoreTray(JSON.stringify({ staged: 'a', dismissed: true, project: 'loom' }))).toEqual({
      staged: [],
      dismissed: true,
      project: 'loom',
    })
    // A queue without a project is meaningless — ids live in a project's id space.
    expect(
      restoreTray(JSON.stringify({ staged: ['a'], dismissed: false, project: null })),
    ).toEqual(emptyTray)
  })
})

describe('allStaged — which way the q key should swing', () => {
  it('is true only when every target is already in the queue', () => {
    expect(allStaged(['a', 'b'], ['a'])).toBe(true)
    expect(allStaged(['a', 'b'], ['a', 'b'])).toBe(true)
    expect(allStaged(['a', 'b'], ['a', 'c'])).toBe(false)
    expect(allStaged(['a'], ['c'])).toBe(false)
    expect(allStaged([], ['a'])).toBe(false)
  })

  it('is false for no targets, so an empty press cannot read as "unstage all"', () => {
    expect(allStaged(['a', 'b'], [])).toBe(false)
    expect(allStaged([], [])).toBe(false)
  })

  it('sends a partial selection towards staging, so q never removes what it just added', () => {
    // The alternative — unstage-if-any-staged — makes a multi-select press
    // destructive in a way the operator cannot predict from the cards.
    expect(allStaged(['a'], ['a', 'b'])).toBe(false)
  })
})
