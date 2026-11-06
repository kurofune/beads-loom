/**
 * Theme selection.
 *
 * A theme is nothing but a `data-theme` value on <html>; all colour lives in
 * src/themes/*.css. Adding a third theme means adding a CSS file and an entry
 * to THEMES — no component changes, which is the point of the token layer.
 *
 * The pre-paint copy of resolveTheme() is inlined in index.html to avoid a
 * flash of the wrong surface; keep the two in sync.
 */
export const THEMES = [
  { id: 'loom-dark', label: 'Dark' },
  { id: 'loom-light', label: 'Light' },
] as const

export type ThemeId = (typeof THEMES)[number]['id']

export const STORAGE_KEY = 'loom.theme'
export const DEFAULT_THEME: ThemeId = 'loom-dark'

export const isThemeId = (v: unknown): v is ThemeId =>
  typeof v === 'string' && THEMES.some((t) => t.id === v)

/**
 * Stored choice wins; otherwise follow the OS. Pure so it can be tested
 * without a DOM — callers pass what they read.
 */
export function resolveTheme(stored: string | null, prefersLight: boolean): ThemeId {
  if (isThemeId(stored)) return stored
  return prefersLight ? 'loom-light' : DEFAULT_THEME
}

const canUseDom = () => typeof document !== 'undefined'

function readStored(): string | null {
  try {
    return localStorage.getItem(STORAGE_KEY)
  } catch {
    // Safari in private mode throws on localStorage access.
    return null
  }
}

function prefersLight(): boolean {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-color-scheme: light)').matches
    : false
}

export function currentTheme(): ThemeId {
  if (!canUseDom()) return DEFAULT_THEME
  const attr = document.documentElement.dataset.theme
  return isThemeId(attr) ? attr : resolveTheme(readStored(), prefersLight())
}

export function applyTheme(theme: ThemeId): void {
  if (!canUseDom()) return
  document.documentElement.dataset.theme = theme
  try {
    localStorage.setItem(STORAGE_KEY, theme)
  } catch {
    /* non-fatal: the theme still applies for this session */
  }
}

export const nextTheme = (theme: ThemeId): ThemeId =>
  theme === 'loom-dark' ? 'loom-light' : 'loom-dark'
