import type {
  BulkCloseRequest,
  BulkPatchRequest,
  BulkResult,
  ApplyFindingRequest,
  CloseIssueRequest,
  CreateIssueRequest,
  DepRequest,
  DoltStatus,
  EvidenceResponse,
  DoltSyncResult,
  DiscoveryRescanResult,
  DiscoveryStatus,
  DismissFindingRequest,
  Forensics,
  GraphResponse,
  Health,
  InitiativesResponse,
  InitiativeUpdateSummariesResponse,
  InitiativeUpdatesResponse,
  IssueDetail,
  IssueFilter,
  IssueList,
  IssuePatch,
  LogChunk,
  NowResponse,
  Project,
  ProjectSchema,
  PublishUpdateRequest,
  PublishUpdateResult,
  QueryResult,
  RunDetail,
  RunList,
  Stats,
  SummonPreview,
  SummonPreviewRequest,
  SummonRequest,
  SummonResponse,
  SpendResponse,
  StaleResponse,
  SweepReport,
} from './types'

/**
 * Typed fetch wrapper for the Loom API.
 *
 * The backend's failure envelope is `{"error": "…"}` with a 4xx/5xx status.
 * Everything that can go wrong — non-2xx, unparseable body, network down —
 * surfaces as an ApiError so views have exactly one error shape to render.
 */
export class ApiError extends Error {
  readonly status: number
  /**
   * bd's own stderr, forwarded by the backend on a 422. This is the sentence
   * that actually explains a rejected write ("issue loom-x is already closed"),
   * so it is surfaced verbatim in the error toast rather than summarised.
   */
  readonly stderr?: string
  /**
   * The decoded error body, when there was one.
   *
   * Some failures carry structured detail beyond `error` and `stderr` — the
   * graph's 413 returns `nodeCount` and `limit`, which is what lets the
   * too-large screen say "1634 nodes, limit 500" instead of shrugging. Keeping
   * the body means a new field on any error becomes readable without another
   * round of plumbing.
   */
  readonly body?: Record<string, unknown>
  /** Present when the failure came from the network/parse layer, not the API. */
  readonly cause?: unknown

  constructor(
    message: string,
    status: number,
    opts: { stderr?: string; cause?: unknown; body?: Record<string, unknown> } = {},
  ) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.stderr = opts.stderr
    this.body = opts.body
    this.cause = opts.cause
  }

  /** A number from the error body, when it is one. */
  detailNumber(field: string): number | undefined {
    const value = this.body?.[field]
    return typeof value === 'number' ? value : undefined
  }

  /** 422 means bd refused the write — the request was well-formed, the domain
   *  said no. Worth distinguishing from a 400 (we built a bad request). */
  get isWriteRejected(): boolean {
    return this.status === 422
  }

  /** Everything worth showing a human, stderr included. */
  get detail(): string {
    return this.stderr ? `${this.message}\n${this.stderr}` : this.message
  }

  /** 0 means "never reached the server" — worth a different empty state. */
  get isOffline(): boolean {
    return this.status === 0
  }

  /**
   * A 504 `query_timeout`: the project's database answered but this read ran
   * past its deadline. The project is up, so this is an inline error with a
   * retry, never the unreachable-server panel.
   */
  get isQueryTimeout(): boolean {
    return this.status === 504 && this.body?.code === 'query_timeout'
  }
}

/**
 * The assignee named by a close that bd refused because another actor holds
 * the issue (a 409 carrying `assignee`), or undefined for any other failure.
 * An empty string means the claim is real but no name could be read.
 */
export function closeClaimedBy(err: unknown): string | undefined {
  if (!(err instanceof ApiError) || err.status !== 409) return undefined
  const assignee: unknown = err.body?.assignee
  return typeof assignee === 'string' ? assignee : undefined
}

