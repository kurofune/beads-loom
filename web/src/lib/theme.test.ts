import { afterEach, describe, expect, it, vi } from 'vitest'
import { applyTheme, currentTheme, isThemeId, nextTheme, resolveTheme, STORAGE_KEY } from './theme'

afterEach(() => {
  localStorage.clear()
  document.documentElement.removeAttribute('data-theme')
})

describe('resolveTheme', () => {
  it('prefers a stored choice over the OS', () => {
    expect(resolveTheme('loom-light', false)).toBe('loom-light')
    expect(resolveTheme('loom-dark', true)).toBe('loom-dark')
  })

  it('follows the OS on first run', () => {
    expect(resolveTheme(null, true)).toBe('loom-light')
    expect(resolveTheme(null, false)).toBe('loom-dark')
  })

  it('ignores a stored value that is no longer a theme', () => {
    // e.g. a theme was renamed or removed between releases.
    expect(resolveTheme('loom-solarized', false)).toBe('loom-dark')
    expect(resolveTheme('', true)).toBe('loom-light')
  })
})

describe('isThemeId', () => {
  it('accepts only shipped themes', () => {
    expect(isThemeId('loom-dark')).toBe(true)
    expect(isThemeId('loom-light')).toBe(true)
    expect(isThemeId('dark')).toBe(false)
    expect(isThemeId(null)).toBe(false)
    expect(isThemeId(42)).toBe(false)
  })
})

describe('applyTheme', () => {
  it('writes the attribute the CSS selects on, and persists the choice', () => {
    applyTheme('loom-light')
    expect(document.documentElement.dataset.theme).toBe('loom-light')
    expect(localStorage.getItem(STORAGE_KEY)).toBe('loom-light')
  })

  // Spy on Storage.prototype: jsdom's Storage stores an instance spy as an item
  // named "setItem" instead of replacing the method.
  it('still applies when storage throws (Safari private mode)', () => {
    const spy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceededError')
    })
    expect(() => applyTheme('loom-dark')).not.toThrow()
    expect(spy).toHaveBeenCalled()
    expect(document.documentElement.dataset.theme).toBe('loom-dark')
    expect(localStorage.getItem(STORAGE_KEY)).toBeNull()
    spy.mockRestore()
  })
})

describe('currentTheme', () => {
  it('reads back what the pre-paint script in index.html put on <html>', () => {
    document.documentElement.dataset.theme = 'loom-light'
    expect(currentTheme()).toBe('loom-light')
  })

  it('falls back through storage when the attribute is missing or bogus', () => {
    document.documentElement.dataset.theme = 'nonsense'
    localStorage.setItem(STORAGE_KEY, 'loom-light')
    expect(currentTheme()).toBe('loom-light')
  })
})

describe('nextTheme', () => {
  it('is its own inverse', () => {
    expect(nextTheme('loom-dark')).toBe('loom-light')
    expect(nextTheme(nextTheme('loom-dark'))).toBe('loom-dark')
  })
})
