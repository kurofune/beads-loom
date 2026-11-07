import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import clsx from 'clsx'
import { isSubmitEnter } from '@/lib/enter'

/**
 * The one popover in the app.
 *
 * Every inline edit — status, priority, assignee, labels — is this component
 * with a different item list, which is what keeps them behaving identically
 * whether they were opened from a keyboard shortcut on a backlog row, a click
 * in the detail sheet, or a board card's context menu.
 *
 * Keyboard-first by construction: it opens with the current value focused,
 * types-to-filter without needing to hit a search box, and commits on Enter.
 * The mouse path is the same list.
 */
export interface MenuItem {
  id: string
  label: string
  /** Optional custom rendering — a status dot, a priority badge, a chip. */
  render?: ReactNode
  /** Shown right-aligned, muted: a count, a hint, a keyboard hint. */
  hint?: string
  selected?: boolean
  /** Filtered against in addition to the label. */
  keywords?: string
}

export interface MenuProps {
  anchor: DOMRect
  items: MenuItem[]
  onPick: (id: string) => void
  onClose: () => void
  /** Stay open after a pick — labels toggle several at a time. */
  multi?: boolean
  title?: string
  /** Shown when the filter matches nothing; also enables free-text entry. */
  onCreate?: (value: string) => void
  createLabel?: (value: string) => string
  placeholder?: string
  /**
   * Set when the project is read-only: every option renders disabled with this
   * as its title, picking does nothing, and free-text entry is off.
   */
  disabledReason?: string | null
}

const ROW_H = 26
const MAX_VISIBLE = 9
const GAP = 4
const WIDTH = 236