/** Pull `{error}` out of a failed response, falling back to the status line. */
async function errorFrom(res: Response): Promise<ApiError> {
  const fallback = `${res.status} ${res.statusText || 'Request failed'}`.trim()
  let body: unknown
  try {
    body = await res.json()
  } catch {
    return new ApiError(fallback, res.status)
  }
  const envelope = (body ?? {}) as { error?: unknown; stderr?: unknown }
  const message = typeof envelope.error === 'string' ? envelope.error : fallback
  const stderr = typeof envelope.stderr === 'string' && envelope.stderr.trim()
    ? envelope.stderr
    : undefined
  return new ApiError(message || fallback, res.status, { stderr, body: envelope as Record<string, unknown> })
}

export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response
  try {
    res = await fetch(path, {
      ...init,
      headers: { Accept: 'application/json', ...init?.headers },
    })
  } catch (cause) {
    throw new ApiError('Cannot reach the Loom server', 0, { cause })
  }

  if (!res.ok) throw await errorFrom(res)

  if (res.status === 204) return undefined as T
  try {
    return (await res.json()) as T
  } catch (cause) {
    throw new ApiError('Server sent a malformed response', res.status, { cause })
  }
}

/** Drop empty values so we never send `?status=&label=`. */
export function toQuery(
  params: Record<string, string | number | boolean | undefined>,
): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === '' || v === false) continue
    q.set(k, String(v))
  }
  const s = q.toString()
  return s ? `?${s}` : ''
}

/** The surface every transport must implement — real HTTP or the dev mock. */
export interface LoomClient {
  health(): Promise<Health>
  projects(): Promise<Project[]>
  discovery(): Promise<DiscoveryStatus>
  discoveryHide(workdir: string): Promise<DiscoveryStatus>
  discoveryUnhide(workdir: string): Promise<DiscoveryStatus>
  discoveryRescan(): Promise<DiscoveryRescanResult>
  issues(project: string, filter?: IssueFilter): Promise<IssueList>
  issue(project: string, id: string): Promise<IssueDetail>
  stats(project: string): Promise<Stats>
  schema(project: string): Promise<ProjectSchema>
  initiatives(project: string, includeClosed: boolean): Promise<InitiativesResponse>
  initiativeUpdateSummaries(project: string): Promise<InitiativeUpdateSummariesResponse>
  initiativeUpdates(project: string, epic: string): Promise<InitiativeUpdatesResponse>
  publishUpdate(
    project: string,
    epic: string,
    week: string,
    body: PublishUpdateRequest,
  ): Promise<PublishUpdateResult>

  // writes — every one of these goes through `bd` on the server, never SQL
  patchIssue(project: string, id: string, patch: IssuePatch): Promise<IssueDetail>
  bulkPatch(project: string, body: BulkPatchRequest): Promise<BulkResult>
  bulkClose(project: string, body: BulkCloseRequest): Promise<BulkResult>
  closeIssue(project: string, id: string, reason: string, force?: boolean): Promise<IssueDetail>
  reopenIssue(project: string, id: string, reason?: string): Promise<IssueDetail>
  addComment(project: string, id: string, text: string): Promise<IssueDetail>
  createIssue(project: string, body: CreateIssueRequest): Promise<IssueDetail>
  addDep(project: string, body: DepRequest): Promise<void>
  removeDep(project: string, body: DepRequest): Promise<void>

  doltStatus(project: string): Promise<DoltStatus>
  doltSync(project: string, verb: 'push' | 'pull'): Promise<DoltSyncResult>

  now(): Promise<NowResponse>
  runs(project: string): Promise<RunList>
  run(project: string, runId: string): Promise<RunDetail>
  runLog(project: string, runId: string, offset?: number): Promise<LogChunk>
  forensics(project: string, bead: string): Promise<Forensics>
  summonPreview(project: string, body: SummonPreviewRequest): Promise<SummonPreview>
  summon(project: string, body: SummonRequest): Promise<SummonResponse>
  stopRun(project: string, runId: string): Promise<void>
  evidence(project: string, bead: string): Promise<EvidenceResponse>
  spend(project: string): Promise<SpendResponse>

