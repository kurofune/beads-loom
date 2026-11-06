import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it } from 'vitest'
import { ChildProgress } from './primitives'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

let root: Root | undefined

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = undefined
  document.body.replaceChildren()
})

function render(node: React.ReactNode): HTMLElement {
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  act(() => root?.render(node))
  return container
}

function segment(container: HTMLElement, name: 'closed' | 'started'): HTMLElement {
  const found = container.querySelector(`[data-segment="${name}"]`)
  if (!(found instanceof HTMLElement)) throw new Error(`${name} segment not rendered`)
  return found
}

describe('ChildProgress', () => {
  it('renders started as its own segment in a colour distinct from closed', () => {
    const container = render(<ChildProgress total={5} closed={1} started={3} />)

    const closed = segment(container, 'closed')
    const started = segment(container, 'started')

    expect(closed.style.width).toBe('20%')
    expect(started.style.width).toBe('60%')
    // The started segment carries the in-progress hue; the closed segment keeps
    // the muted accent utility class and sets no inline background.
    expect(started.style.background).toBe('var(--status-in-progress)')
    expect(closed.style.background).toBe('')
    expect(closed.className).toContain('bg-accent-muted')
    expect(started.className).not.toContain('bg-accent-muted')
  })

  it('never folds started into the closed headline ratio', () => {
    const container = render(<ChildProgress total={4} closed={1} started={3} />)

    // If started were summed into closed the ratio would read 4/4 and the
    // closed segment would fill the whole bar.
    expect(container.textContent).toContain('1/4')
    expect(container.textContent).not.toContain('4/4')
    expect(segment(container, 'closed').style.width).toBe('25%')
  })

  it('names both counts in the tooltip', () => {
    const container = render(<ChildProgress total={5} closed={2} started={3} />)
    const labelled = container.querySelector('[title]')

    expect(labelled?.getAttribute('title')).toBe('2 of 5 children closed, 3 started')
  })

  it('collapses the started segment to zero width when nothing is under way', () => {
    const container = render(<ChildProgress total={3} closed={0} started={0} />)

    expect(segment(container, 'started').style.width).toBe('0%')
    expect(segment(container, 'closed').style.width).toBe('0%')
    expect(container.textContent).toContain('0/3')
  })
})
