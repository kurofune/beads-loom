import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useShortcuts } from './useShortcuts'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

let root: Root | undefined

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = undefined
  document.body.replaceChildren()
})

function mount(open: () => void) {
  function Harness() {
    useShortcuts({ 'issue.open': open })
    return (
      <div>
        <button type="button">Close anyway</button>
        <a href="#x">a link</a>
        <div tabIndex={0} data-testid="plain">plain</div>
      </div>
    )
  }
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  act(() => root?.render(<Harness />))
  return container
}

const press = (target: Element, key: string) => {
  const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true })
  act(() => {
    target.dispatchEvent(event)
  })
  return event
}

describe('useShortcuts and focused controls', () => {
  it('Enter on a focused button activates the button, not a shortcut', () => {
    const open = vi.fn()
    const c = mount(open)

    const event = press(c.querySelector('button')!, 'Enter')

    expect(open).not.toHaveBeenCalled()
    expect(event.defaultPrevented).toBe(false)
  })

  it('Enter on a focused link follows the link, not a shortcut', () => {
    const open = vi.fn()
    const c = mount(open)

    press(c.querySelector('a')!, 'Enter')

    expect(open).not.toHaveBeenCalled()
  })

  it('Enter anywhere else still runs the shortcut', () => {
    const open = vi.fn()
    const c = mount(open)

    const event = press(c.querySelector('[data-testid="plain"]')!, 'Enter')

    expect(open).toHaveBeenCalledTimes(1)
    expect(event.defaultPrevented).toBe(true)
  })
})