  graph(project: string, opts?: { epic?: string; includeClosed?: boolean }): Promise<GraphResponse>
  query(project: string, q: string): Promise<QueryResult>
  stale(project: string, days?: number): Promise<StaleResponse>
  sweep(project: string): Promise<SweepReport>
  applyFinding(
    project: string,
    fingerprint: string,
    body: ApplyFindingRequest,
  ): Promise<{ applied: boolean; fingerprint: string }>
  dismissFinding(
    project: string,
    fingerprint: string,
    body: DismissFindingRequest,
  ): Promise<{ dismissed: boolean; fingerprint: string }>
  undismissFinding(
    project: string,
    fingerprint: string,
  ): Promise<{ dismissed: boolean; fingerprint: string }>
}

const json = (body: unknown): RequestInit => ({
  method: 'POST',
  headers: { 'content-type': 'application/json' },
  body: JSON.stringify(body),
})

const p = encodeURIComponent

/** The djinn integration's routes for one project (internal/integration/djinn). */
const djinn = (project: string) => `/api/projects/${p(project)}/x/djinn`

export const httpClient: LoomClient = {
  health: () => request<Health>('/api/health'),
  projects: () => request<Project[]>('/api/projects'),
  discovery: () => request<DiscoveryStatus>('/api/discovery'),
  discoveryHide: (workdir) =>
    request<DiscoveryStatus>('/api/discovery/hide', json({ workdir })),
  discoveryUnhide: (workdir) =>
    request<DiscoveryStatus>('/api/discovery/unhide', json({ workdir })),
  discoveryRescan: () =>
    request<DiscoveryRescanResult>('/api/discovery/rescan', json({})),
  issues: (project, filter = {}) =>
    request<IssueList>(`/api/projects/${p(project)}/issues${toQuery({ ...filter })}`),
  issue: (project, id) => request<IssueDetail>(`/api/projects/${p(project)}/issues/${p(id)}`),
  stats: (project) => request<Stats>(`/api/projects/${p(project)}/stats`),
  schema: (project) => request<ProjectSchema>(`/api/projects/${p(project)}/schema`),
  initiatives: (project, includeClosed) =>
    request<InitiativesResponse>(
      `/api/projects/${p(project)}/initiatives${toQuery({ includeClosed })}`,
    ),
  initiativeUpdateSummaries: (project) =>
    request<InitiativeUpdateSummariesResponse>(`/api/projects/${p(project)}/initiative-updates`),
  initiativeUpdates: (project, epic) =>
    request<InitiativeUpdatesResponse>(
      `/api/projects/${p(project)}/initiatives/${p(epic)}/updates`,
    ),
  publishUpdate: (project, epic, week, body) =>
    request<PublishUpdateResult>(
      `/api/projects/${p(project)}/initiatives/${p(epic)}/updates/${p(week)}/publish`,
      json(body),
    ),

  patchIssue: (project, id, patch) =>
    request<IssueDetail>(`/api/projects/${p(project)}/issues/${p(id)}`, {
      ...json(patch),
      method: 'PATCH',
    }),
  bulkPatch: (project, body) =>
    request<BulkResult>(`/api/projects/${p(project)}/issues/bulk`, json(body)),
  bulkClose: (project, body) =>
    request<BulkResult>(`/api/projects/${p(project)}/issues/close`, json(body)),
  closeIssue: (project, id, reason, force) => {
    const body: CloseIssueRequest = force ? { reason, force: true } : { reason }
    return request<IssueDetail>(`/api/projects/${p(project)}/issues/${p(id)}/close`, json(body))
  },
  reopenIssue: (project, id, reason) =>
    request<IssueDetail>(`/api/projects/${p(project)}/issues/${p(id)}/reopen`, json({ reason: reason ?? '' })),
  addComment: (project, id, text) =>
    request<IssueDetail>(`/api/projects/${p(project)}/issues/${p(id)}/comments`, json({ text })),
  createIssue: (project, body) =>
    request<IssueDetail>(`/api/projects/${p(project)}/issues`, json(body)),
  addDep: (project, body) =>
    request<void>(`/api/projects/${p(project)}/deps`, json(body)).then(() => undefined),
  removeDep: (project, body) =>
    request<void>(`/api/projects/${p(project)}/deps`, { ...json(body), method: 'DELETE' }).then(
      () => undefined,
    ),

  doltStatus: (project) => request<DoltStatus>(`/api/projects/${p(project)}/dolt`),
  doltSync: (project, verb) =>
    request<DoltSyncResult>(`/api/projects/${p(project)}/dolt/${verb}`, json({})),

  now: () => request<NowResponse>('/api/now'),
  runs: (project) => request<RunList>(`${djinn(project)}/runs`),
  run: (project, runId) => request<RunDetail>(`${djinn(project)}/runs/${p(runId)}`),
  runLog: (project, runId, offset = 0) =>
    request<LogChunk>(
      `${djinn(project)}/runs/${p(runId)}/log${toQuery({ offset })}`,
    ),
  forensics: (project, bead) =>
    request<Forensics>(`${djinn(project)}/beads/${p(bead)}/forensics`),
  summonPreview: (project, body) =>
    request<SummonPreview>(`${djinn(project)}/summon/preview`, json(body)),
  summon: (project, body) =>
    request<SummonResponse>(`${djinn(project)}/summon`, json(body)),
  stopRun: (project, runId) =>
    request<void>(`${djinn(project)}/runs/${p(runId)}/stop`, json({})).then(
      () => undefined,
    ),
  evidence: (project, bead) =>
    request<EvidenceResponse>(`${djinn(project)}/beads/${p(bead)}/evidence`),
  spend: (project) => request<SpendResponse>(`${djinn(project)}/spend`),

  graph: (project, opts = {}) =>
    request<GraphResponse>(
      `/api/projects/${p(project)}/graph${toQuery({
        ...(opts.epic ? { epic: opts.epic } : {}),
        ...(opts.includeClosed ? { includeClosed: 1 } : {}),
      })}`,
    ),
  query: (project, q) =>
    request<QueryResult>(`/api/projects/${p(project)}/query${toQuery({ q })}`),
  stale: (project, days) =>
    request<StaleResponse>(`/api/projects/${p(project)}/stale${toQuery({ days })}`),
  sweep: (project) => request<SweepReport>(`/api/projects/${p(project)}/sweep`),
  applyFinding: (project, fingerprint, body) =>
    request<{ applied: boolean; fingerprint: string }>(
      `/api/projects/${p(project)}/sweep/findings/${p(fingerprint)}/apply`,
      json(body),
    ),
  dismissFinding: (project, fingerprint, body) =>
    request<{ dismissed: boolean; fingerprint: string }>(
      `/api/projects/${p(project)}/sweep/findings/${p(fingerprint)}/dismiss`,
      json(body),
    ),
  undismissFinding: (project, fingerprint) =>
    request<{ dismissed: boolean; fingerprint: string }>(
      `/api/projects/${p(project)}/sweep/findings/${p(fingerprint)}/dismiss`,
      { ...json({}), method: 'DELETE', body: undefined },
    ),
}

