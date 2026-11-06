import { act } from 'react'
import { afterEach, describe, expect, it } from 'vitest'
import { useStats } from '@/api/hooks'
import type { Project, Stats } from '@/api/types'
import { DJINN, testProject } from '@/test/projects'
import { renderWithProjects, until, type Rendered } from '@/test/render'
import { StatsStrip } from './StatsStrip'

/** The spend tile is djinn's `statTiles` contribution, read from /x/djinn/spend. */

const stats: Stats = { byStatus: { open: 3 }, ready: 2, depBlocked: 1, closedLast7d: 4 }

let view: Rendered | undefined
afterEach(() => {
  view?.unmount()
  view = undefined
})

const spendUrl = (p: string) => `/api/projects/${p}/x/djinn/spend`

async function strip(project: Project, reply?: () => { status: number; body: unknown } | 'pending') {
  view = await renderWithProjects(<StatsStrip project={project.name} stats={stats} />, {
    projects: [project],
    routes: reply ? { [spendUrl(project.name)]: reply } : {},
  })
  return view
}

const tile = () => document.querySelector<HTMLElement>('[data-testid="djinn-spend"]')

describe('StatsStrip spend tile', () => {
  it('renders a placeholder value while the request is pending', async () => {
    await strip(testProject('loom', DJINN), () => 'pending')
    expect(tile()?.textContent).toBe('—')
    expect(tile()?.querySelector('[aria-busy="true"]')).not.toBeNull()
  })

  it('reads "no run history" when no run touched the window', async () => {
    await strip(testProject('loom', DJINN), () => ({ status: 200, body: { last7dUsd: null } }))
    await until(() => tile()?.textContent === 'no run history')
    expect(tile()?.textContent).toBe('no run history')
  })

  it('reads the dollar total when there is one', async () => {
    await strip(testProject('loom', DJINN), () => ({ status: 200, body: { last7dUsd: 3.5 } }))
    await until(() => tile()?.textContent?.includes('/ 7d') ?? false)
    expect(tile()?.textContent).toMatch(/^\$3\.50 \/ 7d$/)
  })

  it('renders the shared error affordance with the status code on a 5xx', async () => {
    await strip(testProject('loom', DJINN), () => ({ status: 502, body: { error: 'bad gateway' } }))
    await until(() => document.querySelector('[data-state="error"]') !== null)
    const error = document.querySelector('[data-state="error"]')!
    expect(error.getAttribute('role')).toBe('alert')
    expect(error.textContent).toContain('HTTP 502')
    expect(error.textContent).toContain('spend')
  })

  it('renders no spend tile and makes no spend request without djinn', async () => {
    const rendered = await strip(testProject('plain'))
    expect(tile()).toBeNull()
    expect(document.body.textContent).not.toMatch(/\/ 7d|run history/)
    expect(rendered.requests.filter((r) => r.includes('/x/djinn/'))).toEqual([])
    // The core facts still render.
    expect(document.body.textContent).toContain('ready')
  })
})

/** The strip as BacklogView mounts it: fed by useStats, retrying through it. */
function LiveStrip({ project }: { project: string }) {
  const stats = useStats(project)
  return (
    <StatsStrip
      project={project}
      stats={stats.data}
      error={stats.error}
      onRetry={() => void stats.refetch()}
    />
  )
}

/** LiveStrip plus a control that refetches, the way an SSE invalidation does. */
function RefetchingStrip({ project }: { project: string }) {
  const stats = useStats(project)
  return (
    <>
      <StatsStrip
        project={project}
        stats={stats.data}
        error={stats.error}
        onRetry={() => void stats.refetch()}
      />
      <button type="button" data-testid="refetch" onClick={() => void stats.refetch()}>
        refetch
      </button>
    </>
  )
}

const statsUrl = '/api/projects/plain/stats'
const timedOut = {
  status: 504,
  body: { error: 'query timed out: plain: stats ready: context deadline exceeded', code: 'query_timeout' },
}

