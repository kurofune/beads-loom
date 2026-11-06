import { useEffect, useRef } from 'react'
import { registeredShortcuts } from '@/integrations'
import { SHORTCUTS, type Shortcut, type ShortcutHandlers } from './registry'

/** Chord window: `g` then `l`. Long enough to be humane, short enough that a
 *  stray `g` doesn't swallow the next real keystroke. */
const CHORD_MS = 900

const TYPING = /^(input|textarea|select)$/i

/** Is the user mid-sentence in a field? Escape still gets through. */
function isTyping(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el) return false
  return TYPING.test(el.tagName) || el.isContentEditable
}

const ACTIVATABLE = 'button, a[href], [role="button"], summary'

/**
 * Is this key the browser's own activation of a focused control? Enter and
 * Space on a button or link press it; a bare-key shortcut such as Enter for
 * `issue.open` must not take that keystroke from a dialog's confirm button.
 */
function activatesControl(event: KeyboardEvent): boolean {
  if (event.key !== 'Enter' && event.key !== ' ') return false
  const el = event.target as Element | null
  return typeof el?.closest === 'function' && el.closest(ACTIVATABLE) !== null
}

/** Every binding a shortcut answers to: its primary keys plus any aliases. */
const bindings = (s: Shortcut): string[][] => [s.keys, ...(s.aliases ?? [])]

const matches = (binding: string[], seq: string[]) =>
  binding.length === seq.length && binding.every((k, i) => k === seq[i])

const isPrefixOf = (binding: string[], seq: string[]) =>
  binding.length > seq.length && seq.every((k, i) => binding[i] === k)

/**
 * Binds the registry to handlers.
 *
 * Only ids present in `handlers` are live, so a view opts into the subset it
 * can service and everything else falls through to the browser. Shortcuts are
 * matched against the registry rather than inline `key === 'j'` checks, which
 * is what keeps the cheat sheet honest.
 */
export function useShortcuts(handlers: ShortcutHandlers, enabled = true): void {
  // Kept in a ref so a re-render with new closures doesn't rebind the listener.
  const ref = useRef(handlers)
  ref.current = handlers

  useEffect(() => {
    if (!enabled) return

    // Integration bindings join the match list; one is live only while a
    // mounted slot registered a handler for it.
    const all = [...SHORTCUTS, ...registeredShortcuts()]
    let pending: string[] = []
    let timer: number | undefined

    const reset = () => {
      pending = []
      if (timer) window.clearTimeout(timer)
      timer = undefined
    }

    const onKeyDown = (event: KeyboardEvent) => {
      const mod = event.metaKey || event.ctrlKey

      // A modifier chord may only match a binding that asked for one; anything
      // else falls through to the browser.
      if (mod || event.altKey) {
        reset()
        if (!mod || event.altKey) return
        for (const shortcut of all) {
          if (!shortcut.mod || !ref.current[shortcut.id]) continue
          if (shortcut.keys.length !== 1 || shortcut.keys[0] !== event.key.toLowerCase()) continue
          event.preventDefault()
          ref.current[shortcut.id]?.(event)
          return
        }
        return
      }

      if ((isTyping(event.target) && event.key !== 'Escape') || activatesControl(event)) {
        reset()
        return
      }

      const seq = [...pending, event.key]
      // Modifier bindings are handled above and must not match a bare key.
      const live = all.filter((s) => !s.mod && ref.current[s.id])

      // An exact match wins immediately.
      for (const shortcut of live) {
        if (!bindings(shortcut).some((b) => matches(b, seq))) continue
        event.preventDefault()
        reset()
        ref.current[shortcut.id]?.(event)
        return
      }

      // Otherwise: is this a live prefix of a longer chord? If so, wait.
      const prefix = live.some((s) => bindings(s).some((b) => isPrefixOf(b, seq)))
      reset()
      if (prefix) {
        pending = seq
        timer = window.setTimeout(reset, CHORD_MS)
      }
    }

    window.addEventListener('keydown', onKeyDown)
    return () => {
      window.removeEventListener('keydown', onKeyDown)
      reset()
    }
  }, [enabled])
}
