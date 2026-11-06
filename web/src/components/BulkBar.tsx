import { useRef, type ReactNode } from 'react'
import { useWriteBlock } from '@/api/hooks'
import type { MenuKind } from './Menu'

const MENUS: [MenuKind, string, string][] = [
  ['status', 'Status', 's'],
  ['priority', 'Priority', 'p'],
  ['assignee', 'Assignee', 'a'],
  ['labels', 'Labels', 'l'],
]

/**
 * The multi-select bar.
 *
 * Floats above the content rather than pushing it, because a bulk edit is a
 * momentary mode and reflowing 400 rows every time you press `x` is violent.
 * It offers exactly the actions that have a bulk verb behind them — the rest
 * would be N round trips wearing a bulk costume.
 */
export function BulkBar({
  project,
  count,
  onOpenMenu,
  actions,
  onClose,
  onClear,
}: {
  project: string
  count: number
  onOpenMenu: (kind: MenuKind, anchor: DOMRect) => void
  /** The mounted integrations' bulk actions (djinn: Stage). */
  actions?: ReactNode
  onClose: () => void
  onClear: () => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  const writeBlock = useWriteBlock(project)

  const open = (kind: MenuKind) => (e: React.MouseEvent<HTMLButtonElement>) =>
    onOpenMenu(kind, e.currentTarget.getBoundingClientRect())

  return (
    <div
      ref={ref}
      className="panel-in pointer-events-auto fixed bottom-5 left-1/2 flex -translate-x-1/2 items-center gap-1 rounded-lg bg-overlay px-2 py-1.5"
      style={{ zIndex: 'var(--z-panel)', boxShadow: 'var(--shadow-overlay)' }}
      role="toolbar"
      aria-label={`${count} issues selected`}
    >
      <span className="mono flex items-center gap-1.5 px-2 text-[11.5px] text-ink">
        <span
          className="inline-flex h-4 min-w-4 items-center justify-center rounded-xs px-1 text-[10.5px]"
          style={{ background: 'var(--accent)', color: 'var(--accent-text)' }}
        >
          {count}
        </span>
        selected
      </span>

      <span className="mx-1 h-4 w-px bg-line" aria-hidden="true" />

      {MENUS.map(([kind, label, key]) => (
        <button
          key={kind}
          type="button"
          className="btn btn-quiet h-6 text-[11.5px]"
          disabled={Boolean(writeBlock)}
          title={writeBlock ?? undefined}
          onClick={open(kind)}
        >
          {label} <kbd className="kbd ml-1">{key}</kbd>
        </button>
      ))}

      <span className="mx-1 h-4 w-px bg-line" aria-hidden="true" />

      {actions}

      <button
        type="button"
        className="btn btn-quiet h-6 text-[11.5px]"
        disabled={Boolean(writeBlock)}
        onClick={onClose}
        title={writeBlock ?? 'Close the selected issues (reason required)'}
      >
        Close…
      </button>

      <button
        type="button"
        className="icon-btn"
        onClick={onClear}
        title="Clear selection (esc)"
        aria-label="Clear selection"
      >
        <span className="text-[11px]">esc</span>
      </button>
    </div>
  )
}
