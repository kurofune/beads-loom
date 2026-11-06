import type { Project } from '@/api/types'

/**
 * Marks a project whose writes Loom refuses: bd would write to a different
 * database than the one Loom reads. The tooltip is the server's reason, which
 * names both databases and the config entry to fix.
 */
export function ReadOnlyBadge({ project }: { project: Project }) {
  if (project.writable !== false) return null
  return (
    <span className="chip shrink-0 cursor-help" data-tone="warn" data-outline="true" title={project.writeReason}>
      Read-only
    </span>
  )
}

/** The reason inside a write dialog, so a disabled submit explains itself. */
export function ReadOnlyNote({ reason }: { reason: string }) {
  return (
    <div
      role="note"
      className="mb-4 rounded-md border px-3 py-2 text-[12px] leading-snug text-muted"
      style={{ borderColor: 'var(--warn)', background: 'color-mix(in oklab, var(--warn) 12%, transparent)' }}
    >
      <p className="font-semibold text-ink">This project is read-only</p>
      <p className="mt-0.5 [overflow-wrap:anywhere]">{reason}</p>
    </div>
  )
}
