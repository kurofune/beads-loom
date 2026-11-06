import { useState } from 'react'
import clsx from 'clsx'
import type { Criterion, Evidence } from '@/api/types'

/**
 * djinn's evidence report, from `.djinn/evidence/<bead>.yml`.
 *
 * This is the answer to "did the agent actually do it?", so it is organised
 * around the *claim* and its proof rather than around the file layout: one card
 * per acceptance criterion, its verdict, the `path:line` refs that implement
 * it, the command that verifies it, and what was observed.
 *
 * A `nonCode` criterion is a first-class case, not an anomaly — plenty of
 * acceptance criteria ("a human confirms the migration ran") cannot be
 * satisfied by code, and djinn records a reason instead of an implementation.
 * Rendering that as a failure would be wrong.
 */
const STATUS_TOKEN: Record<string, string> = {
  pass: 'closed',
  partial: 'pinned',
  fail: 'blocked',
}

const STATUS_TONE: Record<string, 'ok' | 'warn' | 'danger' | 'neutral'> = {
  pass: 'ok',
  partial: 'warn',
  fail: 'danger',
}

export function EvidenceReport({
  evidence,
  onOpenIssue,
}: {
  evidence: Evidence
  onOpenIssue: (id: string) => void
}) {
  const passed = evidence.criteria.filter((c) => c.status === 'pass').length
  const total = evidence.criteria.length
  const failed = evidence.criteria.filter((c) => c.status === 'fail').length

  return (
    <div>
      <p className="mb-2 flex flex-wrap items-center gap-2">
        <span
          className="chip"
          data-tone={failed > 0 ? 'danger' : passed === total ? 'ok' : 'warn'}
          data-solid="true"
        >
          {passed}/{total} pass
        </span>
        {failed > 0 && (
          <span className="text-[11.5px]" style={{ color: 'var(--danger)' }}>
            {failed} failing
          </span>
        )}
        <span className="mono ml-auto text-[10px] text-faint">.djinn/evidence/{evidence.bead}.yml</span>
      </p>

      {evidence.description && (
        <p className="mb-2 text-[11.5px] leading-relaxed text-faint">{evidence.description}</p>
      )}

      <ul className="space-y-1.5">
        {evidence.criteria.map((criterion, i) => (
          <CriterionCard key={i} criterion={criterion} onOpenIssue={onOpenIssue} />
        ))}
      </ul>

      {evidence.followUp.length > 0 && (
        <div className="mt-3">
          <p className="legend mb-1.5">Follow-up filed · {evidence.followUp.length}</p>
          <ul className="flex flex-wrap gap-1.5">
            {evidence.followUp.map((id) => (
              <li key={id}>
                <button type="button" className="chip" data-tone="review" onClick={() => onOpenIssue(id)}>
                  <span className="mono">{id}</span> →
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

function CriterionCard({
  criterion,
  onOpenIssue,
}: {
  criterion: Criterion
  onOpenIssue: (id: string) => void
}) {
  const [open, setOpen] = useState(criterion.status === 'fail')
  const token = STATUS_TOKEN[criterion.status] ?? 'open'
  const tone = STATUS_TONE[criterion.status] ?? 'neutral'
  const hasDetail =
    criterion.implementation.length > 0 ||
    Boolean(criterion.verification) ||
    criterion.evidence.observations.length > 0 ||
    criterion.evidence.snapshots.length > 0 ||
    Boolean(criterion.reason) ||
    (criterion.followUp?.length ?? 0) > 0

  return (
    <li className="overflow-hidden rounded-md bg-raised ring-1 ring-line">
      <button
        type="button"
        onClick={() => hasDetail && setOpen((v) => !v)}
        className={clsx(
          'flex w-full items-start gap-2 px-2.5 py-2 text-left',
          hasDetail && 'hover:bg-hover',
        )}
        aria-expanded={hasDetail ? open : undefined}
      >
        <StatusMark status={criterion.status} token={token} />
        <span className="min-w-0 flex-1">
          <span className="block text-[12px] leading-[1.45] text-ink">{criterion.text}</span>
          {criterion.nonCode && (
            <span className="chip mt-1" data-tone="neutral" data-outline="true" title="No code can satisfy this criterion">
              non-code
            </span>
          )}
        </span>
        {hasDetail && (
          <span className="shrink-0 text-[10px] text-faint">{open ? 'hide' : 'detail'}</span>
        )}
      </button>

      {open && hasDetail && (
        <div className="space-y-2 border-t border-line px-2.5 py-2">
          {criterion.reason && (
            <p className="text-[11.5px] leading-relaxed text-muted" data-tone={tone}>
              {criterion.reason}
            </p>
          )}

          {criterion.implementation.length > 0 && (
            <Block label="Implementation">
              <ul className="space-y-0.5">
                {criterion.implementation.map((ref) => (
                  <li key={ref}>
                    <Copyable value={ref} />
                  </li>
                ))}
              </ul>
            </Block>
          )}

          {criterion.verification && (
            <Block label="Verification">
              <Copyable value={criterion.verification} block />
            </Block>
          )}

          {criterion.evidence.observations.length > 0 && (
            <Block label="Observed">
              <ul className="space-y-1">
                {criterion.evidence.observations.map((o, i) => (
                  <li key={i} className="flex gap-1.5 text-[11.5px] leading-relaxed text-muted">
                    <span className="text-faint" aria-hidden="true">
                      ·
                    </span>
                    <span>{o}</span>
                  </li>
                ))}
              </ul>
            </Block>
          )}

          {criterion.evidence.snapshots.length > 0 && (
            <Block label="Snapshots">
              <ul className="flex flex-wrap gap-1">
                {criterion.evidence.snapshots.map((s) => (
                  <li key={s}>
                    <span className="chip mono" data-tone="neutral" data-outline="true">
                      {s}
                    </span>
                  </li>
                ))}
              </ul>
            </Block>
          )}

          {criterion.followUp && criterion.followUp.length > 0 && (
            <Block label="Follow-up">
              <ul className="flex flex-wrap gap-1">
                {criterion.followUp.map((id) => (
                  <li key={id}>
                    <button type="button" className="chip" data-tone="review" onClick={() => onOpenIssue(id)}>
                      <span className="mono">{id}</span> →
                    </button>
                  </li>
                ))}
              </ul>
            </Block>
          )}
        </div>
      )}
    </li>
  )
}

function Block({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <p className="legend mb-1">{label}</p>
      {children}
    </div>
  )
}

/** A `path:line` or a command. Both are things you paste into a terminal. */
function Copyable({ value, block = false }: { value: string; block?: boolean }) {
  const [copied, setCopied] = useState(false)
  return (
    <button
      type="button"
      onClick={() => {
        void navigator.clipboard?.writeText(value)
        setCopied(true)
        window.setTimeout(() => setCopied(false), 1200)
      }}
      className={clsx(
        'mono group flex w-full items-center gap-2 rounded-sm bg-sunken px-2 py-1 text-left text-[10.5px] text-muted hover:text-ink',
        block && 'whitespace-pre-wrap',
      )}
      title="Copy"
    >
      <span className="min-w-0 flex-1 truncate">{value}</span>
      <span className="shrink-0 text-[9.5px] text-faint group-hover:text-muted">
        {copied ? 'copied' : 'copy'}
      </span>
    </button>
  )
}

function StatusMark({ status, token }: { status: string; token: string }) {
  const glyph = status === 'pass' ? '✓' : status === 'fail' ? '✗' : '◐'
  return (
    <span
      className="mt-[1px] inline-flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-[10px] font-semibold"
      data-status={token}
      style={{
        color: 'var(--c)',
        background: 'color-mix(in oklab, var(--c) 16%, transparent)',
      }}
      title={status}
      aria-label={status}
    >
      {glyph}
    </span>
  )
}
