import { readdirSync, readFileSync } from 'node:fs'
import { join, relative, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

// A raw Enter check submits on the Enter that confirms an IME composition.
// Every Enter handler goes through isSubmitEnter (src/lib/enter.ts) instead.
// Each pattern runs over the whole file, so a comparison split across lines
// still matches; `\s` spans the newline.
const RAW_ENTER = [
  /\bkey\s*==?=?\s*['"`]Enter['"`]/g,
  /['"`]Enter['"`]\s*==?=?\s*[\w.?]*\bkey\b/g,
  /\bcase\s+['"`]Enter['"`]\s*:/g,
]

const SRC = resolve(process.cwd(), 'src')
const HELPER = join(SRC, 'lib', 'enter.ts')

function sourceFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) return sourceFiles(path)
    if (!/\.(ts|tsx)$/.test(entry.name) || /\.test\./.test(entry.name) || path === HELPER) return []
    return [path]
  })
}

/** Each raw Enter comparison in `text`, as `file:line: source` at the line it starts on. */
function rawEnterChecks(file: string, text: string): string[] {
  const lines = text.split('\n')
  const starts = new Set<number>()
  for (const re of RAW_ENTER) {
    for (const match of text.matchAll(re)) {
      starts.add(text.slice(0, match.index).split('\n').length - 1)
    }
  }
  return [...starts].sort((a, b) => a - b).map((i) => `${file}:${i + 1}: ${(lines[i] ?? '').trim()}`)
}

describe('Enter handlers go through isSubmitEnter (loom-gkp)', () => {
  it('finds no raw Enter comparison in web/src outside the helper and tests', () => {
    const files = sourceFiles(SRC)
    expect(files.length).toBeGreaterThan(50)
    const found = files.flatMap((path) =>
      rawEnterChecks(`web/src/${relative(SRC, path)}`, readFileSync(path, 'utf8')),
    )
    expect(found, `use isSubmitEnter from '@/lib/enter' instead of:\n${found.join('\n')}`).toEqual([])
  })

  it('flags each raw Enter form and leaves the helper call and other keys alone', () => {
    const planted = [
      "if (e.key === 'Enter') submit()",
      'if (event.key == "Enter") submit()',
      "if ('Enter' === e.key) submit()",
      "case 'Enter':",
      'if (isSubmitEnter(e)) submit()',
      "if (e.key === 'Escape') close()",
      "k === 'Enter' ? '↵' : k",
      'if (e.key ===',
      "  'Enter') submit()",
      'if (event.key',
      "  == 'Enter') submit()",
      "if ('Enter'",
      '  === e.key) submit()',
    ].join('\n')
    expect(rawEnterChecks('x.tsx', planted)).toEqual([
      "x.tsx:1: if (e.key === 'Enter') submit()",
      'x.tsx:2: if (event.key == "Enter") submit()',
      "x.tsx:3: if ('Enter' === e.key) submit()",
      "x.tsx:4: case 'Enter':",
      'x.tsx:8: if (e.key ===',
      'x.tsx:10: if (event.key',
      "x.tsx:12: if ('Enter'",
    ])
  })
})