/**
 * VITE_MOCK=1 swaps the whole transport for fixtures so the frontend can be
 * built and screenshotted before the Go backend exists. The fixture module is
 * loaded lazily so it never lands in a production bundle.
 */
export const MOCK_MODE = import.meta.env.VITE_MOCK === '1'

let mockClient: Promise<LoomClient> | null = null
function getMock(): Promise<LoomClient> {
  mockClient ??= import('./fixtures').then((m) => m.mockClient)
  return mockClient
}

export const api: LoomClient = MOCK_MODE
  ? {
      health: () => getMock().then((c) => c.health()),
      projects: () => getMock().then((c) => c.projects()),
      discovery: () => getMock().then((c) => c.discovery()),
      discoveryHide: (workdir) => getMock().then((c) => c.discoveryHide(workdir)),
      discoveryUnhide: (workdir) => getMock().then((c) => c.discoveryUnhide(workdir)),
      discoveryRescan: () => getMock().then((c) => c.discoveryRescan()),
      issues: (project, filter) => getMock().then((c) => c.issues(project, filter)),
      issue: (project, id) => getMock().then((c) => c.issue(project, id)),
      stats: (project) => getMock().then((c) => c.stats(project)),
      schema: (project) => getMock().then((c) => c.schema(project)),
      initiatives: (project, includeClosed) =>
        getMock().then((c) => c.initiatives(project, includeClosed)),
      initiativeUpdateSummaries: (project) =>
        getMock().then((c) => c.initiativeUpdateSummaries(project)),
      initiativeUpdates: (project, epic) => getMock().then((c) => c.initiativeUpdates(project, epic)),
      publishUpdate: (project, epic, week, body) =>
        getMock().then((c) => c.publishUpdate(project, epic, week, body)),
      patchIssue: (project, id, patch) => getMock().then((c) => c.patchIssue(project, id, patch)),
      bulkPatch: (project, body) => getMock().then((c) => c.bulkPatch(project, body)),
      bulkClose: (project, body) => getMock().then((c) => c.bulkClose(project, body)),
      closeIssue: (project, id, reason) => getMock().then((c) => c.closeIssue(project, id, reason)),
      reopenIssue: (project, id, reason) => getMock().then((c) => c.reopenIssue(project, id, reason)),
      addComment: (project, id, text) => getMock().then((c) => c.addComment(project, id, text)),
      createIssue: (project, body) => getMock().then((c) => c.createIssue(project, body)),
      addDep: (project, body) => getMock().then((c) => c.addDep(project, body)),
      removeDep: (project, body) => getMock().then((c) => c.removeDep(project, body)),
      now: () => getMock().then((c) => c.now()),
      runs: (project) => getMock().then((c) => c.runs(project)),
      run: (project, runId) => getMock().then((c) => c.run(project, runId)),
      runLog: (project, runId, offset) => getMock().then((c) => c.runLog(project, runId, offset)),
      forensics: (project, bead) => getMock().then((c) => c.forensics(project, bead)),
      summonPreview: (project, body) => getMock().then((c) => c.summonPreview(project, body)),
      summon: (project, body) => getMock().then((c) => c.summon(project, body)),
      stopRun: (project, runId) => getMock().then((c) => c.stopRun(project, runId)),
      evidence: (project, bead) => getMock().then((c) => c.evidence(project, bead)),
      spend: (project) => getMock().then((c) => c.spend(project)),
      graph: (project, opts) => getMock().then((c) => c.graph(project, opts)),
      query: (project, q) => getMock().then((c) => c.query(project, q)),
      stale: (project, days) => getMock().then((c) => c.stale(project, days)),
      sweep: (project) => getMock().then((c) => c.sweep(project)),
      applyFinding: (project, fingerprint, body) =>
        getMock().then((c) => c.applyFinding(project, fingerprint, body)),
      dismissFinding: (project, fingerprint, body) =>
        getMock().then((c) => c.dismissFinding(project, fingerprint, body)),
      undismissFinding: (project, fingerprint) =>
        getMock().then((c) => c.undismissFinding(project, fingerprint)),
      doltStatus: (project) => getMock().then((c) => c.doltStatus(project)),
      doltSync: (project, verb) => getMock().then((c) => c.doltSync(project, verb)),
    }
  : httpClient
