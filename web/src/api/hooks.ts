import { useMemo } from 'react'
import {
  useInfiniteQuery,
  useQuery,
  type InfiniteData,
  type QueryKey,
  type UseQueryResult,
} from '@tanstack/react-query'
import { api, ApiError } from './client'
import { dedupeById, nextOffset, pageSizeAt, stalled } from '@/lib/paging'
import { MIN_SEARCH_LENGTH } from '@/lib/palette'
import type {
  DoltStatus,
  DiscoveryStatus,
  Health,
  InitiativesResponse,
  InitiativeUpdateSummariesResponse,
  InitiativeUpdatesResponse,
  QueryResult,
  StaleResponse,
  IssueDetail,
  IssueFilter,
  IssueList,
  Project,
  ProjectSchema,
  Stats,
  SweepReport,
} from './types'
import { BUILTIN_SCHEMA } from './types'

/** One place to derive cache keys, so invalidation in phase 3 has a target. */
export const qk = {
  health: ['health'] as const,
  projects: ['projects'] as const,
  discovery: ['discovery'] as const,
  issues: (project: string, filter: IssueFilter) => ['issues', project, filter] as const,
  // Its own key, not an `issues` filter: the palette's search is short-lived
  // and per-keystroke, and must not evict a mounted view's accumulated pages.
  issueSearch: (project: string, q: string) => ['issue-search', project, q] as const,
  issue: (project: string, id: string) => ['issue', project, id] as const,
  stats: (project: string) => ['stats', project] as const,
  schema: (project: string) => ['schema', project] as const,
  initiatives: (project: string, includeClosed: boolean) =>
    ['initiatives', project, includeClosed] as const,
  updateSummaries: (project: string) => ['update-summaries', project] as const,
  updates: (project: string, epic: string) => ['updates', project, epic] as const,
  dolt: (project: string) => ['dolt', project] as const,
  graph: (project: string, epic: string, includeClosed: boolean) =>
    ['graph', project, epic, includeClosed] as const,
  query: (project: string, q: string) => ['query', project, q] as const,
  stale: (project: string, days: number) => ['stale', project, days] as const,
  sweep: (project: string) => ['sweep', project] as const,
  now: ['now'] as const,
}

/**
 * A 404 means "this project/issue doesn't exist" — retrying cannot fix it. A
 * query timeout already cost its full deadline; three silent retries would
 * hold the error back for minutes, so the reader gets it and a Retry button.
 */
const retry = (failureCount: number, error: unknown) => {
  if (error instanceof ApiError && error.status >= 400 && error.status < 500) return false
  if (error instanceof ApiError && error.isQueryTimeout) return false
  return failureCount < 2
}

export function useHealth(): UseQueryResult<Health, ApiError> {
  return useQuery<Health, ApiError>({
    queryKey: qk.health,
    queryFn: () => api.health(),
    retry,
    staleTime: 30_000,
  })
}

export function useProjects(): UseQueryResult<Project[], ApiError> {
  return useQuery<Project[], ApiError>({
    queryKey: qk.projects,
    queryFn: () => api.projects(),
    retry,
    // No polling: `project-health` events patch this cache directly — counts,
    // reachability and write state — so a refetch here would undo the point of
    // the backend's per-project poller.
    staleTime: 10_000,
  })
}

/**
 * Why writes to this project are refused, or null while they are allowed.
 *
 * Every write control reads this rather than taking a prop, so no write path
 * can forget it. Unknown (projects still loading or failed) counts as allowed:
 * the server still answers 409 if the project is read-only.
 */
export function useWriteBlock(project: string): string | null {
  const { data } = useProjects()
  const p = data?.find((x) => x.name === project)
  if (!p || p.writable !== false) return null
  return p.writeReason || 'Loom and bd disagree on this project\'s database, so writes are disabled.'
}

/**
 * The statuses and types this project accepts, for the create and edit menus.
 *
 * Never empty and never an error: while the request is in flight, and when it
 * fails, the menus offer bd's built-ins, which every project accepts. Fetched
 * once per project — the vocabulary changes only when someone runs
 * `bd config set`, and a reconnect (sse.ts) refetches everything anyway.
 */
