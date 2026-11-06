import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defaultProject, resolveStartProject, setDefaultProject } from './settings'

const projects = [{ name: 'loom' }, { name: 'djinn' }, { name: 'Project-Pheonix' }]

beforeEach(() => {
  localStorage.clear()
})

describe('defaultProject', () => {
  it('round-trips a choice', () => {
    setDefaultProject('djinn')
    expect(defaultProject()).toBe('djinn')
  })

  it('clears back to null', () => {
    setDefaultProject('djinn')
    setDefaultProject(null)
    expect(defaultProject()).toBeNull()
  })

  it('treats an empty string as no preference', () => {
    setDefaultProject('')
    expect(defaultProject()).toBeNull()
  })

  it('survives storage throwing (Safari private mode)', () => {
    const spy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceededError')
    })
    expect(() => setDefaultProject('loom')).not.toThrow()
    spy.mockRestore()
  })
})

describe('resolveStartProject', () => {
  it('honours a saved preference', () => {
    expect(resolveStartProject(projects, 'djinn')).toBe('djinn')
  })

  it('falls back to the first configured project when there is no preference', () => {
    // config.yaml's order is deliberate — the operator listed one first.
    expect(resolveStartProject(projects, null)).toBe('loom')
  })

  it('falls back when the preferred project has left the config', () => {
    // Config files change between sessions; sending someone to a project that
    // no longer exists is a dead end, not a preference.
    expect(resolveStartProject(projects, 'deleted-project')).toBe('loom')
  })

  it('returns null when there are no projects at all', () => {
    expect(resolveStartProject([], 'anything')).toBeNull()
    expect(resolveStartProject([], null)).toBeNull()
  })

  it('reads the stored preference when none is passed', () => {
    setDefaultProject('Project-Pheonix')
    expect(resolveStartProject(projects)).toBe('Project-Pheonix')
  })
})