export function Menu({
  anchor,
  items,
  onPick,
  onClose,
  multi = false,
  title,
  onCreate,
  createLabel = (v) => `Create "${v}"`,
  placeholder = 'Filter…',
  disabledReason,
}: MenuProps) {
  const disabled = Boolean(disabledReason)
  const [query, setQuery] = useState('')
  const [cursor, setCursor] = useState(0)
  const listRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return items
    return items.filter((i) => `${i.label} ${i.keywords ?? ''}`.toLowerCase().includes(q))
  }, [items, query])

  const canCreate = !disabled && Boolean(onCreate) && query.trim().length > 0 &&
    !filtered.some((i) => i.label.toLowerCase() === query.trim().toLowerCase())
  const rowCount = filtered.length + (canCreate ? 1 : 0)

  // Open on the current value, so "change status" starts where you are.
  useEffect(() => {
    const selected = items.findIndex((i) => i.selected)
    setCursor(selected >= 0 ? selected : 0)
    inputRef.current?.focus()
  }, [])

  useEffect(() => {
    setCursor((c) => Math.min(c, Math.max(rowCount - 1, 0)))
  }, [rowCount])

  useLayoutEffect(() => {
    listRef.current
      ?.querySelector<HTMLElement>(`[data-index="${cursor}"]`)
      ?.scrollIntoView({ block: 'nearest' })
  }, [cursor])

  const commit = useCallback(
    (index: number) => {
      if (disabled) return
      if (canCreate && index === filtered.length) {
        onCreate?.(query.trim())
        if (!multi) onClose()
        else setQuery('')
        return
      }
      const item = filtered[index]
      if (!item) return
      onPick(item.id)
      if (!multi) onClose()
    },
    [canCreate, disabled, filtered, multi, onClose, onCreate, onPick, query],
  )

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (isSubmitEnter(event)) {
      event.preventDefault()
      commit(cursor)
      return
    }
    switch (event.key) {
      case 'ArrowDown':
      case 'Tab':
        event.preventDefault()
        setCursor((c) => (rowCount ? (c + 1) % rowCount : 0))
        break
      case 'ArrowUp':
        event.preventDefault()
        setCursor((c) => (rowCount ? (c - 1 + rowCount) % rowCount : 0))
        break
      case 'Escape':
        event.preventDefault()
        event.stopPropagation()
        onClose()
        break
    }
  }

  // Anchor below the trigger, flipping up and clamping in from the edges.
  const height = Math.min(rowCount, MAX_VISIBLE) * ROW_H + (title ? 24 : 0) + (disabledReason ? 56 : 0) + 40
  const flip = anchor.bottom + height + GAP > window.innerHeight
  const top = flip ? Math.max(GAP, anchor.top - height - GAP) : anchor.bottom + GAP
  const left = Math.min(Math.max(GAP, anchor.left), window.innerWidth - WIDTH - GAP)

  return (
    <>
      <div className="fixed inset-0" style={{ zIndex: 'var(--z-modal)' }} onClick={onClose} role="presentation" />
      <div
        role="dialog"
        aria-label={title ?? 'Menu'}
        className="panel-in fixed overflow-hidden rounded-md bg-overlay"
        style={{ top, left, width: WIDTH, zIndex: 'var(--z-modal)', boxShadow: 'var(--shadow-overlay)' }}
        onKeyDown={onKeyDown}
      >
        {title && (
          <p className="legend border-b border-line px-2.5 py-1.5">{title}</p>
        )}

        {disabledReason && (
          <p role="note" className="border-b border-line px-2.5 py-1.5 text-[10.5px] leading-snug text-muted [overflow-wrap:anywhere]">
            <span className="font-semibold text-ink">Read-only.</span> {disabledReason}
          </p>
        )}

        <div className="border-b border-line px-1.5 py-1.5">
          <input
            ref={inputRef}
            className="field w-full"
            value={query}
            placeholder={placeholder}
            onChange={(e) => setQuery(e.target.value)}
            aria-label={placeholder}
          />
        </div>

        <div
          ref={listRef}
          role="listbox"
          className="overflow-y-auto py-1"
          style={{ maxHeight: MAX_VISIBLE * ROW_H }}
        >
          {filtered.map((item, i) => (
            <button
              key={item.id}
              type="button"
              role="option"
              aria-selected={Boolean(item.selected)}
              disabled={disabled}
              title={disabledReason ?? undefined}
              data-index={i}
              onMouseEnter={() => setCursor(i)}
              onClick={() => commit(i)}
              className={clsx(
                'flex w-full items-center gap-2 px-2.5 text-left text-[12px]',
                i === cursor ? 'bg-hover text-ink' : 'text-muted',
                disabled && 'cursor-not-allowed opacity-50',
              )}
              style={{ height: ROW_H }}
            >
              {item.render ?? <span className="truncate">{item.label}</span>}
              <span className="ml-auto flex shrink-0 items-center gap-2">
                {item.hint && <span className="mono text-[10px] text-faint">{item.hint}</span>}
                {item.selected && <Tick />}
              </span>
            </button>
          ))}

          {canCreate && (
            <button
              type="button"
              data-index={filtered.length}
              onMouseEnter={() => setCursor(filtered.length)}
              onClick={() => commit(filtered.length)}
              className={clsx(
                'flex w-full items-center gap-2 px-2.5 text-left text-[12px]',
                cursor === filtered.length ? 'bg-hover text-ink' : 'text-muted',
              )}
              style={{ height: ROW_H }}
            >
              <span className="truncate">{createLabel(query.trim())}</span>
            </button>
          )}

          {rowCount === 0 && (
            <p className="px-2.5 py-2 text-[11.5px] text-faint">No matches</p>
          )}
        </div>

        {multi && (
          <p className="border-t border-line px-2.5 py-1.5 text-[10.5px] text-faint">
            <kbd className="kbd">esc</kbd> when done
          </p>
        )}
      </div>
    </>
  )
}

function Tick() {
  return (
    <svg viewBox="0 0 10 10" width="10" height="10" aria-hidden="true" style={{ color: 'var(--accent)' }}>
      <path
        d="M1.5 5.2 3.9 7.6 8.6 2.8"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}

/**
 * Anchored-menu plumbing: what kind of menu is open, and against which rect.
 * Held by the view so a keyboard shortcut and a click open the same thing.
 */
export type MenuKind = 'status' | 'priority' | 'assignee' | 'labels'

export interface MenuRequest {
  kind: MenuKind
  anchor: DOMRect
  /** Which issues the edit applies to — one row, or the whole selection. */
  ids: string[]
}

/** A rect for keyboard-opened menus, which have no click coordinates. */
export function rectOf(el: Element | null | undefined): DOMRect {
  if (el) return el.getBoundingClientRect()
  return new DOMRect(window.innerWidth / 2 - 118, window.innerHeight / 3, 236, 0)
}