export function useProjectSchema(project: string): ProjectSchema {
  const { data } = useQuery<ProjectSchema, ApiError>({
    queryKey: qk.schema(project),
    queryFn: () => api.schema(project),
    enabled: project !== '',
    retry: false,
    // A failed schema stays on the built-ins rather than asking again on
    // every menu open; a reconnect (applyActions resets it) or reload refetches it.
    retryOnMount: false,
    staleTime: Infinity,
    gcTime: Infinity,
  })
  return data ?? BUILTIN_SCHEMA
}

export function useDiscovery(): UseQueryResult<DiscoveryStatus, ApiError> {
  return useQuery<DiscoveryStatus, ApiError>({
    queryKey: qk.discovery,
    queryFn: () => api.discovery(),
    retry,
    staleTime: 30_000,
  })
}

export interface UseIssuesOptions {
  /** The backend excludes closed by default; this fetches them in addition. */
  includeClosed?: boolean
  /** Restrict the server request to this epic's transitive descendants. */
  epic?: string
  filter?: IssueFilter
  enabled?: boolean
}

export interface IssuesResult {
  /** Every row fetched so far, across pages, each id appearing once. */
  issues: IssueList['issues']
  /** Server-reported total matching the request, which may exceed the rows. */
  total: number
  /** Rows actually in hand — below `total` until the reader loads the rest. */
  loaded: number
  /** True while the server holds rows this hook has not asked for yet. */
  hasMore: boolean
  /**
   * Paging gave up while the server still counts rows that are not in hand.
   *
   * The other half of `hasMore` being false. Converged means the reader has
   * everything; stalled means a seam duplicate (or a server that stopped
   * sending) left the accumulated set short of `total` with no forward request
   * that can close the gap — the state where the view must offer a refetch
   * rather than let the load-more control simply vanish (loom-rz0).
   */
  stalled: boolean
  /** Fetch the next page and append it. A no-op once `hasMore` is false. */
  loadMore: () => void
  /** A page after the first is in flight. */
  isLoadingMore: boolean
  isLoading: boolean
  isFetching: boolean
  error: ApiError | null
  /**
   * The failure of a page *after* the first — the one failure that leaves the
   * rows in hand intact and true.
   *
   * Split out from `error` because the two demand opposite treatment: a first
   * page that fails leaves nothing to show, while a page-2 failure touched
   * none of the rows already on screen. A consumer that cannot tell them apart
   * blanks a loaded table over a failure that cost it nothing.
   */
  loadMoreError: ApiError | null
  refetch: () => void
}

/**
 * Backlog rows, one page at a time.
 *
 * Phase 1 fetched closed issues as a second request because the contract did
 * not pin the status-list separator. It does: `filterFromQuery` accepts both
 * comma-separated and repeated params, and exposes an explicit `includeClosed`
 * flag — so a page is one request.
 *
 * It is an *infinite* query rather than a plain one because the server caps a
 * page (`queries.MaxLimit`), so no single request can answer for a project or
 * an epic subtree past that size. Accumulating pages under one cache key keeps
 * the whole result set in `issues`, which is what everything downstream —
 * grouping, the quick filter, the j/k walk order, multi-select — is written
 * against. Callers that never call `loadMore` behave exactly as before: one
 * page, one request. See `@/lib/paging` for the page sizes and the offset
 * arithmetic, and loom-7qh's design notes for why not prev/next or scroll.
 */
