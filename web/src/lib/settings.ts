/**
 * Browser-local preferences.
 *
 * The line this module holds: Loom's *configuration* is the operator's and
 * lives in `~/.config/loom/config.yaml` — which projects exist, where their
 * workdirs are, what summoner may do. What lives here is only what is genuinely
 * this browser's preference and would be wrong to impose on anyone else opening
 * the same repository.
 *
 * Theme has its own module (`lib/theme.ts`) because it must also run before
 * paint, from an inline script in index.html.
 */
/** Where Loom reads its configuration when neither --config nor LOOM_CONFIG is set. */
export const CONFIG_PATH = '~/.config/loom/config.yaml'

const DEFAULT_PROJECT_KEY = 'loom.defaultProject'

/** Which project Loom opens on, or null to follow config.yaml's order. */
export function defaultProject(): string | null {
  try {
    return localStorage.getItem(DEFAULT_PROJECT_KEY) || null
  } catch {
    return null
  }
}

export function setDefaultProject(project: string | null): void {
  try {
    if (project) localStorage.setItem(DEFAULT_PROJECT_KEY, project)
    else localStorage.removeItem(DEFAULT_PROJECT_KEY)
  } catch {
    /* private mode: the preference simply does not persist */
  }
}

/**
 * Resolve the landing project.
 *
 * A saved preference wins, but only while that project still exists: config
 * files change between sessions, and sending someone to a project that is no
 * longer configured would be a dead end rather than a preference.
 */
export function resolveStartProject(
  available: readonly { name: string }[],
  preferred: string | null = defaultProject(),
): string | null {
  if (preferred && available.some((p) => p.name === preferred)) return preferred
  return available[0]?.name ?? null
}
