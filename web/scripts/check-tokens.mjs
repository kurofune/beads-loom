#!/usr/bin/env node
/**
 * Token discipline gate.
 *
 * Colour may be *defined* only in src/themes/*.css. Everywhere else — every
 * component, every stylesheet rule — must reference a semantic custom
 * property. This catches the two ways that rule usually rots: someone drops a
 * hex in to "just fix this one shadow", or reaches for a Tailwind palette
 * class like `bg-slate-800` that ignores the theme entirely.
 */
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, relative, sep } from 'node:path'

const src = join(dirname(fileURLToPath(import.meta.url)), '..', 'src')
const THEME_DIR = `themes${sep}`

const RULES = [
  { name: 'raw hex', re: /#[0-9a-fA-F]{3,8}\b/ },
  { name: 'rgb()/hsl()', re: /\b(rgba?|hsla?)\(/ },
  { name: 'raw oklch()', re: /\boklch\(/ },
  {
    name: 'Tailwind palette class',
    re: /\b(?:bg|text|border|ring|fill|stroke|from|via|to)-(?:slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-\d{2,3}\b/,
  },
]

const files = []
;(function walk(dir) {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) walk(full)
    else if (/\.(tsx?|css)$/.test(entry)) files.push(full)
  }
})(src)

const violations = []
for (const file of files) {
  const rel = relative(src, file)
  if (rel.startsWith(THEME_DIR)) continue // the one place colour is allowed
  readFileSync(file, 'utf8')
    .split('\n')
    .forEach((line, i) => {
      // Comments are prose about the rule, not a breach of it.
      const code = line.replace(/\/\*.*?\*\//g, '').replace(/(^|\s)(\/\/|\*|\/\*).*$/, '')
      for (const rule of RULES) {
        if (rule.re.test(code)) violations.push(`  src/${rel}:${i + 1}  ${rule.name}  →${line.trim()}`)
      }
    })
}

if (violations.length) {
  console.error(`Colour must come from a token. ${violations.length} violation(s):\n`)
  console.error(violations.join('\n'))
  console.error('\nDefine the colour in src/themes/*.css and reference the semantic name.')
  process.exit(1)
}
console.log(`Token discipline clean across ${files.length} files.`)