export function useIssues(project: string, options: UseIssuesOptions = {}): IssuesResult {
  const { includeClosed = false, epic, filter = {}, enabled = true } = options
  const effective: IssueFilter = {
    ...filter,
    ...(includeClosed ? { includeClosed: true } : {}),
    ...(epic ? { epic } : {}),
  }

  const query = useInfiniteQuery<IssueList, ApiError, InfiniteData<IssueList>, QueryKey, number>({
    queryKey: qk.issues(project, effective),
    // A caller that cannot paginate (the board, which is a sample by design)
    // pins its own page size; everyone else gets this view's page ladder.
    queryFn: ({ pageParam }) =>
      api.issues(project, {
        ...effective,
        limit: effective.limit ?? pageSizeAt(pageParam),
        ...(pageParam > 0 ? { offset: pageParam } : {}),
      }),
    initialPageParam: 0,
    getNextPageParam: (_last, pages) => nextOffset(pages),
    retry,
    enabled: enabled && Boolean(project),
    staleTime: 5_000,
  })

  // Memoised on the cache entry, not recomputed per render: `issues` is the
  // dependency of every downstream useMemo in the Backlog and the Board, so a
  // fresh array identity on each render would re-run the quick filter, the
  // grouping and the lane build over the whole accumulated set every time a
  // keystroke or a focus move re-renders the view.
  const { issues, total, pagingStalled } = useMemo(() => {
    const pages = query.data?.pages ?? []
    const rows = dedupeById(pages.flatMap((page) => page.issues))
    return {
      issues: rows,
      total: pages[pages.length - 1]?.total ?? rows.length,
      pagingStalled: stalled(pages),
    }
  }, [query.data])

  return {
    issues,
    total,
    loaded: issues.length,
    hasMore: query.hasNextPage,
    stalled: pagingStalled,
    loadMore: () => {
      if (query.hasNextPage && !query.isFetchingNextPage) void query.fetchNextPage()
    },
    isLoadingMore: query.isFetchingNextPage,
    isLoading: query.isLoading,
    isFetching: query.isFetching,
    error: query.error ?? null,
    loadMoreError: query.isFetchNextPageError ? (query.error ?? null) : null,
    refetch: () => void query.refetch(),
  }
}

/* --- palette search ------------------------------------------------------- */

/**
 * How many matches one search asks the server for.
 *
 * A palette lists a couple of dozen rows before it stops being a palette, so
 * this is deliberately far below `queries.MaxLimit`. It is not the corpus — the
 * corpus is the whole project, because the *matching* happens in SQL — it is
 * how many of the matches come back, and the palette states the difference
 * when there are more (loom-nlm).
 */
export const SEARCH_PAGE = 50

export interface IssueSearchResult {
  /** The matches the server returned, best-effort ordered by the list sort. */
  issues: IssueList['issues']
  /** How many issues in the project match, which may exceed `issues.length`. */
  total: number
  /** True when the server holds matches this response did not carry. */
  truncated: boolean
  /** True once the query is long enough that a search was actually asked for. */
  active: boolean
  isSearching: boolean
  error: ApiError | null
  /** Ask again after a failure. */
  retry: () => void
}

/**
 * Search every issue in a project, in SQL.
 *
 * The palette used to search a client-side array built from one default page,
 * so on any project past 200 issues "Jump to…" quietly could not reach most of
 * it (loom-nlm). Matching belongs where the rows are: the server ANDs the
 * typed words across title and id, so the reachable set is the project rather
 * than whatever happened to sort first.
 *
 * Closed work is included on purpose. Looking a bead up by id is the palette's
 * job whether or not the bead is still open; the rows say which is which.
 */
export function useIssueSearch(project: string, query: string): IssueSearchResult {
  const q = query.trim()
  const active = Boolean(project) && q.length >= MIN_SEARCH_LENGTH

  const search = useQuery<IssueList, ApiError>({
    queryKey: qk.issueSearch(project, q),
    queryFn: () => api.issues(project, { q, includeClosed: true, limit: SEARCH_PAGE }),
    retry,
    enabled: active,
    staleTime: 15_000,
    // Keep the previous query's rows on screen while the next one is in
    // flight, so the list narrows as you type instead of blinking empty.
    placeholderData: (previous) => previous,
  })

  const issues = active ? (search.data?.issues ?? []) : []
  const total = active ? (search.data?.total ?? issues.length) : 0

  return {
    issues,
    total,
    truncated: total > issues.length,
    active,
    isSearching: active && search.isFetching,
    error: search.error ?? null,
    retry: () => void search.refetch(),
  }
}

export function useIssue(
  project: string,
  id: string | undefined,
): UseQueryResult<IssueDetail, ApiError> {
  return useQuery<IssueDetail, ApiError>({
    queryKey: qk.issue(project, id ?? ''),
    queryFn: () => api.issue(project, id!),
    retry,
    enabled: Boolean(project && id),
    staleTime: 5_000,
  })
}

