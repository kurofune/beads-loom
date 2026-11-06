import type { TrayState } from './tray'

/**
 * Staged-for-djinn tray — the backlog's presentation of the queue the board
 * shows as a persistent rail. Only one of the two is ever mounted.
 *
 * It appears once the manager stages something explicitly; it used to appear
 * when they moved work into `open`, and loom-0n6 deleted that side effect.
 * Non-blocking by design: it never steals focus, never covers the last row, and
 * dismissing it changes nothing about the beads themselves.
 *
 * Summon opens the preview — it never dispatches. Nothing runs until the user
 * has read an ordered list and pressed a red button on a second screen. It
 * mounts only for a djinn project; when summoner cannot dispatch here, the
 * flow it opens says why.
 */
export function StagedTray({
  state,
  onDismiss,
  onClear,
  onUnstage,
  onOpenIssue,
  onSummon,
}: {
  state: TrayState
  onDismiss: () => void
  onClear: () => void
  onUnstage: (id: string) => void
  onOpenIssue: (id: string) => void
  onSummon: () => void
}) {
  const count = state.staged.length

  return (
    <div
      className="panel-in pointer-events-auto fixed right-4 bottom-4 w-[340px] overflow-hidden rounded-lg bg-overlay"
      style={{ zIndex: 'var(--z-panel)', boxShadow: 'var(--shadow-overlay)' }}
      role="region"
      aria-label="Staged for djinn"
    >
      <div className="thread flex items-center gap-2 border-b border-line px-3 py-2">
        <span
          className="inline-flex h-4 min-w-4 items-center justify-center rounded-xs px-1 text-[10.5px]"
          style={{ background: 'var(--accent)', color: 'var(--accent-text)' }}
        >
          {count}
        </span>
        <span className="text-[12px] font-medium text-ink">
          {count === 1 ? 'ticket staged' : 'tickets staged'}
        </span>
        <button
          type="button"
          className="icon-btn ml-auto"
          onClick={onDismiss}
          title="Dismiss — staging is kept until you navigate away"
          aria-label="Dismiss"
        >
          <span className="text-[11px]">–</span>
        </button>
      </div>

      <ul className="max-h-[132px] overflow-y-auto py-1">
        {state.staged.map((id) => (
          <li key={id} className="flex items-center gap-1 px-2">
            <button
              type="button"
              className="mono flex-1 truncate px-1 py-1 text-left text-[11px] text-muted hover:text-ink"
              onClick={() => onOpenIssue(id)}
              title={`Open ${id}`}
            >
              {id}
            </button>
            <button
              type="button"
              className="icon-btn h-5 w-5"
              onClick={() => onUnstage(id)}
              title={`Unstage ${id}`}
              aria-label={`Unstage ${id}`}
            >
              <span className="text-[11px]">×</span>
            </button>
          </li>
        ))}
      </ul>

      <div className="flex items-center gap-2 border-t border-line px-3 py-2">
        <button
          type="button"
          className="btn"
          onClick={onSummon}
          title="Preview what summoner would dispatch. Nothing runs until you confirm."
          style={{ background: 'var(--accent)', color: 'var(--accent-text)', boxShadow: 'none' }}
        >
          Summon
        </button>
        <button type="button" className="btn btn-quiet ml-auto h-6 text-[11.5px]" onClick={onClear}>
          Clear
        </button>
      </div>
    </div>
  )
}
