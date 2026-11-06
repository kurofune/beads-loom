import { afterEach, describe, expect, it } from 'vitest'
import { nearestEdgeDelta, reveal, SCROLL_CONTAINER } from './reveal'

afterEach(() => document.body.replaceChildren())

const span = (start: number, end: number) => ({ start, end })

describe('nearestEdgeDelta', () => {
  const view = span(0, 100)

  it('does not move a box that already fits inside the view', () => {
    expect(nearestEdgeDelta(span(10, 40), view)).toBe(0)
    // Flush against both edges is still fully visible.
    expect(nearestEdgeDelta(span(0, 100), view)).toBe(0)
  })

  it('brings a box above the view down to the start edge, and no further', () => {
    // -30 scrolls the container up by 30, which is exactly the gap — a
    // re-centre would return -55 and move a card that only needed 30.
    expect(nearestEdgeDelta(span(-30, 10), view)).toBe(-30)
  })

  it('brings a box below the view up to the end edge, and no further', () => {
    expect(nearestEdgeDelta(span(120, 160), view)).toBe(60)
  })

  it('leaves a box that already spans the whole view alone', () => {
    // Both edges outside: any scroll would trade one hidden edge for the
    // other, so the nearest edge is neither.
    expect(nearestEdgeDelta(span(-50, 150), view)).toBe(0)
  })

  it('shows the near end of a box taller than the view', () => {
    // Above the view and taller than it: aligning the start edge would put the
    // whole box below the fold, so the end edge is the nearest one that lands
    // any of it on screen.
    expect(nearestEdgeDelta(span(-300, -50), view)).toBe(-150)
    // Below the view and taller than it: same rule mirrored.
    expect(nearestEdgeDelta(span(200, 450), view)).toBe(200)
  })
})

/** A stubbed scroll container: jsdom has no layout, so the geometry is ours. */
function container(opts: { top: number; height: number; left?: number; width?: number }) {
  const el = document.createElement('div')
  el.setAttribute('data-scroll', 'test')
  const left = opts.left ?? 0
  const width = opts.width ?? 100
  let top = 0
  let leftScroll = 0
  Object.defineProperty(el, 'scrollTop', {
    configurable: true,
    get: () => top,
    set: (v: number) => {
      top = v
    },
  })
  Object.defineProperty(el, 'scrollLeft', {
    configurable: true,
    get: () => leftScroll,
    set: (v: number) => {
      leftScroll = v
    },
  })
  el.getBoundingClientRect = () =>
    ({
      top: opts.top,
      bottom: opts.top + opts.height,
      left,
      right: left + width,
      height: opts.height,
      width,
    }) as DOMRect
  document.body.append(el)
  return el
}

/** A child whose on-screen box moves as its scroll parents scroll. */
function child(parent: HTMLElement, box: { top: number; height: number; left?: number; width?: number }) {
  const el = document.createElement('div')
  parent.append(el)
  el.getBoundingClientRect = () => {
    let dy = 0
    let dx = 0
    for (let p = el.parentElement; p; p = p.parentElement) {
      dy += p.scrollTop
      dx += p.scrollLeft
    }
    const left = (box.left ?? 0) - dx
    const width = box.width ?? 10
    return {
      top: box.top - dy,
      bottom: box.top + box.height - dy,
      left,
      right: left + width,
      height: box.height,
      width,
    } as DOMRect
  }
  return el
}

describe('reveal', () => {
  it('scrolls the marked ancestor just far enough to show the card', () => {
    const list = container({ top: 0, height: 100 })
    const card = child(list, { top: 140, height: 20 })

    reveal(card)

    expect(list.scrollTop).toBe(60)
    expect(card.getBoundingClientRect().bottom).toBe(100)
  })

  it('leaves the viewport alone when the card is already fully visible', () => {
    const list = container({ top: 0, height: 100 })
    const card = child(list, { top: 20, height: 20 })

    reveal(card)

    expect(list.scrollTop).toBe(0)
    expect(list.scrollLeft).toBe(0)
  })

  it('moves both axes when the card is off the corner', () => {
    const list = container({ top: 0, height: 100, left: 0, width: 100 })
    const card = child(list, { top: 140, height: 20, left: 160, width: 40 })

    reveal(card)

    expect(list.scrollTop).toBe(60)
    expect(list.scrollLeft).toBe(100)
  })

  it('walks every marked container between the card and the document', () => {
    // The board's shape: a column list that scrolls vertically, inside a board
    // that scrolls horizontally. A column move needs both to give.
    const board = container({ top: 0, height: 400, left: 0, width: 300 })
    const list = document.createElement('div')
    list.setAttribute('data-scroll', 'column')
    let listTop = 0
    Object.defineProperty(list, 'scrollTop', {
      configurable: true,
      get: () => listTop,
      set: (v: number) => {
        listTop = v
      },
    })
    list.getBoundingClientRect = () =>
      ({ top: 0, bottom: 400, left: 400, right: 700, height: 400, width: 300 }) as DOMRect
    board.append(list)
    const card = child(list, { top: 500, height: 20, left: 400, width: 260 })

    reveal(card)

    expect(list.scrollTop).toBe(120)
    expect(board.scrollLeft).toBe(360)
    const rect = card.getBoundingClientRect()
    expect(rect.bottom).toBeLessThanOrEqual(400)
    expect(rect.right).toBeLessThanOrEqual(300)
  })

  it('does nothing without a card', () => {
    expect(() => reveal(null)).not.toThrow()
  })

  it('names the attribute the board marks its scroll containers with', () => {
    const list = container({ top: 0, height: 100 })
    expect(list.matches(SCROLL_CONTAINER)).toBe(true)
  })
})
