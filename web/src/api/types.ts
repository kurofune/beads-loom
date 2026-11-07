/**
 * Wire types — a literal mirror of the JSON the Go handlers in internal/httpapi send.
 *
 * Rule for this file: it describes what the backend sends, not what the UI
 * wishes it received. If a field is awkward, adapt it in a selector, never by
 * renaming here — the contract is shared with a sibling implementation.
 */

/** bd's seven built-in statuses. Projects may define custom ones in the
 *  `status.custom` config key (GET /schema), so the string escape hatch is
 *  deliberate. */
export type IssueStatus =
  | 'open'
  | 'in_progress'
  | 'blocked'
  | 'deferred'
  | 'closed'
  | 'pinned'
  | 'hooked'
  | (string & {})

/** bd built-in types + per-project custom types. */
export type IssueType =
  | 'task'
  | 'bug'
  | 'feature'
  | 'chore'
  | 'epic'
  | 'decision'
  | 'spike'
  | 'story'
  | 'milestone'
  | (string & {})

/** bd's nine core types, in bd's order (`bd types --json` → core_types). */
export const CORE_TYPES: IssueType[] = [
  'task',
  'bug',
  'feature',
  'chore',
  'epic',
  'decision',
  'spike',
  'story',
  'milestone',
]

/** One status a project accepts; category is bd's active | wip | done | frozen,
 *  or "" for a legacy bare custom name. */
export interface StatusDef {
  name: IssueStatus
  category: string
}

/**
 * GET /api/projects/{p}/schema — the statuses and types this project accepts:
 * bd's built-ins first, then the project's own. Truth: internal/queries/schema.go.
 */
export interface ProjectSchema {
  statuses: StatusDef[]
  types: IssueType[]
}

/** What the menus offer before (or instead of) the project's own schema. */
export const BUILTIN_SCHEMA: ProjectSchema = {
  statuses: [
    { name: 'open', category: 'active' },
    { name: 'in_progress', category: 'wip' },
    { name: 'blocked', category: 'wip' },
    { name: 'deferred', category: 'frozen' },
    { name: 'closed', category: 'done' },
    { name: 'pinned', category: 'frozen' },
    { name: 'hooked', category: 'wip' },
  ],
  types: CORE_TYPES,
}

/** 0 = highest. Rendered P0…P4. */
export type Priority = 0 | 1 | 2 | 3 | 4

export interface ProjectHealth {
  ready: number
  inProgress: number
  blocked: number
  open: number
  total: number
}

export interface Project {
  name: string
  db: string
  workdir: string
  source: 'config' | 'discovered'
  reachable: boolean
  /** Why the project is unreachable; "" when reachable. */
  reason: string
  /**
   * False when bd would write to a different database than the one Loom reads
   * (a server:/database: override that disagrees with .beads/metadata.json);
   * every write route then answers 409 with writeReason.
   * Truth: internal/registry/writable.go.
   */
  writable: boolean
  /** Why the project is read-only; "" when writable. */
  writeReason: string
  /**
   * The optional integrations this project has, keyed by provider id (e.g.
   * "djinn"), and what each can do here. A provider that does not apply has no
   * key. Truth: internal/httpapi/api.go projectResponse, registry.Capabilities.
   */
  integrations: Record<string, IntegrationState>
  /**
   * CONTRACT DIVERGENCE (reported, not resolved): phase-1-backend.md always
   * shows a health object, but internal/httpapi/api.go declares
   * `Health *queries.Health` and documents that an unreachable project comes
   * back with a null health block. Typed nullable so a dead dolt server
   * degrades instead of throwing on `health.open`.
   */
  health: ProjectHealth | null
}

/** One optional integration's state for one project. */
export interface IntegrationState {
  /** What works here right now, e.g. djinn's "runs", "evidence", "dispatch". */
  capabilities: string[]
  /** Per capability that does not work here, why not. Absent when all work. */
  reasons?: Record<string, string>
}

export interface HiddenProject {
  workdir: string
  name: string
}

