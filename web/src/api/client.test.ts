import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, httpClient, request, toQuery } from './client'

/** Every failure path in client.ts throws ApiError; narrow once, here. */
const failure = (promise: Promise<unknown>): Promise<ApiError> =>
  promise.then(
    () => {
      throw new Error('expected the request to reject')
    },
    (e: unknown) => {
      if (!(e instanceof ApiError)) throw e
      return e
    },
  )

const respond = (body: unknown, init: ResponseInit = {}) =>
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      new Response(typeof body === 'string' ? body : JSON.stringify(body), {
        headers: { 'content-type': 'application/json' },
        ...init,
      }),
    ),
  )

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('request', () => {
  it('returns the parsed body on success', async () => {
    respond({ status: 'ok', bdVersion: '1.1.2' })
    await expect(request('/api/health')).resolves.toEqual({ status: 'ok', bdVersion: '1.1.2' })
  })

  it('unwraps the {error} envelope the backend sends on failure', async () => {
    respond({ error: 'project "ghost" is not configured' }, { status: 404 })
    const err = await failure(request('/api/projects/ghost/issues'))
    expect(err).toBeInstanceOf(ApiError)
    expect(err.message).toBe('project "ghost" is not configured')
    expect(err.status).toBe(404)
    expect(err.isOffline).toBe(false)
  })

  it('preserves backend stderr byte-for-byte for sweep and write failures', async () => {
    const stderr = 'guarded close: dependent loom-child remains open\n\n'
    respond({ error: 'bd apply failed', stderr }, { status: 422 })

    const err = await failure(request('/api/projects/loom/sweep/findings/abc/apply'))

    expect(err.stderr).toBe(stderr)
  })

  it('falls back to the status line when the error body is not JSON', async () => {
    respond('<html>502 Bad Gateway</html>', { status: 502, statusText: 'Bad Gateway' })
    const err = await failure(request('/api/projects'))
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(502)
    expect(err.message).toContain('502')
  })

  it('falls back when the error body is JSON without an error key', async () => {
    respond({ detail: 'something' }, { status: 500, statusText: 'Internal Server Error' })
    const err = await failure(request('/api/projects'))
    expect(err.message).toContain('500')
  })

  it('reports an unreachable server as status 0, not as a crash', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch')
      }),
    )
    const err = await failure(request('/api/projects'))
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(0)
    expect(err.isOffline).toBe(true)
    expect(err.cause).toBeInstanceOf(TypeError)
  })

  it('raises a distinct error when a 200 carries an unparseable body', async () => {
    respond('not json at all', { status: 200 })
    const err = await failure(request('/api/projects'))
    expect(err).toBeInstanceOf(ApiError)
    expect(err.message).toMatch(/malformed/i)
    expect(err.status).toBe(200)
  })
})

describe('toQuery', () => {
  it('omits empty and undefined values instead of sending ?status=', () => {
    expect(toQuery({ status: '', label: undefined, q: 'pool' })).toBe('?q=pool')
  })

  it('returns an empty string when nothing survives', () => {
    expect(toQuery({})).toBe('')
    expect(toQuery({ a: undefined, b: '' })).toBe('')
  })

  it('encodes values that would otherwise break the query string', () => {
    expect(toQuery({ q: 'a b&c=d' })).toBe('?q=a+b%26c%3Dd')
  })
})

describe('issues', () => {
  it('sends an epic subtree scope as a query parameter', async () => {
    respond({ issues: [], total: 0 })
    const fetchMock = vi.mocked(fetch)

    await httpClient.issues('loom-scale', { epic: 'scale-001' })

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/projects/loom-scale/issues?epic=scale-001')
  })
})

describe('sweep transport', () => {
  it('uses the backend finding routes with exact strict-decoder bodies', async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) =>
      new Response(JSON.stringify({ applied: true, dismissed: true, fingerprint: 'abc 123' })),
    )
    vi.stubGlobal('fetch', fetchMock)

    await httpClient.applyFinding('Loom Project', 'abc 123', {
      materialDigest: 'digest-1',
      window: 2000,
      branch: 'release',
    })
    await httpClient.dismissFinding('Loom Project', 'abc 123', {
      materialDigest: 'digest-1',
      note: 'accepted exception',
      window: 2000,
      branch: 'release',
    })
    await httpClient.undismissFinding('Loom Project', 'abc 123')

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/projects/Loom%20Project/sweep/findings/abc%20123/apply',
      '/api/projects/Loom%20Project/sweep/findings/abc%20123/dismiss',
      '/api/projects/Loom%20Project/sweep/findings/abc%20123/dismiss',
    ])
    expect(fetchMock.mock.calls[0]?.[1]?.method).toBe('POST')
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      materialDigest: 'digest-1',
      window: 2000,
      branch: 'release',
    })
    expect(fetchMock.mock.calls[1]?.[1]?.method).toBe('POST')
    expect(JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body))).toEqual({
      materialDigest: 'digest-1',
      note: 'accepted exception',
      window: 2000,
      branch: 'release',
    })
    expect(fetchMock.mock.calls[2]?.[1]?.method).toBe('DELETE')
    expect(fetchMock.mock.calls[2]?.[1]?.body).toBeUndefined()
  })

  it('sends the CSRF JSON content type when undismissing a finding', async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) =>
      new Response(JSON.stringify({ dismissed: false, fingerprint: 'abc' })),
    )
    vi.stubGlobal('fetch', fetchMock)

    await httpClient.undismissFinding('loom', 'abc')

    const [, init] = fetchMock.mock.calls[0]!
    expect(init?.method).toBe('DELETE')
    expect(init?.headers).toMatchObject({ 'content-type': 'application/json' })
  })
})

