/**
 * The sidebar's project order — a browser-local preference, like the default
 * project in `lib/settings.ts`. config.yaml's order is the fallback and is
 * never written: a project the saved order has not seen yet lands after the
 * saved ones in config order, and a saved name no longer configured drops out.
 */
const KEY = 'loom.projectOrder'

export function projectOrder(): string[] {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(KEY) ?? '[]')
    return Array.isArray(parsed) ? parsed.filter((n): n is string => typeof n === 'string') : []
  } catch {
    return []
  }
}

export function setProjectOrder(order: readonly string[]): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(order))
  } catch {
    /* private mode: the order simply does not persist */
  }
}

export function applyProjectOrder<T extends { name: string }>(projects: readonly T[], order: readonly string[]): T[] {
  const rank = new Map(order.map((name, i) => [name, i]))
  const saved = projects.filter((p) => rank.has(p.name)).sort((a, b) => rank.get(a.name)! - rank.get(b.name)!)
  return [...saved, ...projects.filter((p) => !rank.has(p.name))]
}

/** Move `from` into `to`'s slot, shifting the rest — a sortable-list drop. */
export function moveProject(order: readonly string[], from: string, to: string): string[] {
  const i = order.indexOf(from)
  const j = order.indexOf(to)
  if (i < 0 || j < 0 || i === j) return [...order]
  const next = [...order]
  next.splice(i, 1)
  next.splice(j, 0, from)
  return next
}