export interface DiscoveryStatus {
  enabled: boolean
  roots: string[]
  depth: number
  exclude: string[]
  intervalSeconds: number
  lastScanAt: string | null
  hidden: HiddenProject[]
}

export interface DiscoveryRescanResult {
  added: string[]
  removed: string[]
}

export interface DepCounts {
  blockedBy: number
  blocks: number
}

export interface ParentRef {
  id: string
  title: string
}

/** Direct-children rollup shown on epic cards and chips. */
export interface ChildStats {
  total: number
  closed: number
  /** Direct children that are in_progress or hooked. Never folded into closed. */
  started: number
}

/** Transitive-subtree rollup shown on the Initiatives view. */
export interface InitiativeRollup {
  total: number
  closed: number
  /**
   * Descendants whose work is under way. Disjoint from every other bucket:
   * an in_progress issue is absent from ready_issues and is neither
   * depBlocked nor deferScheduled, so before this existed it was counted in
   * total and nothing else.
   */
  started: number
  ready: number
  depBlocked: number
  deferScheduled: number
  /**
   * Descendants on either end of a `blocks` dependency, whatever the other
   * end's status. A shape count, not a bucket: it overlaps the others, and it
   * is wider than depBlocked because it keeps the blockers themselves and the
   * pairs whose blocker has closed.
   */
  chained: number
}

/** Missing promised-vs-landed receipts among closed transitive descendants. */
export interface NoReceipt {
  /** Missing either receipt, with a bead missing both counted once. */
  total: number
  noAcceptance: number
  noCloseReason: number
}

/** Deterministic burn-health state computed by the server. */
export type HealthState = 'on-track' | 'at-risk' | 'off-track' | 'no-target' | 'no-scope'

/** Compact health signal; the explanation carries the arithmetic behind it. */
export interface InitiativeHealth {
  state: HealthState
  explanation: string
}

export interface Initiative {
  id: string
  title: string
  status: string
  priority: number
  assignee: string
  due: string | null
  updated: string
  lastActivity: string
  subtree: InitiativeRollup
  /** True when the open epic has descendants and every descendant is closed. */
  closeEligible: boolean
  health: InitiativeHealth
  /**
   * The missing-target nudge's firing condition, decided server-side: an open
   * epic with descendants, no due date, and no `no-target` opt-out label.
   * Narrower than `health.state === 'no-target'`, which also covers closed and
   * scopeless epics that need no date at all.
   */
  needsTarget: boolean
  noReceipt: NoReceipt
}

export interface InitiativesResponse {
  initiatives: Initiative[]
}

/** A row in the backlog. */
export interface Issue {
  id: string
  title: string
  type: IssueType
  status: IssueStatus
  priority: Priority
  assignee: string
  labels: string[]
  /**
   * Minutes. CONTRACT DIVERGENCE (reported, not resolved): the contract example
   * shows a plain number, but internal/queries/types.go declares `Estimate *int`
   * so an issue with no estimate serialises as null.
   */
  estimate: number | null
  created: string
  updated: string
  closed: string | null
  due: string | null
  deferUntil: string | null
  /**
   * Truth for readiness, straight from the `ready_issues` SQL view. NEVER
   * infer this: a dependency-blocked bead keeps stored status `open`, so
   * status alone cannot tell you whether work can start.
   */
  ready: boolean
  /**
   * *Why* an open issue is not ready. `ready_issues` excludes a row for two
   * unrelated reasons — an unmet `blocks` dependency, or a future `defer_until`
   * (on the issue or its parent) — and conflating them tells a manager who
   * deferred three issues to next sprint that three issues are blocked.
   *
   * Tolerant by design: `undefined` means the backend has not sent the field
   * (it is being added in parallel), and callers fall back to the old
   * dependency-only reading rather than guessing. See notReadyReason().
   */
  notReady?: 'deps' | 'deferred' | null
  /** Closed issue has no acceptance criteria after whitespace normalization. */
  missingAcceptance: boolean
  /** Closed issue has no close reason after whitespace normalization. */
  missingCloseReason: boolean
  pinned: boolean
  depCounts: DepCounts
  parent: ParentRef | null
  childStats: ChildStats | null
}

