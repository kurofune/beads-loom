import { beforeEach, describe, expect, it } from 'vitest'
import { WriteCoordinator, writes } from './write-coordinator'

/**
 * Review finding N8: mutation A's post-write invalidation refetched the lists
 * while mutation B was still in flight, so a refetch carrying pre-B data could
 * land on top of B's optimistic patch and visibly revert it.
 */
describe('WriteCoordinator', () => {
  let c: WriteCoordinator
  beforeEach(() => {
    c = new WriteCoordinator()
  })

  it('invalidates immediately when a write is the only one in flight', () => {
    c.begin()
    expect(c.end('loom')).toEqual(['loom'])
  })

  it('holds A’s invalidation while B is still running — the regression', () => {
    c.begin() // A
    c.begin() // B
    // A settles first. Nothing may refetch yet: a refetch here would return
    // pre-B data and revert B's optimistic patch on screen.
    expect(c.end('loom')).toEqual([])
    expect(c.inflight).toBe(1)
    expect(c.deferred).toEqual(['loom'])

    // B settles. Now, and only now, the accumulated set is released.
    expect(c.end('loom')).toEqual(['loom'])
    expect(c.deferred).toEqual([])
  })

  it('coalesces repeated projects into one invalidation', () => {
    c.begin()
    c.begin()
    c.begin()
    expect(c.end('loom')).toEqual([])
    expect(c.end('loom')).toEqual([])
    // Three writes to one project produce one refetch, not three.
    expect(c.end('loom')).toEqual(['loom'])
  })

  it('releases every touched project, not just the last one', () => {
    c.begin()
    c.begin()
    expect(c.end('loom')).toEqual([])
    expect(c.end('djinn').sort()).toEqual(['djinn', 'loom'])
  })

  it('survives a long overlapping chain', () => {
    // Rapid successive edits: each new write starts before the previous settles.
    c.begin()
    for (let i = 0; i < 5; i++) {
      c.begin()
      expect(c.end('loom')).toEqual([])
    }
    expect(c.end('loom')).toEqual(['loom'])
  })

  it('does not go negative when end is called without begin', () => {
    // Defensive: a mutation path that forgets to pair must not wedge the
    // counter below zero and suppress every future invalidation.
    expect(c.end('loom')).toEqual(['loom'])
    expect(c.inflight).toBe(0)
    c.begin()
    expect(c.end('loom')).toEqual(['loom'])
  })

  it('resets cleanly', () => {
    c.begin()
    c.begin()
    c.end('loom')
    c.reset()
    expect(c.inflight).toBe(0)
    expect(c.deferred).toEqual([])
  })

  it('exports a single app-wide instance', () => {
    expect(writes).toBeInstanceOf(WriteCoordinator)
  })
})
