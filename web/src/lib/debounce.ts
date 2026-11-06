import { useEffect, useState } from 'react'

/**
 * The value, one quiet pause behind.
 *
 * The command palette searches the server on every query (loom-nlm), and a
 * manager types `loom-nlm` as eight keystrokes, not one. Debouncing turns that
 * into one request per pause instead of one per key, without making the input
 * itself laggy — the field renders the live value; only the search waits.
 */
export const SEARCH_DEBOUNCE_MS = 140

export function useDebounced<T>(value: T, delay = SEARCH_DEBOUNCE_MS): T {
  const [settled, setSettled] = useState(value)

  useEffect(() => {
    if (Object.is(value, settled)) return
    const timer = setTimeout(() => setSettled(value), delay)
    return () => clearTimeout(timer)
  }, [value, settled, delay])

  return settled
}