export interface IssueList {
  issues: Issue[]
  total: number
}

export interface IssueRef {
  id: string
  title: string
  status: IssueStatus
  priority: Priority
  ready: boolean
}

/** The five typed dependency buckets. `discoveredFrom` is provenance, NOT a
 *  blocker — the detail panel styles it accordingly. */
export interface IssueDeps {
  blocks: IssueRef[]
  blockedBy: IssueRef[]
  children: IssueRef[]
  discoveredFrom: IssueRef[]
  parent: IssueRef | null
}

export interface Comment {
  id: string
  author: string
  text: string
  created: string
}

export interface IssueEvent {
  type: string
  actor: string
  oldValue: string
  newValue: string
  comment: string
  created: string
}

/** GET /api/projects/{p}/issues/{id} — the list shape plus the prose fields. */
export interface IssueDetail extends Issue {
  /** Present only for epics; uses the same transitive rollup as Initiatives. */
  health: InitiativeHealth | null
  description: string
  /** Raw text; one criterion per non-blank line. Split with splitCriteria(). */
  acceptanceCriteria: string
  design: string
  notes: string
  closeReason: string
  owner: string
  deps: IssueDeps
  comments: Comment[]
  events: IssueEvent[]
}

export interface Stats {
  byStatus: Record<string, number>
  ready: number
  /** open ∧ NOT ready ∧ not manually blocked — the silent backlog killer.
   *  Since review fix I2 this excludes defer-scheduled work. */
  depBlocked: number
  closedLast7d: number

  /* --- phase 5, additive ------------------------------------------------ */
  /** Equals `ready`. The separate name is what a manager reads as "how much
   *  work is available right now", which is why the backend exposes both. */
  readyFrontier?: number
  /** Defer-scheduled rows, split out of depBlocked by review fix I2. */
  deferScheduled?: number
  /** 30 days, oldest first, zero-filled — plot it without patching gaps. */
  trend?: TrendPoint[]
}

export interface Health {
  status: string
  bdVersion: string
}

/** Server-side filters. Kept in the client so the phase-2 filter bar has a
 *  typed surface to bind to; phase 1 only ever sends `status`. */
export interface IssueFilter {
  /** Comma-separated list, e.g. "open,in_progress" (filterFromQuery §multiValue). */
  status?: string
  type?: string
  priority?: string
  label?: string
  assignee?: string
  q?: string
  /** Closed issues are excluded by default; this asks for them in the same page. */
  includeClosed?: boolean
  /** Transitive descendants of this epic, excluding the epic row itself. */
  epic?: string
  /** Closed issues missing acceptance criteria or a close reason. */
  noReceipt?: boolean
  limit?: number
  offset?: number
  sort?: 'priority' | 'created' | 'updated'
}

/* ===========================================================================
 * Write contract — phase 2
 * ===========================================================================
 * Truth is internal/httpapi/writes.go, not prose. Two things about it shape
 * every type below:
 *
 *   1. The decoder runs DisallowUnknownFields. An extra key is a 400, so these
 *      bodies must be exact — never spread a whole Issue into a patch.
 *   2. PATCH / close / reopen / comment / create all respond with the full
 *      re-read IssueDetail, because bd normalises values and recomputes
 *      readiness. The response is authoritative; the optimistic guess is not.
 * ========================================================================= */

/**
 * PATCH body. Every field optional; omit rather than send null — the Go side
 * uses pointers to tell "absent" from "set to zero", which is what makes
 * priority 0 and clearing an assignee both expressible.
 */
export interface IssuePatch {
  status?: IssueStatus
  priority?: Priority
  assignee?: string
  title?: string
  type?: IssueType
  estimate?: number
  /**
   * Prose fields. Note `acceptance`, not `acceptanceCriteria` — the write
   * bodies and the read shape spell this field differently, same as create.
   * An empty string clears the field; omit to leave it alone.
   */
  description?: string
  acceptance?: string
  design?: string
  notes?: string
  labelsAdd?: string[]
  labelsRemove?: string[]
  /** bd date syntax: an ISO date or a relative offset like "+2d". */
  defer?: string
  due?: string
}

