#!/usr/bin/env node
/**
 * WCAG contrast gate for the Loom themes.
 *
 * Parses `src/themes/loom-*.css` directly — the CSS is the single source of
 * truth, there is no duplicated palette JSON to drift.
 *
 * ## Why this is a matrix and not a list
 *
 * The original gate measured every token against `--surface` only. That made
 * the AA claim true as scoped and misleading in practice: `--text-faint` is the
 * `.field` placeholder (painted on `--surface-sunken`), the menu hint (on
 * `--surface-overlay`) and the row metadata on a selected row (on
 * `--surface-selected`), and it was below AA on all three. The gate passed
 * while real screens didn't. (Review finding I3.)
 *
 * So PAIRINGS below enumerates every (ink, ground) combination the app
 * actually paints, and each one must clear AA. A pairing that cannot be fixed
 * by adjusting the token has to be removed from the component instead — the
 * checker is the place that decision gets recorded.
 *
 * Usage:  npm run check:contrast [-- --write]
 *         --write refreshes the trailing `/* n.nn:1 *\/` comments, which
 *         document each token's ratio against its WORST allowed ground.
 */
import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const themesDir = join(here, '..', 'src', 'themes')
const AA_SMALL = 4.5

/* ── colour math ─────────────────────────────────────────────────────────── */

/** oklch() → linear-light sRGB triple (clamped to gamut). */
function oklchToLinearSrgb(L, C, hDeg) {
  const h = (hDeg * Math.PI) / 180
  const a = C * Math.cos(h)
  const b = C * Math.sin(h)

  const l_ = L + 0.3963377774 * a + 0.2158037573 * b
  const m_ = L - 0.1055613458 * a - 0.0638541728 * b
  const s_ = L - 0.0894841775 * a - 1.291485548 * b

  const l = l_ ** 3
  const m = m_ ** 3
  const s = s_ ** 3

  return [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ].map((v) => Math.min(1, Math.max(0, v)))
}

const luminance = ([r, g, b]) => 0.2126 * r + 0.7152 * g + 0.0722 * b

function contrast(fg, bg) {
  const a = luminance(fg)
  const b = luminance(bg)
  const [hi, lo] = a > b ? [a, b] : [b, a]
  return (hi + 0.05) / (lo + 0.05)
}

/* ── parsing ─────────────────────────────────────────────────────────────── */

const DECL = /^(\s*)(--[\w-]+):\s*oklch\(([^)]+)\);(.*)$/

function parseOklch(body) {
  const [coords] = body.split('/')
  const parts = coords.trim().split(/\s+/).map(Number)
  if (parts.length < 3 || parts.some(Number.isNaN)) return null
  return parts.slice(0, 3)
}

function readTheme(name) {
  const path = join(themesDir, `${name}.css`)
  const lines = readFileSync(path, 'utf8').split('\n')
  const tokens = new Map()
  lines.forEach((line, i) => {
    const m = DECL.exec(line)
    if (!m) return
    const coords = parseOklch(m[3])
    if (coords) tokens.set(m[2], { coords, line: i })
  })
  return { path, lines, tokens }
}

/* ── the pairing matrix ──────────────────────────────────────────────────── */

/**
 * Grounds, and what is painted on each. Keep this honest: if a component
 * starts drawing an ink on a new ground, add it here and let the gate rule.
 */
const SURFACE = '--surface'
const SUNKEN = '--surface-sunken'
const RAISED = '--surface-raised'
const OVERLAY = '--surface-overlay'
const SELECTED = '--surface-selected'
const ACTIVE = '--surface-active'
const HOVER = '--surface-hover'
// Filled grounds: a button or badge whose *background* is a semantic colour.
// Their ink is a fixed pairing rather than the general ink set.
const ACCENT = '--accent'
const DANGER = '--danger'

const TEXT = ['--text', '--text-muted', '--text-faint']

const STATUS = [
  '--status-open',
  '--status-in-progress',
  '--status-blocked',
  '--status-deferred',
  '--status-closed',
  '--status-pinned',
  '--status-hooked',
]

const PRIORITY = ['--priority-p0', '--priority-p1', '--priority-p2', '--priority-p3', '--priority-p4']

const SEMANTIC = ['--accent', '--danger', '--warn', '--ok', '--label-review']
const TYPE = ['--type-epic', '--type-feature', '--type-task', '--type-bug', '--type-chore', '--type-decision', '--type-spike', '--type-story', '--type-milestone']

