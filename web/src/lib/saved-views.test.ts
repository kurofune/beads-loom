import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  clearViews,
  deleteView,
  describeView,
  loadViews,
  matchesView,
  saveView,
  type SavedViewInput,
} from './saved-views'

const input = (over: Partial<SavedViewInput> = {}): SavedViewInput => ({
  name: 'My P0s',
  query: 'p0',
  raw: false,
  group: 'status',
  includeClosed: false,
  staleOnly: false,
  ...over,
})

beforeEach(() => {
  localStorage.clear()
})

describe('saveView / loadViews', () => {
  it('round-trips a view', () => {
    saveView('loom', input())
    const [saved] = loadViews('loom')
    expect(saved).toMatchObject({ name: 'My P0s', query: 'p0', group: 'status' })
    expect(saved!.id).toBeTruthy()
    expect(saved!.createdAt).toBeTruthy()
  })

  it('round-trips a hierarchy lens', () => {
    saveView('loom', input({ hierarchy: 'leaf' }))
    expect(loadViews('loom')[0]!.hierarchy).toBe('leaf')
  })

  it('loads a legacy view without hierarchy', () => {
    saveView('loom', input())
    const legacy = loadViews('loom')[0]!
    expect(legacy).not.toHaveProperty('hierarchy')
    expect(matchesView(legacy, input())).toBe(true)
    expect(matchesView(legacy, input({ hierarchy: 'all' }))).toBe(true)
    expect(describeView(legacy)).toBe('filter “p0”')
  })

  it('scopes views per project', () => {
    saveView('loom', input({ name: 'mine' }))
    saveView('djinn', input({ name: 'mine', query: 'other' }))
    expect(loadViews('loom')[0]!.query).toBe('p0')
    expect(loadViews('djinn')[0]!.query).toBe('other')
  })

  it('replaces a same-named view rather than duplicating it', () => {
    // "Save" after tweaking a filter should update the view, not leave two
    // entries with one name and different contents.
    saveView('loom', input({ query: 'first' }))
    saveView('loom', input({ query: 'second' }))
    const views = loadViews('loom')
    expect(views).toHaveLength(1)
    expect(views[0]!.query).toBe('second')
  })

  it('matches names case-insensitively when replacing', () => {
    saveView('loom', input({ name: 'My P0s' }))
    saveView('loom', input({ name: 'my p0s', query: 'x' }))
    expect(loadViews('loom')).toHaveLength(1)
  })

  it('puts the newest view first', () => {
    saveView('loom', input({ name: 'older' }), 1_000)
    saveView('loom', input({ name: 'newer' }), 2_000)
    expect(loadViews('loom').map((v) => v.name)).toEqual(['newer', 'older'])
  })

  it('refuses a blank name', () => {
    saveView('loom', input({ name: '   ' }))
    expect(loadViews('loom')).toEqual([])
  })

  it('trims the name', () => {
    saveView('loom', input({ name: '  spaced  ' }))
    expect(loadViews('loom')[0]!.name).toBe('spaced')
  })

  it('returns [] for a project with nothing saved, and for no project', () => {
    expect(loadViews('nothing-here')).toEqual([])
    expect(loadViews('')).toEqual([])
  })

  it('survives a corrupt entry instead of taking the backlog down', () => {
    localStorage.setItem('loom.views.loom', 'not json at all')
    expect(loadViews('loom')).toEqual([])
    localStorage.setItem('loom.views.loom', '{"not":"an array"}')
    expect(loadViews('loom')).toEqual([])
    localStorage.setItem('loom.views.loom', '[{"garbage":true},{"id":"a","name":"b","query":""}]')
    expect(loadViews('loom')).toHaveLength(1)
  })

  it('survives storage throwing (Safari private mode)', () => {
    const spy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceededError')
    })
    expect(() => saveView('loom', input())).not.toThrow()
    spy.mockRestore()
  })
})

describe('deleteView / clearViews', () => {
  it('deletes by id and leaves the rest', () => {
    saveView('loom', input({ name: 'a' }), 1)
    saveView('loom', input({ name: 'b' }), 2)
    const target = loadViews('loom').find((v) => v.name === 'a')!
    deleteView('loom', target.id)
    expect(loadViews('loom').map((v) => v.name)).toEqual(['b'])
  })

  it('is a no-op for an unknown id', () => {
    saveView('loom', input())
    deleteView('loom', 'nope')
    expect(loadViews('loom')).toHaveLength(1)
  })

  it('clears a project without touching another', () => {
    saveView('loom', input())
    saveView('djinn', input())
    clearViews('loom')
    expect(loadViews('loom')).toEqual([])
    expect(loadViews('djinn')).toHaveLength(1)
  })
})

describe('matchesView', () => {
  it('recognises the current configuration', () => {
    saveView('loom', input())
    const view = loadViews('loom')[0]!
    expect(matchesView(view, input())).toBe(true)
  })

  it('notices any single difference', () => {
    saveView('loom', input())
    const view = loadViews('loom')[0]!
    expect(matchesView(view, input({ query: 'different' }))).toBe(false)
    expect(matchesView(view, input({ group: 'priority' }))).toBe(false)
    expect(matchesView(view, input({ includeClosed: true }))).toBe(false)
    expect(matchesView(view, input({ staleOnly: true }))).toBe(false)
    expect(matchesView(view, input({ raw: true }))).toBe(false)
  })

  it('distinguishes a top-level lens from all issues', () => {
    saveView('loom', input({ hierarchy: 'top' }))
    const view = loadViews('loom')[0]!
    expect(matchesView(view, input({ hierarchy: 'top' }))).toBe(true)
    expect(matchesView(view, input({ hierarchy: 'all' }))).toBe(false)
  })

  it('ignores the name, which is not part of the configuration', () => {
    saveView('loom', input())
    const view = loadViews('loom')[0]!
    expect(matchesView(view, input({ name: 'renamed' }))).toBe(true)
  })
})

describe('describeView', () => {
  const view = (over: Partial<SavedViewInput>) => ({
    ...input(over),
    id: '',
    createdAt: '',
  })

  it('describes a plain filter', () => {
    expect(describeView(view({ query: 'kanban' }))).toContain('filter “kanban”')
  })

  it('marks a bd query as one', () => {
    expect(describeView(view({ query: 'status=open', raw: true }))).toContain('bd query')
  })

  it('mentions grouping, closed and stale', () => {
    const text = describeView(
      view({ query: '', group: 'priority', includeClosed: true, staleOnly: true }),
    )
    expect(text).toContain('grouped by priority')
    expect(text).toContain('including closed')
    expect(text).toContain('stale only')
  })

  it('mentions hierarchy lenses only when set', () => {
    expect(describeView(view({ query: '', hierarchy: 'top' }))).toContain('top-level only')
    expect(describeView(view({ query: '', hierarchy: 'leaf' }))).toContain('leaves only')
    expect(describeView(view({ query: '' }))).toBe('the default backlog')
  })

  it('says something for the default view rather than an empty string', () => {
    expect(describeView(view({ query: '' }))).toBe('the default backlog')
  })
})