export interface BulkPatchRequest {
  ids: string[]
  patch: IssuePatch
}

/**
 * POST …/issues/{id}/close. `force` overrides another actor's claim: send it
 * only after the user has seen the 409's assignee and confirmed.
 */
export interface CloseIssueRequest {
  reason: string
  force?: boolean
}

/**
 * The 409 from POST …/issues/{id}/close when bd refused because another actor
 * holds the issue. `assignee` is read from the issue row; empty when no name
 * could be found.
 */
export interface CloseClaimedConflict {
  error: string
  stderr?: string
  assignee: string
}

/** POST …/issues/close — one `bd close id1 id2 …`, one invalidation. */
export interface BulkCloseRequest {
  ids: string[]
  reason: string
}

/** POST …/issues/bulk deliberately returns counts, not bodies: a bulk edit can
 *  touch hundreds of beads and the client refetches the list it is showing. */
export interface BulkResult {
  updated: number
  ids: string[]
}

export interface DepSpec {
  id: string
  /** blocks | parent-child | discovered-from | related | … */
  type: string
}

/**
 * POST …/issues. Note `acceptance`, not `acceptanceCriteria` — the create body
 * and the read shape spell this field differently.
 */
export interface CreateIssueRequest {
  title: string
  type?: IssueType
  priority?: Priority
  description?: string
  acceptance?: string
  design?: string
  notes?: string
  parent?: string
  labels?: string[]
  estimate?: number
  deps?: DepSpec[]
}

export interface DepRequest {
  from: string
  to: string
  type?: string
}

/* ===========================================================================
 * Dolt sync — phase 3
 * ===========================================================================
 * Truth is internal/queries/dolt.go. Note the two deliberate null shapes:
 *   · `remote` is null when the project has no remote configured
 *   · `ahead` / `behind` are OMITTED (not zero) when there is no remote, or
 *     when the tracking branch has not been fetched — a plain 0 would be a
 *     lie, and the backend has a test for exactly that trap.
 * ========================================================================= */

export interface DoltCommit {
  hash: string
  message: string
  when: string
}

export interface DoltStatus {
  /** Uncommitted working-set changes (`dolt_status`). */
  dirty: boolean
  ahead?: number
  behind?: number
  remote: string | null
  branch: string
  lastCommit: DoltCommit | null
}

/** POST …/dolt/push|pull. `output` is bd's combined stdout+stderr, verbatim. */
export interface DoltSyncResult {
  ok: boolean
  output: string
}

/* ===========================================================================
 * Runs, summoner dispatch, forensics and evidence — phase 4
 * ===========================================================================
 * Truth is internal/runs/model.go and internal/runs/evidence.go.
 *
 * The shaping constraint, stated in that package's doc comment: Loom is a
 * *window onto* djinn, not the only door into it. Most runs in a real project
 * were started by someone at a terminal and reconstructed from djinn's own
 * logs — `source: "external"` — so nothing here may assume Loom started it.
 * ========================================================================= */

export type RunState = 'running' | 'stopping' | 'stopped' | 'finished' | 'failed'
export type RunSource = 'loom' | 'external'

/** COMPLETE / FAILED / CRASHED are the observed values, but the backend passes
 *  unknown verdicts through rather than dropping them, so this is open. */
export type Verdict = string

export interface CostCaps {
  /** null means "no cap configured" — the real .djinn/config.json has neither. */
  maxCostUsdPerBead: number | null
  maxCostUsdPerSession: number | null
}

export interface DispatchedBead {
  bead: string
  verdict: Verdict
}

export interface SessionSummary {
  iterations: number
  closed: number
  failures: number
  totalCostUsd: number
  /** 0–100. */
  peakContext: number
  /** Wall clock exactly as printed, e.g. "9m47.9s". */
  ran: string
  ranSeconds: number
  /** Empty when the session ended cleanly. */
  halt: string
  /** Empty when the log said "(none)". */
  gateFailures: string[]
  sandbagging?: string
  dispatched: DispatchedBead[]
  filedFollowUps: number
}