/** ground → inks painted on it, with a note naming a real consumer. */
const PAIRINGS = [
  { ground: SURFACE, inks: [...TEXT, ...STATUS, ...PRIORITY, ...SEMANTIC, ...TYPE], where: 'backlog rows, top bar, detail sheet body' },
  { ground: SUNKEN, inks: [...TEXT, ...STATUS, ...PRIORITY, ...SEMANTIC, ...TYPE], where: 'board columns, sidebar, .field inputs and their placeholders' },
  { ground: RAISED, inks: [...TEXT, ...STATUS, ...PRIORITY, ...SEMANTIC, ...TYPE], where: 'board cards, detail sheet panel, kbd chips' },
  { ground: OVERLAY, inks: [...TEXT, ...STATUS, ...PRIORITY, ...SEMANTIC, ...TYPE], where: 'menus, dialogs, palette, toasts, popovers' },
  { ground: SELECTED, inks: [...TEXT, ...STATUS, ...PRIORITY, ...SEMANTIC, ...TYPE], where: 'selected rows and cards, active sidebar project' },
  // ACTIVE is deliberately narrow, and that is a claim about the codebase, not
  // a convenience: it is used by `.btn:active`, `.btn-quiet[aria-pressed]` and
  // two 3px progress troughs. Buttons paint `--text`; the troughs contain no
  // ink at all. Listing status/priority here would force darker tokens for a
  // combination that is never rendered.
  { ground: ACTIVE, inks: ['--text', '--text-muted'], where: 'pressed controls (label only); progress troughs carry no ink' },

  // HOVER was missing from the original matrix even though it is the most-used
  // ground in the codebase — every row, card, menu item and quiet button paints
  // on it. It sits between raised and overlay in dark and between sunken and
  // surface in light, so it was implicitly covered; listing it makes that a
  // checked fact rather than an assumption. (Phase-6 audit.)
  { ground: HOVER, inks: [...TEXT, ...STATUS, ...PRIORITY, ...SEMANTIC, ...TYPE], where: 'hovered rows, cards, menu items and quiet buttons' },

  // Filled buttons and badges: the accent and danger grounds carry exactly one
  // ink each, and neither pairing was checked before. A primary button whose
  // label fails AA is a worse failure than a faint timestamp doing so.
  { ground: ACCENT, inks: ['--accent-text'], where: 'primary buttons, staged-tray and bulk-bar count badges, selected checkbox' },
  { ground: DANGER, inks: ['--text-inverted'], where: 'the summon confirm button and the stop-run button' },
]

/* ── the gate ────────────────────────────────────────────────────────────── */

const write = process.argv.includes('--write')
let failures = 0

for (const theme of ['loom-dark', 'loom-light']) {
  const { path, lines, tokens } = readTheme(theme)
  const ink = (name) => {
    const entry = tokens.get(name)
    return entry ? oklchToLinearSrgb(...entry.coords) : null
  }

  console.log(`\n${theme}`)
  console.log('─'.repeat(78))

  // worst ratio per ink, across every ground it is painted on
  const worst = new Map()

  for (const { ground, inks, where } of PAIRINGS) {
    const bg = ink(ground)
    if (!bg) throw new Error(`${theme}: missing ground ${ground}`)

    const failed = []
    for (const name of inks) {
      const fg = ink(name)
      if (!fg) continue
      const ratio = contrast(fg, bg)
      const previous = worst.get(name)
      if (!previous || ratio < previous.ratio) worst.set(name, { ratio, ground })
      if (ratio < AA_SMALL) failed.push([name, ratio])
    }

    const label = `${ground.replace('--surface-', '').replace('--surface', 'surface')}`
    if (failed.length === 0) {
      console.log(`  ✓ ${label.padEnd(10)} ${String(inks.length).padStart(2)} inks    ${where}`)
    } else {
      failures += failed.length
      console.log(`  ✗ ${label.padEnd(10)} ${failed.length} below AA   ${where}`)
      for (const [name, ratio] of failed) {
        console.log(`      ${name.padEnd(24)} ${ratio.toFixed(2)}:1`)
      }
    }
  }

  if (write) {
    for (const [name, { ratio, ground }] of worst) {
      const entry = tokens.get(name)
      if (!entry) continue
      const line = lines[entry.line]
      if (!/\/\*[^*]*:1[^*]*\*\//.test(line)) continue
      const g = ground.replace('--surface-', '').replace('--surface', 'surface')
      lines[entry.line] = line.replace(
        /\/\*(\s*)[\d.]+:1[^*]*\*\//,
        `/*$1${ratio.toFixed(2)}:1 worst (on ${g}) */`,
      )
    }
    writeFileSync(path, lines.join('\n'))
  }
}

console.log('')
if (failures) {
  console.error(
    `${failures} pairing(s) below WCAG AA (${AA_SMALL}:1).\n` +
      'Either adjust the token in src/themes/*.css, or stop painting that ink on\n' +
      'that ground and remove the pairing from PAIRINGS in this file.',
  )
  process.exit(1)
}
console.log(
  `All inks clear WCAG AA on every ground they are painted on ` +
    `(${PAIRINGS.length} grounds × both themes).`,
)
