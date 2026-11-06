import type { HealthState, Initiative } from '@/api/types'

export const HEALTH_META = {
  'on-track': { label: 'on track', color: 'var(--accent)', tone: 'accent' },
  'at-risk': { label: 'at risk', color: 'var(--warn)', tone: 'warn' },
  'off-track': { label: 'off track', color: 'var(--danger)', tone: 'danger' },
  'no-target': { label: 'no target', color: 'var(--text-faint)', tone: 'muted' },
  'no-scope': { label: 'no scope', color: 'var(--text-faint)', tone: 'muted' },
} as const satisfies Record<HealthState, { label: string; color: string; tone: string }>

export type SortKey = 'due' | 'progress' | 'activity'
export type SortDir = 'asc' | 'desc'

export interface InitiativeSort {
  key: SortKey
  dir: SortDir
}

export const SORT_KEYS: { id: SortKey; label: string }[] = [
  { id: 'due', label: 'Due' },
  { id: 'progress', label: 'Progress' },
  { id: 'activity', label: 'Activity' },
]

export const DEFAULT_SORT: InitiativeSort = { key: 'due', dir: 'asc' }

const SORT_KEY_PREFIX = 'loom.initiatives.'

export function loadSort(project: string): InitiativeSort {
  try {
    const raw = localStorage.getItem(`${SORT_KEY_PREFIX}${project}`)
    if (!raw) return DEFAULT_SORT
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return DEFAULT_SORT
    const value = parsed as Record<string, unknown>
    if (!SORT_KEYS.some(({ id }) => id === value.key)) return DEFAULT_SORT
    if (value.dir !== 'asc' && value.dir !== 'desc') return DEFAULT_SORT
    return { key: value.key as SortKey, dir: value.dir }
  } catch {
    return DEFAULT_SORT
  }
}

export function saveSort(project: string, sort: InitiativeSort): void {
  try {
    localStorage.setItem(`${SORT_KEY_PREFIX}${project}`, JSON.stringify(sort))
  } catch {
    // Sorting remains usable for this session when storage is unavailable.
  }
}

export function progressOf(row: Initiative): number | null {
  return row.subtree.total === 0 ? null : row.subtree.closed / row.subtree.total
}

function sortValue(row: Initiative, key: SortKey): number | null {
  switch (key) {
    case 'progress':
      return progressOf(row)
    case 'activity':
      return Date.parse(row.lastActivity)
    case 'due':
      return row.due ? Date.parse(row.due) : null
  }
}

export function sortInitiatives(rows: Initiative[], sort: InitiativeSort): Initiative[] {
  return [...rows].sort((left, right) => {
    const leftValue = sortValue(left, sort.key)
    const rightValue = sortValue(right, sort.key)
    if (leftValue === null && rightValue === null) return left.id.localeCompare(right.id)
    if (leftValue === null) return 1
    if (rightValue === null) return -1
    const compared = sort.dir === 'asc' ? leftValue - rightValue : rightValue - leftValue
    return compared || left.id.localeCompare(right.id)
  })
}
