import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useDebounced } from './debounce'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

/** Renders `useDebounced` and lets a test drive the input value. */
function mount(initial: string, delay: number) {
  const seen: string[] = []
  let set: (v: string) => void = () => undefined

  function Probe() {
    const [value, setValue] = useState(initial)
    set = setValue
    seen.push(useDebounced(value, delay))
    return null
  }

  const container = document.createElement('div')
  const root = createRoot(container)
  act(() => root.render(<Probe />))
  return {
    seen,
    set: (v: string) => act(() => set(v)),
    tick: (ms: number) => act(() => void vi.advanceTimersByTime(ms)),
    unmount: () => act(() => root.unmount()),
  }
}

describe('useDebounced', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('returns the initial value immediately', () => {
    const probe = mount('a', 100)
    expect(probe.seen.at(-1)).toBe('a')
    probe.unmount()
  })

  it('holds a new value until the delay elapses', () => {
    const probe = mount('', 100)
    probe.set('l')
    expect(probe.seen.at(-1)).toBe('')
    probe.tick(99)
    expect(probe.seen.at(-1)).toBe('')
    probe.tick(1)
    expect(probe.seen.at(-1)).toBe('l')
    probe.unmount()
  })

  it('emits only the last value of a burst — one search per pause, not per key', () => {
    const probe = mount('', 100)
    for (const v of ['l', 'lo', 'loo', 'loom']) {
      probe.set(v)
      probe.tick(40)
    }
    expect(probe.seen).not.toContain('lo')
    probe.tick(100)
    expect(probe.seen.at(-1)).toBe('loom')
    probe.unmount()
  })

  it('settles on a value the user typed back to', () => {
    const probe = mount('loom', 100)
    probe.set('loomx')
    probe.set('loom')
    probe.tick(100)
    expect(probe.seen.at(-1)).toBe('loom')
    probe.unmount()
  })
})
