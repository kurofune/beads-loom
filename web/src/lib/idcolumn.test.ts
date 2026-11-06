import { describe, expect, it } from 'vitest'

import { ID_COL_MAX, ID_COL_MIN, idColumnChars } from './idcolumn'

describe('idColumnChars', () => {
  it('gives a project of short ids a narrower column than one of long ids', () => {
    // The point of measuring at all: `loom-7qh` must not reserve the room
    // `djinn-8ncc0.2.2` needs, and `djinn-8ncc0.2.2` must not be cut to fit
    // a column sized for `loom-7qh`.
    const short = idColumnChars(['loom-7qh', 'loom-a7b', 'loom-w7d'])
    const long = idColumnChars(['djinn-8ncc0.5', 'djinn-vyhci', 'djinn-8ncc0.2.2'])

    expect(short).toBeLessThan(long)
    expect(long).toBeGreaterThanOrEqual('djinn-8ncc0.2.2'.length)
  })

  it('fits the longest id it was given', () => {
    expect(idColumnChars(['djinn-8ncc0.5'])).toBeGreaterThanOrEqual('djinn-8ncc0.5'.length)
  })

  it('widens when the longest id arrives on a later page', () => {
    const firstPage = ['loom-7qh', 'loom-a7b']
    const bothPages = [...firstPage, 'loom-3ft.5.12']

    expect(idColumnChars(bothPages)).toBeGreaterThan(idColumnChars(firstPage))
  })

  it('holds a floor so an empty backlog does not collapse the column', () => {
    // The column is measured from loaded rows, and the loading state has none.
    // A zero-width column would snap to its measured width on first paint.
    expect(idColumnChars([])).toBe(ID_COL_MIN)
  })

  it('holds that same floor for ids shorter than it', () => {
    expect(idColumnChars(['bd-1'])).toBe(ID_COL_MIN)
  })

  it('caps the column so one pathological id cannot eat the title', () => {
    const runaway = 'megaproject-8ncc0.2.2.14.7'
    expect(runaway.length).toBeGreaterThan(ID_COL_MAX)
    expect(idColumnChars([runaway])).toBe(ID_COL_MAX)
  })

  it('ignores row order', () => {
    const ids = ['loom-7qh', 'djinn-8ncc0.5', 'bd-1']
    expect(idColumnChars(ids)).toBe(idColumnChars([...ids].reverse()))
  })
})
