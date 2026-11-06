import { beforeEach, describe, expect, it, vi } from 'vitest'
import { applyProjectOrder, moveProject, projectOrder, setProjectOrder } from './project-order'

const p = (...names: string[]) => names.map((name) => ({ name }))
const names = (list: { name: string }[]) => list.map((x) => x.name)

beforeEach(() => {
  localStorage.clear()
})

describe('applyProjectOrder', () => {
  it('keeps config order when nothing is saved', () => {
    expect(names(applyProjectOrder(p('loom', 'djinn', 'the-pit'), []))).toEqual(['loom', 'djinn', 'the-pit'])
  })

  it('puts saved projects first, in saved order', () => {
    expect(names(applyProjectOrder(p('loom', 'djinn', 'the-pit'), ['the-pit', 'loom', 'djinn']))).toEqual([
      'the-pit',
      'loom',
      'djinn',
    ])
  })

  it('appends projects the saved order has never seen, in config order', () => {
    expect(names(applyProjectOrder(p('loom', 'new-a', 'djinn', 'new-b'), ['djinn', 'loom']))).toEqual([
      'djinn',
      'loom',
      'new-a',
      'new-b',
    ])
  })

  it('drops saved names that are no longer configured', () => {
    expect(names(applyProjectOrder(p('loom', 'djinn'), ['gone', 'djinn', 'loom']))).toEqual(['djinn', 'loom'])
  })

  it('returns the same objects, not copies', () => {
    const list = p('loom', 'djinn')
    expect(applyProjectOrder(list, ['djinn'])[0]).toBe(list[1])
  })
})

describe('moveProject', () => {
  it('moves the dragged project to the drop target position', () => {
    expect(moveProject(['loom', 'djinn', 'claudit', 'the-pit'], 'the-pit', 'loom')).toEqual([
      'the-pit',
      'loom',
      'djinn',
      'claudit',
    ])
  })

  it('moves down as well as up', () => {
    expect(moveProject(['loom', 'djinn', 'the-pit'], 'loom', 'the-pit')).toEqual(['djinn', 'the-pit', 'loom'])
  })

  it('is a no-op when either name is unknown or they are the same', () => {
    const order = ['loom', 'djinn']
    expect(moveProject(order, 'loom', 'loom')).toEqual(order)
    expect(moveProject(order, 'gone', 'loom')).toEqual(order)
    expect(moveProject(order, 'loom', 'gone')).toEqual(order)
  })
})

describe('projectOrder storage', () => {
  it('round-trips a saved order', () => {
    setProjectOrder(['the-pit', 'loom'])
    expect(projectOrder()).toEqual(['the-pit', 'loom'])
  })

  it('reads corrupt or non-array storage as no order', () => {
    localStorage.setItem('loom.projectOrder', '{not json')
    expect(projectOrder()).toEqual([])
    localStorage.setItem('loom.projectOrder', '{"a":1}')
    expect(projectOrder()).toEqual([])
    localStorage.setItem('loom.projectOrder', '["loom", 3]')
    expect(projectOrder()).toEqual(['loom'])
  })

  // Spy on Storage.prototype, not on the localStorage instance: jsdom's Storage
  // turns a property defined on the instance into a stored item named
  // "setItem", so an instance spy never intercepts the call.
  it('survives storage throwing (Safari private mode)', () => {
    const set = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceededError')
    })
    const get = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('SecurityError')
    })
    expect(() => setProjectOrder(['loom'])).not.toThrow()
    expect(set).toHaveBeenCalled()
    expect(projectOrder()).toEqual([])
    expect(get).toHaveBeenCalled()
  })
})
