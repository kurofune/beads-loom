# Screenshots

Referenced from the root `README.md`. Captured in phase 6 with Playwright against
a live `loom` binary on `:7333` and the vite dev server on `:5173` — real HTTP,
real `bd`, real Dolt. Nothing here is a mock or a mockup.

| File | Project | Viewport | Shows |
|---|---|---|---|
| `backlog-dark.png` | `loom` (seeded) | 1400 @2x | Backlog grouped by status, stats strip, blocked-by chips, `needs-operator` |
| `backlog-light.png` | `loom` (seeded) | 1400 @2x | The same screen in `loom-light`, so the pair is a true theme comparison |
| `board-dark.png` | `loom` (seeded) | 1940 @1.5x | All six status columns, WIP counts, ready count, blocked-distinct cards |
| `detail-panel.png` | `djinn` (real) | 1400 @1.5x | AC checklist, design, notes, children rollup, discovered-from, evidence 4/4 |
| `runs-dashboard.png` | `djinn` (real) | 1400 @1.5x | Spend, worker slots, per-bead timeline with the formula chain, log pane |

## Two deliberate departures from the original note here

**The board is 1940 px, not ~1400.** Columns are a fixed 268 px and the seeded
backlog populates six statuses, so the row is 1902 px wide. At 1400 the Closed
column is sliced by the frame edge — which is honest (the board scrolls
horizontally) but in a still image reads as a crop bug. Column geometry is
asserted in the capture script rather than eyeballed.

**Detail and runs use `djinn`, not the seeded backlog.** The seeded `loom`
project has no evidence files and no run history, because both are produced by
actually running djinn. A screenshot of the empty-evidence state would show the
opposite of what its caption promises. `djinn` is a real 1,634-bead repository
on this machine and has both.

The runs shot is a **finished external run** — one djinn started from a terminal
and Loom reconstructed from `.djinn/state`. No summoner run has ever been
dispatched from this Loom (doing so
spends real money on a real repository), so the live-dispatch state is not
photographable here. The console pane says so in the picture, which is why the
README caption no longer claims "live summoner dispatch".
