/**
 * Subsequence fuzzy matching for the command palette.
 *
 * Deliberately not a generic fuzzy library. The corpus here is bead ids and
 * issue titles, and the query is almost always either a prefix of an id
 * (`3ft`) or a couple of words from a title (`kanban wip`). Those two shapes
 * are what the scoring rewards; everything else just has to not embarrass
 * itself.
 *
 * Scoring is deterministic and additive, so ties break the same way every time
 * and the list does not shuffle as you type.
 */
export interface FuzzyResult {
  score: number
  /** Indexes in the haystack that matched — for highlighting. */
  matches: number[]
}

const SCORE_EXACT = 1_000
const SCORE_PREFIX = 600
const SCORE_WORD_START = 80
const SCORE_CONSECUTIVE = 24
const SCORE_MATCH = 8
const PENALTY_GAP = 2
const PENALTY_LENGTH = 0.15

const isBoundary = (text: string, i: number): boolean =>
  i === 0 || /[\s\-_./:]/.test(text[i - 1]!)

/**
 * Match `query` against `text`. Returns null when the query is not a
 * subsequence of the text, which is the only hard filter.
 */
export function fuzzyMatch(query: string, text: string): FuzzyResult | null {
  const q = query.trim().toLowerCase()
  if (!q) return { score: 0, matches: [] }

  const haystack = text.toLowerCase()
  if (haystack === q) return { score: SCORE_EXACT, matches: range(q.length) }

  const matches: number[] = []
  let score = haystack.startsWith(q) ? SCORE_PREFIX : 0
  let cursor = 0
  let previous = -2

  for (const char of q) {
    const found = haystack.indexOf(char, cursor)
    if (found < 0) return null

    score += SCORE_MATCH
    if (found === previous + 1) score += SCORE_CONSECUTIVE
    if (isBoundary(haystack, found)) score += SCORE_WORD_START
    // A gap costs, but sub-linearly: matching "kanban" in a long title should
    // still beat matching two scattered letters in a short one.
    if (previous >= 0 && found > previous + 1) score -= Math.min((found - previous - 1) * PENALTY_GAP, 20)

    matches.push(found)
    previous = found
    cursor = found + 1
  }

  // Shorter haystacks win ties: "loom-3ft" should outrank "loom-3ft.4" for "3ft".
  score -= haystack.length * PENALTY_LENGTH
  return { score, matches }
}

/**
 * Rank items by the best match across several searchable fields.
 *
 * Each field carries a weight, so an id match can outrank a title match on the
 * same query without the caller having to pre-concatenate and lose the
 * distinction.
 */
export interface Searchable<T> {
  item: T
  fields: { text: string; weight: number }[]
}

export interface Ranked<T> {
  item: T
  score: number
}

export function rank<T>(query: string, items: Searchable<T>[], limit = 20): Ranked<T>[] {
  const q = query.trim()
  const scored: Ranked<T>[] = []

  for (const entry of items) {
    let best = -Infinity
    for (const field of entry.fields) {
      const result = fuzzyMatch(q, field.text)
      if (!result) continue
      best = Math.max(best, result.score * field.weight)
    }
    if (best > -Infinity) scored.push({ item: entry.item, score: best })
  }

  // Stable: equal scores keep their input order, so the list does not jitter.
  return scored
    .map((entry, i) => ({ entry, i }))
    .sort((a, b) => b.entry.score - a.entry.score || a.i - b.i)
    .slice(0, limit)
    .map(({ entry }) => entry)
}

const range = (n: number): number[] => Array.from({ length: n }, (_, i) => i)
