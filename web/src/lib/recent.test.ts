import { beforeEach, describe, expect, it } from 'vitest'
import { readRecentIssues, rememberIssue } from './recent'

describe('recent issues', () => {
  beforeEach(() => sessionStorage.clear())

  it('remembers the title alongside the id', () => {
    rememberIssue('loom', 'loom-nlm', 'Command palette searches a silent sample')
    expect(readRecentIssues('loom')).toEqual([
      { id: 'loom-nlm', title: 'Command palette searches a silent sample' },
    ])
  })

  it('keeps the title so a recent bead survives without a client-side corpus', () => {
    // The palette no longer holds every issue in memory, so a recent entry
    // that carried only an id would render as an unlabelled row — or, as
    // before loom-nlm, be dropped entirely when the bead sat past row 200.
    rememberIssue('loom', 'scale-030.200.9', 'Deep row nobody ever fetched')
    const [entry] = readRecentIssues('loom')
    expect(entry?.title).toBe('Deep row nobody ever fetched')
  })

  it('puts the most recent first and de-duplicates by id', () => {
    rememberIssue('loom', 'a', 'A')
    rememberIssue('loom', 'b', 'B')
    rememberIssue('loom', 'a', 'A renamed')
    expect(readRecentIssues('loom')).toEqual([
      { id: 'a', title: 'A renamed' },
      { id: 'b', title: 'B' },
    ])
  })

  it('caps the list', () => {
    for (let i = 0; i < 20; i += 1) rememberIssue('loom', `id-${i}`, `Title ${i}`)
    expect(readRecentIssues('loom').length).toBeLessThanOrEqual(8)
  })

  it('scopes recency to the project', () => {
    rememberIssue('loom', 'loom-1', 'One')
    rememberIssue('djinn', 'djinn-1', 'Two')
    expect(readRecentIssues('loom').map((r) => r.id)).toEqual(['loom-1'])
    expect(readRecentIssues('djinn').map((r) => r.id)).toEqual(['djinn-1'])
  })

  it('reads the pre-loom-nlm id-only format without losing the entry', () => {
    sessionStorage.setItem('loom.recent.loom', JSON.stringify(['loom-old']))
    expect(readRecentIssues('loom')).toEqual([{ id: 'loom-old', title: '' }])
  })

  it('survives junk in storage', () => {
    sessionStorage.setItem('loom.recent.loom', '{not json')
    expect(readRecentIssues('loom')).toEqual([])
    sessionStorage.setItem('loom.recent.loom', JSON.stringify([{ nope: 1 }, 7, null]))
    expect(readRecentIssues('loom')).toEqual([])
  })

  it('ignores a call with no project or no id', () => {
    rememberIssue('', 'x', 'X')
    rememberIssue('loom', '', 'X')
    expect(readRecentIssues('loom')).toEqual([])
  })
})
