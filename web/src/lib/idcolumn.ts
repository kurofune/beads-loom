/**
 * How wide the Backlog's id column should be, measured from the rows in hand.
 *
 * The column used to be a hardcoded 74px, which fits about eleven characters of
 * 11px IBM Plex Mono — so `djinn-8ncc0.5` rendered as `djinn-8ncc…`. The id is
 * the one field a reader copies out of the row to act on it in a terminal, and
 * an elided id is not copyable, so a fixed width was the wrong shape: project
 * prefixes differ in length and suffixed ids (`loom-3ft.5`, `djinn-8ncc0.2.2`)
 * are open-ended.
 *
 * Measuring the loaded rows means a project of short ids spends nothing on room
 * it never uses, and a project of long ones still shows them whole. Everything
 * the browser gives beyond this column goes to the title, which is the only
 * `flex-1` cell in the row.
 *
 * The measurement is in characters, not pixels, and it is applied through a CSS
 * custom property in `ch` units. That keeps this module pure — no DOM, no font
 * metrics, no per-row measure pass fighting the virtualizer — and it stays
 * correct if the row's font size changes, because `ch` is relative to the font
 * actually rendering.
 */

/**
 * The narrowest the column goes.
 *
 * The column is measured from loaded rows, and both the loading state and an
 * empty result have none. Without a floor the column would start at zero and
 * snap wider on first paint, which reads as a layout bug rather than a measured
 * fit. Eight characters also keeps the shortest real ids (`bd-1`) sitting in a
 * column rather than jammed against the type glyph.
 */
export const ID_COL_MIN = 8

/**
 * The widest the column goes.
 *
 * A measured column has one failure mode: a single outlier id sets the width
 * for every row, and at some length that outlier is stealing the title's space
 * to display something no one is reading. Past this cap the outlier truncates
 * — it is the only id that does — and every other row keeps its full width and
 * its title. Sixteen characters clears the longest realistic id shape (a
 * project prefix plus two suffix levels, `djinn-8ncc0.2.2`).
 */
export const ID_COL_MAX = 16

/**
 * The id column's width, in characters, for the ids currently loaded.
 *
 * Recomputes as pages arrive, so a long id first seen on page three widens the
 * column then. That is a deliberate accepted cost of measuring rather than
 * fixing the width: the alternative is reserving the worst case on every
 * project, which is the waste this replaces.
 */
export function idColumnChars(ids: readonly string[]): number {
  let widest = ID_COL_MIN
  for (const id of ids) {
    if (id.length > widest) widest = id.length
  }
  return Math.min(widest, ID_COL_MAX)
}