export interface RunBead {
  id: string
  spawnedAt: string
  /** null while the worker is still in flight. */
  exitedAt: string | null
  verdict?: Verdict
  /** bd status at exit ("closed", "open") or a halt reason. */
  finalStatus?: string
  retries: number
}

export interface Run {
  id: string
  project: string
  source: RunSource
  startedAt: string
  endedAt: string | null
  state: RunState
  /** summoner argv. Empty for external runs — that is how the UI knows not to
   *  offer a re-run for something Loom never started. */
  args?: string[]
  summary?: SessionSummary
  beads: RunBead[]
  costSoFar: number
  caps?: CostCaps
  exitError?: string
}

/** One state-directory step for a bead. */
export interface TimelineEntry {
  /** Directory name, e.g. "review-impl-loop". */
  loop: string
  /** `loop` with the "-loop" suffix removed, for display. */
  step: string
  iter: number
  /** The verdict file inside the loop dir; empty when none written yet. */
  procedure: string
  mtime: string
}

export interface WorkerSlot {
  bead: string
  spawnedAt: string
  /** null until the worker writes any state. */
  currentStep: TimelineEntry | null
}

export interface RunDetail extends Run {
  activeSlots: WorkerSlot[]
  logPath?: string
}

export interface RunList {
  runs: Run[]
}

/** One procedure output file from .djinn/state. */
export interface ForensicVerdict {
  loop: string
  iter: number
  procedure: string
  /** Set when the file parsed as JSON. */
  json?: unknown
  /** Set when it did not. */
  text?: string
  truncated?: boolean
}

export interface Forensics {
  bead: string
  claimSha?: string
  timeline: TimelineEntry[]
  verdicts: ForensicVerdict[]
}

/* --- summon ---------------------------------------------------------------- */

export interface PreviewWarning {
  bead: string
  reason: string
}

export interface SummonPreview {
  /** Bead ids summoner would dispatch, in order. */
  ordered: string[]
  /** Beads that were asked for but will not run. */
  warnings: PreviewWarning[]
  /** Beads that cannot finish unattended — the LOUD ones. */
  needsOperator: string[]
  /** Whether the deeper LLM preflight actually ran. */
  preflight: boolean
}

export interface SummonPreviewRequest {
  beads?: string[]
  epic?: string
  preflight?: boolean
}

export interface SummonRequest {
  beads?: string[]
  epic?: string
  maxWorkers?: number
  maxBeads?: number
  noPush?: boolean
}

export interface SummonResponse {
  runId: string
  run: Run
}

export interface LogChunk {
  text: string
  /** Byte offset to pass back for the next read. */
  offset: number
  eof: boolean
}

/* --- evidence -------------------------------------------------------------- */

export type CriterionStatus = 'pass' | 'partial' | 'fail' | (string & {})

export interface CriterionProof {
  snapshots: string[]
  observations: string[]
}

export interface Criterion {
  text: string
  status: CriterionStatus
  /** A criterion no code can satisfy — it carries a reason instead. */
  nonCode: boolean
  reason?: string
  /** `path:line` references. */
  implementation: string[]
  verification?: string
  evidence: CriterionProof
  followUp?: string[]
}

export interface Evidence {
  bead: string
  title: string
  description?: string
  criteria: Criterion[]
  followUp: string[]
}

/** GET /api/projects/{p}/x/djinn/beads/{id}/evidence. null until a worker writes one. */
export interface EvidenceResponse {
  evidence: Evidence | null
}

/** GET /api/projects/{p}/x/djinn/spend. */
export interface SpendResponse {
  /** null when no run touched the last seven days; "no runs" is not "$0". */
  last7dUsd: number | null
}

/* --- now ------------------------------------------------------------------- */

export interface NowIssue {
  id: string
  title: string
  status: IssueStatus
  priority: Priority
  ready: boolean
  assignee: string
  startedAt: string | null
}

export interface NowEntry {
  project: string
  issue: NowIssue
  inStatusSince: string | null
  /** updated_at older than 24h. */
  stale: boolean
}

