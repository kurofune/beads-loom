import { useCallback, useEffect, useState } from 'react'

/**
 * Recently opened issues, per project.
 *
 * The palette's most useful entry for a manager is usually the thing they were
 * just looking at — they opened it, went to check a dependency, and want back.
 * A list search cannot rank for that, because recency is not in the text.
 *
 * Session-scoped by choice (`sessionStorage`): "recent" means this sitting, not
 * this fortnight, and a week-old id resurfacing in tomorrow's palette would be
 * noise rather than help.
 *
 * The *title* is stored with the id (loom-nlm). It used to be looked up in the
 * palette's client-side issue corpus, which meant a recent bead that sorted
 * below that corpus's page silently vanished from "where I just was". The
 * title was on screen when the visit happened, so recording it there is both
 * cheaper and correct at any project size.
 */
export interface RecentIssue {
  id: string
  /** Empty for entries written before titles were recorded. */
  title: string
}

const KEY = 'loom.recent.'
const CAP = 8

const listeners = new Set<() => void>()

/** Accepts both the current `{id,title}` shape and the pre-loom-nlm id list. */
function parse(raw: unknown): RecentIssue[] {
  if (!Array.isArray(raw)) return []
  const out: RecentIssue[] = []
  for (const entry of raw) {
    if (typeof entry === 'string') {
      if (entry) out.push({ id: entry, title: '' })
      continue
    }
    if (entry && typeof entry === 'object') {
      const { id, title } = entry as { id?: unknown; title?: unknown }
      if (typeof id === 'string' && id) {
        out.push({ id, title: typeof title === 'string' ? title : '' })
      }
    }
  }
  return out
}

export function readRecentIssues(project: string): RecentIssue[] {
  if (!project) return []
  try {
    const raw = sessionStorage.getItem(KEY + project)
    return parse(raw ? (JSON.parse(raw) as unknown) : [])
  } catch {
    return []
  }
}

/** Record a visit. Most recent first, de-duplicated, capped. */
export function rememberIssue(project: string, id: string, title = ''): void {
  if (!project || !id) return
  const next = [{ id, title }, ...readRecentIssues(project).filter((v) => v.id !== id)].slice(0, CAP)
  try {
    sessionStorage.setItem(KEY + project, JSON.stringify(next))
  } catch {
    /* private mode: recency is a nicety, not a requirement */
  }
  listeners.forEach((fn) => fn())
}

export function useRecentIssues(project: string): RecentIssue[] {
  const [entries, setEntries] = useState<RecentIssue[]>(() => readRecentIssues(project))
  const refresh = useCallback(() => setEntries(readRecentIssues(project)), [project])

  useEffect(() => {
    refresh()
    listeners.add(refresh)
    return () => {
      listeners.delete(refresh)
    }
  }, [refresh])

  // Held in state rather than re-read per render, so the identity is stable
  // between visits and the palette's action memo is not rebuilt per keystroke.
  return entries
}