describe('StatsStrip states', () => {
  it('shows a loading bar while the counts are pending', async () => {
    view = await renderWithProjects(<LiveStrip project="plain" />, {
      projects: [testProject('plain')],
      routes: { [statsUrl]: () => 'pending' },
    })
    const strip = document.querySelector('[data-testid="stats-strip"]')!
    expect(strip.querySelector('[aria-busy="true"][aria-label="Loading stats"]')).not.toBeNull()
  })

  it('says "query timed out" inline on a 504 query_timeout, and Retry refetches', async () => {
    let replies = 0
    view = await renderWithProjects(<LiveStrip project="plain" />, {
      projects: [testProject('plain')],
      routes: {
        [statsUrl]: () => (++replies === 1 ? timedOut : { status: 200, body: stats }),
      },
    })
    await until(() => document.querySelector('[data-state="error"]') !== null)
    const error = document.querySelector('[data-testid="stats-strip"] [data-state="error"]')!
    expect(error.getAttribute('role')).toBe('alert')
    expect(error.textContent).toContain('query timed out')
    expect(error.textContent).toContain('HTTP 504')
    // A slow count is not an outage: no unreachable-server panel, no bd advice.
    expect(document.body.textContent).not.toMatch(/server unreachable|bd dolt start/)

    const retry = [...error.querySelectorAll('button')].find((b) => b.textContent === 'Retry')!
    expect(retry).toBeDefined()
    await act(async () => retry.click())
    await until(() => document.querySelector('[data-state="error"]') === null)

    expect(view.requests.filter((r) => r === statsUrl)).toHaveLength(2)
    expect(document.querySelector('[data-testid="stats-strip"]')?.textContent).toContain('2 ready')
  })

  it('does not retry a query timeout behind the reader\'s back', async () => {
    view = await renderWithProjects(<LiveStrip project="plain" />, {
      projects: [testProject('plain')],
      routes: { [statsUrl]: () => timedOut },
    })
    // A retried timeout would hold the error back behind a 1s backoff.
    await until(() => document.querySelector('[data-state="error"]') !== null)
    expect(document.querySelector('[data-state="error"]')).not.toBeNull()
    await new Promise((r) => setTimeout(r, 50))
    expect(view.requests.filter((r) => r === statsUrl)).toHaveLength(1)
  })

  it('keeps the last counts and says a failed refetch inline with Retry', async () => {
    let replies = 0
    const refreshed: Stats = { ...stats, ready: 5 }
    view = await renderWithProjects(<RefetchingStrip project="plain" />, {
      projects: [testProject('plain')],
      routes: {
        [statsUrl]: () => {
          replies++
          if (replies === 1) return { status: 200, body: stats }
          if (replies === 2) return timedOut
          return { status: 200, body: refreshed }
        },
      },
    })
    const stripText = () => document.querySelector('[data-testid="stats-strip"]')?.textContent ?? ''
    await until(() => stripText().includes('2 ready'))

    const refetch = document.querySelector<HTMLButtonElement>('[data-testid="refetch"]')!
    await act(async () => refetch.click())
    await until(() => document.querySelector('[data-state="error"]') !== null)
    const error = document.querySelector('[data-testid="stats-strip"] [data-state="error"]')!
    expect(error.getAttribute('role')).toBe('alert')
    expect(error.textContent).toContain('query timed out')
    expect(error.textContent).toContain('HTTP 504')
    // The last good counts stay on screen beside the failure.
    expect(stripText()).toContain('2 ready')

    const retry = [...error.querySelectorAll('button')].find((b) => b.textContent === 'Retry')!
    expect(retry).toBeDefined()
    await act(async () => retry.click())
    await until(() => document.querySelector('[data-state="error"]') === null)
    expect(view.requests.filter((r) => r === statsUrl)).toHaveLength(3)
    expect(stripText()).toContain('5 ready')
  })

  it('says "could not load stats" for any other failure', async () => {
    // A 4xx, because useStats retries a plain 5xx with backoff before failing.
    view = await renderWithProjects(<LiveStrip project="plain" />, {
      projects: [testProject('plain')],
      routes: { [statsUrl]: () => ({ status: 400, body: { error: 'boom' } }) },
    })
    await until(() => document.querySelector('[data-state="error"]') !== null)
    const error = document.querySelector('[data-testid="stats-strip"] [data-state="error"]')!
    expect(error.textContent).toContain('could not load stats')
    expect(error.textContent).not.toContain('timed out')
  })
})
