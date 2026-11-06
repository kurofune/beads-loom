import { describe, expect, it } from 'vitest'
import {
  ISSUE_LINK_SCHEME,
  RUN_LINK_SCHEME,
  issueIdFromHref,
  latestWeekTone,
  linkifyCitations,
  runIdFromHref,
} from './updates'

describe('linkifyCitations', () => {
  it('links a bead id only when the issue exists', () => {
    const out = linkifyCitations('- loom-d1q.1 landed; the read-only view shipped', ['loom-d1q.1'])
    expect(out).toBe(`- [loom-d1q.1](${ISSUE_LINK_SCHEME}loom-d1q.1) landed; the read-only view shipped`)
  })

  it('prefers the longest id so a parent does not swallow its child', () => {
    const out = linkifyCitations('- loom-d1q.1 under loom-d1q', ['loom-d1q', 'loom-d1q.1'])
    expect(out).toBe(
      `- [loom-d1q.1](${ISSUE_LINK_SCHEME}loom-d1q.1) under [loom-d1q](${ISSUE_LINK_SCHEME}loom-d1q)`,
    )
  })

  it('leaves a known id that is only a prefix of the written token alone', () => {
    // loom-d1q.1 does not exist, so the parent must not be linked over half of it.
    expect(linkifyCitations('- see loom-d1q.1 here', ['loom-d1q'])).toBe('- see loom-d1q.1 here')
  })

  it('links an id wrapped in parentheses, the shape the retro skill emits', () => {
    expect(linkifyCitations('- sweep landed (loom-d1q.1)', ['loom-d1q.1'])).toBe(
      `- sweep landed ([loom-d1q.1](${ISSUE_LINK_SCHEME}loom-d1q.1))`,
    )
  })

  it('renders a commit sha as mono text, never as a link', () => {
    const out = linkifyCitations('- shipped in b64f334', [])
    expect(out).toBe('- shipped in `b64f334`')
    expect(out).not.toContain(ISSUE_LINK_SCHEME)
  })

  it('renders a run id as mono text', () => {
    expect(linkifyCitations('- verified by run-1754270000000000000', [])).toBe(
      '- verified by `run-1754270000000000000`',
    )
    expect(linkifyCitations('- verified by ext-20260804T120000Z', [])).toBe(
      '- verified by `ext-20260804T120000Z`',
    )
  })

  it('links a run id the project has a run for, and leaves an unknown one mono', () => {
    const known = 'run-1754270000000000000'
    expect(linkifyCitations(`- verified by ${known}`, [], [known])).toBe(
      `- verified by [${known}](${RUN_LINK_SCHEME}${known})`,
    )
    expect(linkifyCitations('- verified by run-1600000000000000000', [], [known])).toBe(
      '- verified by `run-1600000000000000000`',
    )
  })

  it('links an external run id too, since it has the same run detail page', () => {
    const known = 'ext-20260804T120000Z'
    expect(linkifyCitations(`- drained by ${known}`, [], [known])).toBe(
      `- drained by [${known}](${RUN_LINK_SCHEME}${known})`,
    )
  })

  it('never lets the sha pass corrupt a link the id pass just wrote', () => {
    // The suffix is 8 hex characters, so a sha pass over the generated markdown
    // would put backticks inside both the link text and its href.
    expect(linkifyCitations('- fixed in loom-abcdef12', ['loom-abcdef12'])).toBe(
      `- fixed in [loom-abcdef12](${ISSUE_LINK_SCHEME}loom-abcdef12)`,
    )
    const run = 'run-1754270000000000000'
    expect(linkifyCitations(`- verified by ${run}`, [], [run])).toBe(
      `- verified by [${run}](${RUN_LINK_SCHEME}${run})`,
    )
  })

  it('never double-links a token already inside an inline code span', () => {
    expect(linkifyCitations('- run `bd show loom-d1q.1` for detail', ['loom-d1q.1'])).toBe(
      '- run `bd show loom-d1q.1` for detail',
    )
  })

  it('leaves fenced code blocks untouched', () => {
    const body = '## Closed\n\n```bash\nbd show loom-d1q.1 # b64f334\n```\n\n- loom-d1q.1 landed\n'
    const out = linkifyCitations(body, ['loom-d1q.1'])
    expect(out).toContain('bd show loom-d1q.1 # b64f334')
    expect(out).toContain(`- [loom-d1q.1](${ISSUE_LINK_SCHEME}loom-d1q.1) landed`)
  })

  it('does not rewrite an existing markdown link', () => {
    const body = '- see [the plan](https://example.test/loom-d1q.1)'
    expect(linkifyCitations(body, ['loom-d1q.1'])).toBe(body)
  })

  it('is a no-op on a body with nothing to cite', () => {
    const body = '# loom-yqh\n\nQuiet week for loom-yqh: nothing closed, started, or discovered.\n'
    expect(linkifyCitations(body, [])).toBe(body)
  })

  it('handles an empty body without throwing', () => {
    expect(linkifyCitations('', ['loom-d1q.1'])).toBe('')
  })
})

describe('issueIdFromHref', () => {
  it('reads back an id the linkifier wrote', () => {
    expect(issueIdFromHref(`${ISSUE_LINK_SCHEME}loom-d1q.1`)).toBe('loom-d1q.1')
  })

  it('ignores every other href, so external links keep their own behaviour', () => {
    expect(issueIdFromHref('https://example.test')).toBeNull()
    expect(issueIdFromHref(undefined)).toBeNull()
    expect(issueIdFromHref(ISSUE_LINK_SCHEME)).toBeNull()
  })
})

describe('runIdFromHref', () => {
  it('reads back a run id the linkifier wrote', () => {
    expect(runIdFromHref(`${RUN_LINK_SCHEME}run-1754270000000000000`)).toBe(
      'run-1754270000000000000',
    )
  })

  it('ignores an issue href, so the two schemes never cross', () => {
    expect(runIdFromHref(`${ISSUE_LINK_SCHEME}loom-d1q.1`)).toBeNull()
    expect(runIdFromHref('https://example.test')).toBeNull()
    expect(runIdFromHref(undefined)).toBeNull()
    expect(runIdFromHref(RUN_LINK_SCHEME)).toBeNull()
  })
})

describe('latestWeekTone', () => {
  it('marks a draft so the chip reads as unpublished work', () => {
    expect(latestWeekTone('draft')).toBe('warn')
  })

  it('marks a published update as settled', () => {
    expect(latestWeekTone('published')).toBe('accent')
  })

  it('falls back to neutral for a status this build does not know', () => {
    expect(latestWeekTone('archived')).toBe('neutral')
    expect(latestWeekTone('')).toBe('neutral')
  })
})
