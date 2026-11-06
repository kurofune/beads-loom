import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it } from 'vitest'
import { ISSUE_LINK_SCHEME, RUN_LINK_SCHEME } from '@/lib/updates'
import { Markdown } from './Markdown'

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

describe('Markdown in-app link schemes', () => {
  it('keeps an issue link live when a handler will intercept it', () => {
    const opened: string[] = []
    const container = render(
      <Markdown onIssueLink={(id) => opened.push(id)}>
        {`[loom-d1q.1](${ISSUE_LINK_SCHEME}loom-d1q.1)`}
      </Markdown>,
    )
    const link = container.querySelector('a')!
    expect(link.getAttribute('href')).toBe(`${ISSUE_LINK_SCHEME}loom-d1q.1`)
    expect(link.getAttribute('target')).toBeNull()
    act(() => link.click())
    expect(opened).toEqual(['loom-d1q.1'])
  })

  it('keeps a run link live when a handler will intercept it', () => {
    const opened: string[] = []
    const container = render(
      <Markdown onRunLink={(id) => opened.push(id)}>
        {`[run-17](${RUN_LINK_SCHEME}run-17)`}
      </Markdown>,
    )
    const link = container.querySelector('a')!
    expect(link.getAttribute('href')).toBe(`${RUN_LINK_SCHEME}run-17`)
    act(() => link.click())
    expect(opened).toEqual(['run-17'])
  })

  it('sanitizes an in-app scheme nobody will intercept, rather than opening a tab on it', () => {
    // Bead prose can contain a literal loom-issue: link. With no handler there
    // is nothing to intercept it, so it must not survive as a live href.
    const container = render(
      <Markdown>{`[x](${ISSUE_LINK_SCHEME}loom-d1q.1) [y](${RUN_LINK_SCHEME}run-17)`}</Markdown>,
    )
    for (const link of container.querySelectorAll('a')) {
      expect(link.getAttribute('href')).toBe('')
    }
    expect(container.textContent).toContain('x')
    expect(container.textContent).toContain('y')
  })

  it('leaves an ordinary link external and out of flow', () => {
    const container = render(<Markdown>{'[docs](https://example.test/a)'}</Markdown>)
    const link = container.querySelector('a')!
    expect(link.getAttribute('href')).toBe('https://example.test/a')
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toContain('noopener')
  })
})
