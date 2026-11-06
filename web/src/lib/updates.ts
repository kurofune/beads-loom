/**
 * Citation rendering for stored weekly updates.
 *
 * The body is markdown an agent wrote; Loom renders it and never rewrites what
 * it says. The one transformation here is presentational: a citation whose
 * target Loom can open becomes an in-app link, and one it cannot becomes mono
 * text. Everything else — including an id-shaped word like "read-only" that no
 * issue answers to — is left exactly as written.
 */

/** Href scheme for an in-app issue link. Not a real URL: the sheet intercepts it. */
export const ISSUE_LINK_SCHEME = 'loom-issue:'

/** Href scheme for an in-app run link, intercepted the same way. */
export const RUN_LINK_SCHEME = 'loom-run:'

/** A short or full git object name. */
const SHA = String.raw`\b[0-9a-f]{7,40}\b`

/** Both run id shapes Loom mints: run-<unixnano> and ext-<timestamp>. */
const RUN_ID = String.raw`\b(?:run|ext)-[A-Za-z0-9._-]+\b`

/** A fenced code block delimiter. */
const FENCE = /^\s*(?:```|~~~)/

/**
 * Splits a line into code spans and everything else. Inline code is passed
 * through verbatim, so `bd show loom-d1q.1` never becomes a link.
 */
const CODE_SPAN = /(`+)(?:[^`]|(?!\1)`)*\1/g

/** An existing markdown link, left alone so its target is not rewritten. */
const MARKDOWN_LINK = /\[[^\]]*\]\([^)]*\)/g

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/**
 * Builds the single pattern that finds every citation in one left-to-right pass.
 *
 * One pass is the point. Running the rules in sequence meant each later rule
 * scanned markdown an earlier rule had just generated — the sha rule would put
 * backticks inside a link whose id ends in hex, in both the text and the href.
 * Here every token is consumed once, so no rule ever sees another's output.
 *
 * Alternation order is precedence: a known bead id wins over a run id, which
 * wins over a sha. Group 1 is the bead id, group 2 the run id, group 3 the sha.
 *
 * Within the known-id alternation, longest first is what stops `loom-d1q`
 * matching the head of `loom-d1q.1`. The trailing `(?!\.\d)` closes the same
 * trap from the other side: when only the parent is known, the written child
 * token stays plain rather than becoming a link over half a word.
 */
function citationPattern(knownIds: string[]): RegExp {
  const ids = [...new Set(knownIds.filter(Boolean))].sort((a, b) => b.length - a.length)
  const branches: string[] = []
  // Omitted rather than left empty when nothing is known: an empty alternation
  // matches the empty string at every offset.
  if (ids.length > 0) {
    branches.push(String.raw`(?<![\w.-])(?<id>${ids.map(escapeRegExp).join('|')})(?![\w-])(?!\.\d)`)
  }
  branches.push(`(?<run>${RUN_ID})`, `(?<sha>${SHA})`)
  return new RegExp(branches.join('|'), 'g')
}

/** The three things one pass of citationPattern can find. */
type CitationGroups = { id?: string; run?: string; sha?: string }

/** Applies a transform to the parts of a line that are neither code nor a link. */
function outsideCodeAndLinks(line: string, transform: (chunk: string) => string): string {
  const protectedRanges: [number, number][] = []
  for (const pattern of [CODE_SPAN, MARKDOWN_LINK]) {
    pattern.lastIndex = 0
    for (const match of line.matchAll(pattern)) {
      protectedRanges.push([match.index, match.index + match[0].length])
    }
  }
  protectedRanges.sort((a, b) => a[0] - b[0])

  let out = ''
  let cursor = 0
  for (const [start, end] of protectedRanges) {
    if (start < cursor) continue
    out += transform(line.slice(cursor, start)) + line.slice(start, end)
    cursor = end
  }
  return out + transform(line.slice(cursor))
}

/**
 * Rewrites one body for rendering.
 *
 * A citation becomes a link when Loom has the page to open: a known bead id
 * becomes `[id](loom-issue:id)` and a known run id `[id](loom-run:id)`. A run
 * id the project has no run for, and every sha, becomes inline code. Fenced
 * blocks, inline code and existing links are untouched.
 *
 * `knownIds` and `knownRunIds` both come from the server, which resolved them
 * against the issues table and the run history. Shape alone never links.
 */
export function linkifyCitations(
  body: string,
  knownIds: string[],
  knownRunIds: string[] = [],
): string {
  const pattern = citationPattern(knownIds)
  const knownRuns = new Set(knownRunIds)
  let inFence = false

  return body
    .split('\n')
    .map((line) => {
      if (FENCE.test(line)) {
        inFence = !inFence
        return line
      }
      if (inFence) return line

      return outsideCodeAndLinks(line, (chunk) => {
        pattern.lastIndex = 0
        return chunk.replace(pattern, (...args) => {
          const { id, run, sha } = args.at(-1) as CitationGroups
          if (id) return `[${id}](${ISSUE_LINK_SCHEME}${id})`
          if (run) {
            return knownRuns.has(run) ? `[${run}](${RUN_LINK_SCHEME}${run})` : `\`${run}\``
          }
          return `\`${sha}\``
        })
      })
    })
    .join('\n')
}

/** Reads back the issue id a linkified href carries, or null for anything else. */
export function issueIdFromHref(href: string | undefined): string | null {
  return idFromHref(href, ISSUE_LINK_SCHEME)
}

/** Reads back the run id a linkified href carries, or null for anything else. */
export function runIdFromHref(href: string | undefined): string | null {
  return idFromHref(href, RUN_LINK_SCHEME)
}

function idFromHref(href: string | undefined, scheme: string): string | null {
  if (!href || !href.startsWith(scheme)) return null
  const id = href.slice(scheme.length)
  return id === '' ? null : id
}

/**
 * Chip tone for an update status. A draft is warn because it is work the
 * operator still owes a decision on; published is settled.
 */
export function latestWeekTone(status: string): 'warn' | 'accent' | 'neutral' {
  if (status === 'draft') return 'warn'
  if (status === 'published') return 'accent'
  return 'neutral'
}
