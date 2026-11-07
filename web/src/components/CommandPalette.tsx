import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import clsx from 'clsx'
import { useIssueSearch } from '@/api/hooks'
import { useDebounced } from '@/lib/debounce'
import {
  describeScope,
  groupActions,
  issueActions,
  searchActions,
  type PaletteAction,
} from '@/lib/palette'
import { useRecentIssues } from '@/lib/recent'
import { useInteractionLock } from './Live'
import { isSubmitEnter } from '@/lib/enter'

/**
 * `Cmd+K`.
 *
 * v1 covers the two jumps a manager makes constantly — to another project, and
 * to a bead by id or title. Everything is fed by the action registry in
 * `lib/palette.ts`, so phase 5 extends it by appending entries rather than
 * editing this file.
 *
 * Issues are searched **server-side**, per keystroke, and only issues are: the
 * projects, views and actions are a fixed list the shell already holds. That
 * split is the point of loom-nlm. The palette used to rank a client-side array
 * built from one default page of the project, which meant a 6,030-issue
 * project was 97% unreachable from "Jump to…" with nothing on screen saying
 * so. A palette has no header to hang a truncation notice on, so the fix is
 * not a bigger sample — it is not sampling. The footer states the size of the
 * match set, which is the only number here that can honestly be bounded.
 *
 * The list is flat for keyboard purposes and grouped only for the eye: the
 * cursor walks every result in rank order regardless of which heading it sits
 * under, because "press down twice" should not depend on invisible grouping.
 */
const ROW_H = 30
const MAX_HEIGHT = 380