/**
 * Dolt sync state. Refetched only when SSE says the db moved (the invalidation
 * covers `qk.dolt`), so an idle project costs nothing.
 */
export function useDoltStatus(
  project: string,
  enabled = true,
): UseQueryResult<DoltStatus, ApiError> {
  return useQuery<DoltStatus, ApiError>({
    queryKey: qk.dolt(project),
    queryFn: () => api.doltStatus(project),
    retry,
    enabled: enabled && Boolean(project),
    staleTime: 30_000,
  })
}

/** Stale issues, stalest first. Closed and deferred work is excluded by the
 *  backend: something nobody has touched because it is finished or parked on
 *  purpose is not stale. */
export function useStale(
  project: string,
  days = 14,
  enabled = true,
): UseQueryResult<StaleResponse, ApiError> {
  return useQuery<StaleResponse, ApiError>({
    queryKey: qk.stale(project, days),
    queryFn: () => api.stale(project, days),
    retry,
    enabled: enabled && Boolean(project),
    staleTime: 60_000,
  })
}

/**
 * `bd query` passthrough.
 *
 * Only fires for a non-empty expression, so switching into raw mode does not
 * immediately ask bd to parse "". Errors carry bd's own parse message, which is
 * rendered verbatim — bd explains its own syntax better than we could.
 */
export function useBdQuery(
  project: string,
  expression: string,
  enabled = true,
): UseQueryResult<QueryResult, ApiError> {
  const q = expression.trim()
  return useQuery<QueryResult, ApiError>({
    queryKey: qk.query(project, q),
    queryFn: () => api.query(project, q),
    // A syntax error is the user mid-sentence, not a transient failure.
    retry: false,
    enabled: enabled && Boolean(project) && q.length > 0,
    staleTime: 5_000,
  })
}

export function useStats(project: string, enabled = true): UseQueryResult<Stats, ApiError> {
  return useQuery<Stats, ApiError>({
    queryKey: qk.stats(project),
    queryFn: () => api.stats(project),
    retry,
    enabled: enabled && Boolean(project),
    staleTime: 15_000,
  })
}

export function useInitiatives(
  project: string,
  includeClosed: boolean,
  enabled = true,
): UseQueryResult<InitiativesResponse, ApiError> {
  return useQuery<InitiativesResponse, ApiError>({
    queryKey: qk.initiatives(project, includeClosed),
    queryFn: () => api.initiatives(project, includeClosed),
    retry,
    enabled: enabled && Boolean(project),
    staleTime: 15_000,
  })
}

/**
 * Stored weekly updates for every epic, in one request.
 *
 * The Initiatives table joins this by epic id rather than asking per row: a
 * project with 30 epics would otherwise open 30 connections to show 30 chips.
 */
export function useInitiativeUpdateSummaries(
  project: string,
  enabled = true,
): UseQueryResult<InitiativeUpdateSummariesResponse, ApiError> {
  return useQuery<InitiativeUpdateSummariesResponse, ApiError>({
    queryKey: qk.updateSummaries(project),
    queryFn: () => api.initiativeUpdateSummaries(project),
    retry,
    enabled: enabled && Boolean(project),
    staleTime: 60_000,
  })
}

/**
 * One epic's full update history. Enabled only while the sheet is open, so a
 * year of markdown is not fetched to render a table.
 */
export function useInitiativeUpdates(
  project: string,
  epic: string | undefined,
  enabled = true,
): UseQueryResult<InitiativeUpdatesResponse, ApiError> {
  return useQuery<InitiativeUpdatesResponse, ApiError>({
    queryKey: qk.updates(project, epic ?? ''),
    queryFn: () => api.initiativeUpdates(project, epic!),
    retry,
    enabled: enabled && Boolean(project && epic),
    staleTime: 30_000,
  })
}

/** A reconciliation sweep is manual: mounting the queue never runs git or bd. */
export function useSweep(project: string): UseQueryResult<SweepReport, ApiError> {
  return useQuery<SweepReport, ApiError>({
    queryKey: qk.sweep(project),
    queryFn: () => api.sweep(project),
    // One sweep is already bounded at 30s. Retrying a manual action would hold
    // the in-flight state for up to 90s before revealing the useful stderr.
    retry: false,
    enabled: false,
  })
}