/**
 * Review finding N3: closing 40 selected issues was 40 sequential POSTs and 40
 * invalidate/refetch cycles. `bd close` has always taken multiple ids.
 */
describe('bulkClose', () => {
  it('posts one request with every id', async () => {
    const fetchMock = vi.fn(
      async (_url: string, _init?: RequestInit) =>
        new Response(JSON.stringify({ updated: 3, ids: ['a', 'b', 'c'] }), {
          headers: { 'content-type': 'application/json' },
        }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(
      httpClient.bulkClose('loom', { ids: ['a', 'b', 'c'], reason: 'wontfix' }),
    ).resolves.toEqual({ updated: 3, ids: ['a', 'b', 'c'] })

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const call = fetchMock.mock.calls[0]!
    expect(call[0]).toBe('/api/projects/loom/issues/close')
    expect(call[1]?.method).toBe('POST')
    expect(JSON.parse(String(call[1]?.body))).toEqual({
      ids: ['a', 'b', 'c'],
      reason: 'wontfix',
    })
  })

  it('surfaces a missing endpoint as an ApiError so the caller can fall back', async () => {
    // The endpoint is landing in parallel; useBulkClose retries per-issue when
    // the route is absent. Verified live: chi answers an unregistered POST on
    // an existing path prefix with 405, not 404, so both must be handled.
    for (const status of [404, 405]) {
      respond({ error: 'no route' }, { status })
      const err = await failure(httpClient.bulkClose('loom', { ids: ['a'], reason: 'x' }))
      expect(err.status).toBe(status)
    }
  })
})

describe('project discovery', () => {
  it('reads discovery status from the fixed route', async () => {
    const status = {
      enabled: true,
      roots: ['/work'],
      depth: 3,
      exclude: [],
      intervalSeconds: 300,
      lastScanAt: null,
      hidden: [],
    }
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) =>
      new Response(JSON.stringify(status)),
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(httpClient.discovery()).resolves.toEqual(status)
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/discovery',
      expect.objectContaining({ headers: expect.objectContaining({ Accept: 'application/json' }) }),
    )
  })

  it.each([
    ['hide', '/api/discovery/hide', 'discoveryHide'],
    ['unhide', '/api/discovery/unhide', 'discoveryUnhide'],
  ] as const)('posts %s with the exact workdir body and CSRF content type', async (_verb, path, method) => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) =>
      new Response(JSON.stringify({ enabled: true })),
    )
    vi.stubGlobal('fetch', fetchMock)

    await httpClient[method]('/work/My Project')

    const [url, init] = fetchMock.mock.calls[0]!
    expect(url).toBe(path)
    expect(init?.method).toBe('POST')
    expect(init?.headers).toMatchObject({ 'content-type': 'application/json' })
    expect(JSON.parse(String(init?.body))).toEqual({ workdir: '/work/My Project' })
  })

  it('posts an empty JSON object when rescanning', async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) =>
      new Response(JSON.stringify({ added: ['alpha'], removed: [] })),
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(httpClient.discoveryRescan()).resolves.toEqual({ added: ['alpha'], removed: [] })
    const [url, init] = fetchMock.mock.calls[0]!
    expect(url).toBe('/api/discovery/rescan')
    expect(init?.headers).toMatchObject({ 'content-type': 'application/json' })
    expect(init?.body).toBe('{}')
  })
})

/**
 * Some failures carry structured detail beyond the {error} envelope — the
 * graph's 413 returns nodeCount and limit, which is what lets the too-large
 * screen be specific instead of shrugging.
 */
describe('structured error bodies', () => {
  it('keeps the decoded body and reads numbers out of it', async () => {
    respond(
      { error: 'graph too large to render; scope it to an epic', nodeCount: 1634, limit: 500 },
      { status: 413 },
    )
    const err = await failure(request('/api/projects/djinn/graph'))
    expect(err.status).toBe(413)
    expect(err.detailNumber('nodeCount')).toBe(1634)
    expect(err.detailNumber('limit')).toBe(500)
  })

  it('returns undefined for a field that is absent or not a number', async () => {
    respond({ error: 'nope', nodeCount: 'lots' }, { status: 413 })
    const err = await failure(request('/api/x'))
    expect(err.detailNumber('nodeCount')).toBeUndefined()
    expect(err.detailNumber('missing')).toBeUndefined()
  })

  it('has no body when the response was not JSON', async () => {
    respond('<html>502</html>', { status: 502, statusText: 'Bad Gateway' })
    const err = await failure(request('/api/x'))
    expect(err.body).toBeUndefined()
    expect(err.detailNumber('anything')).toBeUndefined()
  })
})