export function CommandPalette({
  actions,
  project,
  goIssue,
  onClose,
}: {
  /** Projects, views, actions, saved views — everything that is not an issue. */
  actions: PaletteAction[]
  /** The project whose issues the search covers. */
  project: string
  goIssue: (id: string) => void
  onClose: () => void
}) {
  const [query, setQuery] = useState('')
  const [cursor, setCursor] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)

  // The palette is an interaction: nothing should refetch under it.
  useInteractionLock(true)

  // One request per pause in typing, not one per key.
  const search = useIssueSearch(project, useDebounced(query))
  const recent = useRecentIssues(project)

  const all = useMemo(
    () => [...actions, ...issueActions({ issues: search.issues, recent, goIssue })],
    [actions, goIssue, recent, search.issues],
  )

  // Ranked against the *live* query, not the debounced one, so the static rows
  // respond to the keystroke immediately; server rows the local matcher would
  // reject are kept regardless (see `searchActions`).
  const results = useMemo(() => searchActions(query, all), [query, all])
  const grouped = useMemo(() => groupActions(results), [results])
  // Server-matched rows drawn, counted by what they are rather than by which
  // heading they landed under: a hit the recency dedup moved into Recent is
  // still one of `search.total`, and counting only the Issues group made the
  // footer say "Showing 0 of 1" with the match on screen.
  const shownIssues = results.filter((a) => a.preMatched).length

  useEffect(() => setCursor(0), [query])
  useEffect(() => inputRef.current?.focus(), [])

  useLayoutEffect(() => {
    listRef.current
      ?.querySelector<HTMLElement>(`[data-index="${cursor}"]`)
      ?.scrollIntoView({ block: 'nearest' })
  }, [cursor])

  const run = (action: PaletteAction | undefined) => {
    if (!action) return
    onClose()
    action.run()
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown' || (e.key === 'n' && e.ctrlKey)) {
      e.preventDefault()
      setCursor((c) => (results.length ? (c + 1) % results.length : 0))
    } else if (e.key === 'ArrowUp' || (e.key === 'p' && e.ctrlKey)) {
      e.preventDefault()
      setCursor((c) => (results.length ? (c - 1 + results.length) % results.length : 0))
    } else if (isSubmitEnter(e)) {
      e.preventDefault()
      run(results[cursor])
    } else if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      onClose()
    }
  }

  // Flat index across groups, so the cursor and the rendering agree.
  let flat = -1

  return (
    <div
      className="scrim-in fixed inset-0 flex items-start justify-center p-6 pt-[12vh]"
      style={{ background: 'var(--scrim)', zIndex: 'var(--z-modal)' }}
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
      role="presentation"
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        className="panel-in w-full max-w-[560px] overflow-hidden rounded-lg bg-overlay"
        style={{ boxShadow: 'var(--shadow-overlay)' }}
        onKeyDown={onKeyDown}
      >
        <div className="flex items-center gap-2 border-b border-line px-3">
          <SearchGlyph />
          <input
            ref={inputRef}
            className="h-11 flex-1 bg-transparent text-[13.5px] text-ink outline-none placeholder:text-faint"
            value={query}
            placeholder="Jump to a project or an issue…"
            onChange={(e) => setQuery(e.target.value)}
            aria-label="Search"
            role="combobox"
            aria-expanded="true"
            aria-controls="palette-results"
          />
          <kbd className="kbd">esc</kbd>
        </div>

        <div
          ref={listRef}
          id="palette-results"
          role="listbox"
          className="overflow-y-auto py-1"
          style={{ maxHeight: MAX_HEIGHT }}
        >
          {grouped.map(([group, items]) => (
            <section key={group}>
              <p className="legend px-3 pt-2 pb-1">{group}</p>
              {items.map((action) => {
                flat += 1
                const index = flat
                return (
                  <button
                    key={action.id}
                    type="button"
                    role="option"
                    aria-selected={index === cursor}
                    data-index={index}
                    onMouseEnter={() => setCursor(index)}
                    onClick={() => run(action)}
                    className={clsx(
                      'flex w-full items-center gap-3 px-3 text-left',
                      index === cursor ? 'bg-hover text-ink' : 'text-muted',
                    )}
                    style={{ height: ROW_H }}
                  >
                    <span className="min-w-0 flex-1 truncate text-[12.5px]">{action.label}</span>
                    {action.meta && (
                      <span className="shrink-0 text-[10px] uppercase tracking-wide text-faint">
                        {action.meta}
                      </span>
                    )}
                    {action.hint && (
                      <span className="mono shrink-0 text-[10.5px] text-faint">{action.hint}</span>
                    )}
                  </button>
                )
              })}
            </section>
          ))}

          {results.length === 0 &&
            !search.isSearching &&
            // A failed search has not established that nothing matches, and
            // saying so would be the confident wrong answer this bead's
            // sibling (loom-jhd) was filed about. Say what happened, and
            // offer the one control that can change it.
            (search.error ? (
              <div className="px-3 py-6 text-center text-[12px] text-faint">
                <p>Issue search failed — issue results are missing.</p>
                <button
                  type="button"
                  className="mt-2 underline underline-offset-2 hover:text-ink"
                  onClick={() => search.retry()}
                >
                  Search again
                </button>
              </div>
            ) : (
              <p className="px-3 py-6 text-center text-[12px] text-faint">
                Nothing matches <span className="mono">{query}</span>
              </p>
            ))}
        </div>

        <footer className="flex items-center gap-3 border-t border-line px-3 py-2 text-[10.5px] text-faint">
          <span>
            <kbd className="kbd">↑</kbd> <kbd className="kbd">↓</kbd> move
          </span>
          <span>
            <kbd className="kbd">↵</kbd> open
          </span>
          <span className="ml-auto text-right" data-testid="palette-scope">
            {describeScope({
              query,
              project,
              shownIssues,
              total: search.total,
              active: search.active,
              searching: search.isSearching,
              failed: Boolean(search.error),
            })}
          </span>
        </footer>
      </div>
    </div>
  )
}

function SearchGlyph() {
  return (
    <svg viewBox="0 0 12 12" width="13" height="13" aria-hidden="true" className="shrink-0 text-faint">
      <circle cx="5" cy="5" r="3.2" fill="none" stroke="currentColor" strokeWidth="1.2" />
      <path d="M7.4 7.4 10 10" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
    </svg>
  )
}
