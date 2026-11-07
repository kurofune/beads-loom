import { readdirSync, readFileSync } from 'node:fs'
import { join, relative, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

// A raw Enter check submits on the Enter that confirms an IME composition.
// Every Enter handler goes through isSubmitEnter (src/lib/enter.ts) instead.
const RAW_ENTER = [
  /\bkey\s*==?=?\s*['"`]Enter['"`]/,
  /['"`]Enter['"`]\s*==?=?\s*[\w.?]*\bkey\b/,
  /\bcase\s+['"`]Enter['"`]\s*:/,
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

/** Each raw Enter comparison in `text`, as `file:line: source`. */
function rawEnterChecks(file: string, text: string): string[] {
  return text.split('\n').flatMap((line, i) =>
    RAW_ENTER.some((re) => re.test(line)) ? [`${file}:${i + 1}: ${line.trim()}`] : [],
  )
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
    ].join('\n')
    expect(rawEnterChecks('x.tsx', planted)).toEqual([
      "x.tsx:1: if (e.key === 'Enter') submit()",
      'x.tsx:2: if (event.key == "Enter") submit()',
      "x.tsx:3: if ('Enter' === e.key) submit()",
      "x.tsx:4: case 'Enter':",
    ])
  })
})
