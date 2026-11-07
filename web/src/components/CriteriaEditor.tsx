import { useLayoutEffect, useRef, useState } from 'react'
import { isSubmitEnter } from '@/lib/enter'

/**
 * One criterion per line, enforced structurally.
 *
 * Enter adds the next line, Backspace on an empty line removes it, and the
 * arrows reorder — the same muscle memory as any list editor, so nobody has to
 * learn that blank lines are meaningful here.
 */
export function CriteriaEditor({
  value,
  onChange,
  autoFocus = false,
}: {
  value: string[]
  onChange: (next: string[]) => void
  autoFocus?: boolean
}) {
  const refs = useRef<(HTMLInputElement | null)[]>([])
  // Which line should hold focus after the next commit.
  //
  // Deliberately NOT requestAnimationFrame: rAF runs a frame later, and
  // anything typed in between — a fast typist, a paste, an automated test —
  // lands in the line the user just left. A layout effect runs synchronously
  // after React commits and before the browser handles the next input event,
  // so no keystroke can slip through the gap.
  const [focusLine, setFocusLine] = useState<number | null>(autoFocus ? 0 : null)

  useLayoutEffect(() => {
    if (focusLine === null) return
    refs.current[focusLine]?.focus()
    setFocusLine(null)
  }, [focusLine])

  const setAt = (i: number, text: string) => onChange(value.map((v, j) => (j === i ? text : v)))

  const insertAfter = (i: number) => {
    const next = [...value]
    next.splice(i + 1, 0, '')
    onChange(next)
    setFocusLine(i + 1)
  }

  const removeAt = (i: number) => {
    if (value.length === 1) return onChange([''])
    onChange(value.filter((_, j) => j !== i))
    setFocusLine(Math.max(0, i - 1))
  }

  const move = (i: number, delta: number) => {
    const j = i + delta
    if (j < 0 || j >= value.length) return
    const next = [...value]
    ;[next[i], next[j]] = [next[j]!, next[i]!]
    onChange(next)
    setFocusLine(j)
  }

  return (
    <div>
      <span className="legend mb-1.5 block">Acceptance criteria</span>
      <ul className="space-y-1">
        {value.map((line, i) => (
          <li key={i} className="flex items-center gap-2">
            <span
              className="h-3 w-3 shrink-0 rounded-full border border-line-strong"
              aria-hidden="true"
            />
            <input
              ref={(el) => {
                refs.current[i] = el
              }}
              className="field flex-1"
              value={line}
              placeholder={i === 0 ? 'One criterion per line…' : ''}
              onChange={(e) => setAt(i, e.target.value)}
              aria-label={`Criterion ${i + 1}`}
              onKeyDown={(e) => {
                if (isSubmitEnter(e) && !e.metaKey && !e.ctrlKey) {
                  e.preventDefault()
                  insertAfter(i)
                } else if (e.key === 'Backspace' && line === '' && value.length > 1) {
                  e.preventDefault()
                  removeAt(i)
                } else if (e.key === 'ArrowUp' && e.altKey) {
                  e.preventDefault()
                  move(i, -1)
                } else if (e.key === 'ArrowDown' && e.altKey) {
                  e.preventDefault()
                  move(i, 1)
                }
              }}
            />
            <button
              type="button"
              className="icon-btn h-5 w-5"
              onClick={() => removeAt(i)}
              title="Remove criterion"
              aria-label={`Remove criterion ${i + 1}`}
            >
              <span className="text-[11px]">×</span>
            </button>
          </li>
        ))}
      </ul>
      <p className="mt-1.5 text-[11px] leading-relaxed text-faint">
        bd stores these as one blob; the convention is{' '}
        <span className="text-muted">one criterion per non-blank line</span>, which is what
        makes the detail panel render them as a checklist.{' '}
        <kbd className="kbd">↵</kbd> adds a line, <kbd className="kbd">⌥</kbd>
        <kbd className="kbd">↑</kbd>/<kbd className="kbd">↓</kbd> reorders.
      </p>
    </div>
  )
}
