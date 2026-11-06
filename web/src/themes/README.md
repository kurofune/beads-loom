# Themes

A theme in Loom is **one CSS file that assigns values to semantic tokens**, and
nothing else. No component is aware of any theme; no theme is aware of any
component. Adding a third one touches two files — a new CSS file and one line of
a list — and requires no JavaScript, no component change, and no build step.

That is the claim this document exists to make checkable.

---

## The rule components obey

Components declare **meaning**, never colour:

```tsx
<span className="dot" data-status="in_progress" />   {/* not: className="bg-cyan-400" */}
<span className="chip" data-priority={0} />          {/* not: style={{ color: '#ff5555' }} */}
```

`src/index.css` binds meaning to a token by setting a local `--c`, which the
`.dot` / `.chip` / `.rail` primitives consume. So a status gets its colour from
`--status-in-progress` in whichever theme is active, and a component that wanted
to hard-code a colour would have to bypass the primitive to do it.

This is enforced, not merely encouraged: `npm run check:tokens` fails the build
on any raw hex, `rgb()`, `hsl()`, raw `oklch()`, or Tailwind palette class
(`bg-slate-800`) found anywhere outside this directory.

---

## The token list

Every token a theme must define. Copy this list, change the values, and you have
a theme.

### Surfaces — the grounds things are painted on

| Token | Used by |
|---|---|
| `--surface` | the app background, backlog rows, top bar, detail sheet body |
| `--surface-sunken` | board columns, sidebar, `.field` inputs, code blocks |
| `--surface-raised` | board cards, the detail sheet panel, `kbd` chips |
| `--surface-overlay` | menus, dialogs, the palette, toasts, popovers |
| `--surface-hover` | any hovered row, card, menu item or quiet button |
| `--surface-active` | pressed controls, progress-bar troughs |
| `--surface-selected` | selected rows and cards, the active sidebar project |

### Lines and text

| Token | Used by |
|---|---|
| `--border` | hairlines, input outlines, card rings |
| `--border-strong` | emphasised edges, unchecked checkboxes |
| `--border-focus` | usually `var(--accent)` |
| `--text` | primary copy, issue titles |
| `--text-muted` | secondary copy, prose fields |
| `--text-faint` | ids, timestamps, counts, placeholders |
| `--text-inverted` | ink on a filled `--danger` button |

### Accent — the one non-semantic colour

| Token | Used by |
|---|---|
| `--accent` | focus rings, selection, the ready frontier, primary buttons |
| `--accent-muted` | progress fills |
| `--accent-text` | ink on a filled `--accent` button |

### Semantic

`--danger`, `--warn`, `--ok`, `--label-review`

### Status — one hue per bd status (categorical)

`--status-open`, `--status-in-progress`, `--status-blocked`,
`--status-deferred`, `--status-closed`, `--status-pinned`, `--status-hooked`

### Priority — one hue family, hot → cold (sequential)

`--priority-p0` … `--priority-p4`

### Depth and texture

`--shadow-panel`, `--shadow-overlay`, `--shadow-control`, `--scrim`, `--thread`

Structural tokens — spacing, radii, motion, the font stacks, `--row-h` — live in
`tokens.css` and are **shared by every theme**. A theme does not redefine them.

---

## Adding a theme

Two files. Here is the whole of the first one, at the size a real theme's opening
lines actually are:

```css
/* src/themes/loom-slate.css */
[data-theme='loom-slate'] {
  color-scheme: dark;
  --surface: oklch(0.19 0.012 250);
  --surface-sunken: oklch(0.15 0.011 250);
  --surface-raised: oklch(0.23 0.013 250);
  --text: oklch(0.95 0.006 250);
  --text-faint: oklch(0.63 0.012 250);
  --accent: oklch(0.84 0.16 210);
  --status-in-progress: oklch(0.80 0.13 200);
  --priority-p0: oklch(0.71 0.20 27);
  /* …the rest of the token list above… */
}
```

Then:

1. `@import './themes/loom-slate.css'` in `src/index.css`, beside the other two.
2. Add `{ id: 'loom-slate', label: 'Slate' }` to `THEMES` in `src/lib/theme.ts`.

That is all. The Settings picker, the `t` shortcut, the palette action and the
pre-paint script in `index.html` all read from `THEMES`, so they pick it up
without being touched.

---

## Before you ship one: the contrast gate

```bash
npm run check:contrast
```

This is not advisory. It parses these CSS files directly — the CSS is the single
source of truth, there is no duplicated palette JSON to drift — converts oklch to
linear sRGB, and measures **every ink against every ground it is actually
painted on**: 9 grounds × ~20 inks × every theme. All must clear WCAG AA (4.5:1)
for small text.

A pairing matrix rather than a list, because measuring every token against
`--surface` alone is how the original gate passed while real screens failed:
`--text-faint` is the input placeholder (on `--surface-sunken`), the menu hint
(on `--surface-overlay`) and row metadata on a selected row — and it was below AA
on all three.

`npm run check:contrast -- --write` refreshes the ratio comments in the theme
files, so the documented numbers can never drift from the measured ones.

If a pairing cannot be fixed by adjusting the token, the alternative is to stop
painting that ink on that ground and remove the pairing from `PAIRINGS` in
`scripts/check-contrast.mjs`. The checker is where that decision gets recorded.

---

## Why oklch

Perceptual lightness. The priority ramp reads as a single temperature gradient
because P0…P4 differ mostly in chroma at controlled lightness, which is
straightforward in oklch and fiddly in hex. It also means a theme can be
re-lightened wholesale — nudging every `L` — without hues drifting, which is
exactly what the contrast gate asks you to do when a pairing fails.

Both shipped themes were finalised that way: the gate reported 34 failing
pairings, and the fix was a softened `--surface-selected` plus solved
minimum-lightness moves on 11 inks, not a hand-tuned repaint.