export interface NowResponse {
  entries: NowEntry[]
  /** Projects this answer excludes because they were unreachable. */
  skipped: string[]
}

/* ===========================================================================
 * Graph, query, stale — phase 5
 * ========================================================================= */

export interface GraphNode {
  id: string
  title: string
  status: IssueStatus
  priority: Priority
  type: IssueType
  ready: boolean
  labels: string[]
  /**
   * Outside the requested epic's subtree, pulled in because an edge reaches it.
   * The backend drops externals whose only edge did not survive filtering, so
   * one of these always has a visible reason to be on screen.
   */
  external?: boolean
}

/** Direction is the dependencies table's: `from` depends on `to`. For
 *  parent-child that means the child points at the parent. */
export interface GraphEdge {
  from: string
  to: string
  type: string
}

export interface GraphResponse {
  nodes: GraphNode[]
  edges: GraphEdge[]
  epic?: string
}

/** 413 body when the graph is too big to lay out usefully. */
export interface GraphTooLarge {
  error: string
  nodeCount: number
  limit: number
}

export interface TrendPoint {
  date: string
  opened: number
  closed: number
}

export interface QueryResult {
  issues: Issue[]
  total: number
  query: string
}

export interface StaleIssue extends Issue {
  staleDays: number
}

export interface StaleResponse {
  issues: StaleIssue[]
  days: number
}

/* --- reconciliation sweep ------------------------------------------------ */

export interface SweepSuggestion {
  steps: string[][]
  applicable: boolean
  rationale: string
}

/** The typed intent behind an applicable finding. The server renders
 *  `suggested.steps` from these fields and executes the apply from them too, so
 *  the argv shown and the argv run cannot drift. Clients display the steps; they
 *  never rebuild a command from this. */
export interface SweepApplyIntent {
  issueId?: string
  status?: string
  title?: string
  type?: string
  priority?: number
  description?: string
  sha?: string
}

export interface SweepFinding {
  fingerprint: string
  materialDigest: string
  kind: string
  severity: string
  title: string
  issueIds: string[]
  commits: string[]
  evidence: string[]
  apply: SweepApplyIntent
  suggested: SweepSuggestion
  dismissed: boolean
  dismissedAt: string | null
}

export interface SweepPartial {
  detector: string
  reason: string
}

export interface SweepReport {
  sweptAt: string
  branch: string
  window: number
  commitsScanned: number
  findings: SweepFinding[]
  partial: SweepPartial[]
  counts: Record<string, number>
}

export interface ApplyFindingRequest {
  materialDigest: string
  window?: number
  branch?: string
}

export interface DismissFindingRequest extends ApplyFindingRequest {
  note: string
}

/* --- weekly initiative updates ---------------------------------------------
 *
 * Written by the /initiative-retro skill into bd's key-value space, one key per
 * epic per ISO week. Loom reads them, links their citations, and flips exactly
 * one field on publish. It never drafts one — see the no-generation invariant
 * test in internal/httpapi/no_generation_test.go.
 */

export interface InitiativeUpdate {
  epic: string
  /** ISO week label, `YYYY-Www`. Sorts chronologically as a string. */
  week: string
  /** `draft` or `published`; any other value renders verbatim. */
  status: string
  generatedAt: string
  body: string
  schema: number
  /** sha256 of `body` — echoed on publish so a moved draft is refused. */
  bodySha: string
  /** Non-empty when the stored value would not decode. The row still renders. */
  parseError: string
}

export interface InitiativeUpdatesResponse {
  updates: InitiativeUpdate[]
  /** The cited, id-shaped tokens that name a real issue. Only these link. */
  knownIssueIds: string[]
  /** The cited run ids the project's run history has. Only these link. */
  knownRunIds: string[]
}

export interface InitiativeUpdateSummary {
  epic: string
  total: number
  draftCount: number
  latestWeek: string
  latestStatus: string
}

export interface InitiativeUpdateSummariesResponse {
  updates: InitiativeUpdateSummary[]
}

export interface PublishUpdateRequest {
  bodySha: string
}

export interface PublishUpdateResult {
  published: boolean
  epic: string
  week: string
}
