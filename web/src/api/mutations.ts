import {
  useMutation,
  useQueryClient,
  type InfiniteData,
  type QueryClient,
} from '@tanstack/react-query'
import { api, ApiError } from './client'
import { qk } from './hooks'
import { uiIsBusy } from './sse'
import type {
  BulkResult,
  ApplyFindingRequest,
  CreateIssueRequest,
  DiscoveryRescanResult,
  DiscoveryStatus,
  DismissFindingRequest,
  Issue,
  IssueDetail,
  IssueList,
  IssuePatch,
  IssueStatus,
  PublishUpdateResult,
} from './types'
import { applyDetailPatch, patchList } from '@/lib/optimistic'
import { initiativesRefetch } from '@/lib/rollup-refetch'
import { writes } from '@/lib/write-coordinator'

export function useHideProject() {
  const qc = useQueryClient()
  return useMutation<DiscoveryStatus, ApiError, string>({
    mutationFn: (workdir) => api.discoveryHide(workdir),
    onSuccess: (status) => {
      qc.setQueryData(qk.discovery, status)
      void qc.invalidateQueries({ queryKey: qk.projects })
    },
  })
}

export function useUnhideProject() {
  const qc = useQueryClient()
  return useMutation<DiscoveryStatus, ApiError, string>({
    mutationFn: (workdir) => api.discoveryUnhide(workdir),
    onSuccess: (status) => {
      qc.setQueryData(qk.discovery, status)
      void qc.invalidateQueries({ queryKey: qk.projects })
    },
  })
}

export function useRescanDiscovery() {
  const qc = useQueryClient()
  return useMutation<DiscoveryRescanResult, ApiError, void>({
    mutationFn: () => api.discoveryRescan(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.projects })
      void qc.invalidateQueries({ queryKey: qk.discovery })
    },
  })
}

export interface FindingDigestVars extends ApplyFindingRequest {
  fingerprint: string
}

export function useApplyFinding(project: string) {
  const qc = useQueryClient()
  return useMutation<
    { applied: boolean; fingerprint: string },
    ApiError,
    FindingDigestVars
  >({
    mutationFn: ({ fingerprint, ...body }) =>
      api.applyFinding(project, fingerprint, body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.sweep(project) })
    },
  })
}

export interface DismissFindingVars extends DismissFindingRequest {
  fingerprint: string
}

export function useDismissFinding(project: string) {
  const qc = useQueryClient()
  return useMutation<
    { dismissed: boolean; fingerprint: string },
    ApiError,
    DismissFindingVars
  >({
    mutationFn: ({ fingerprint, ...body }) =>
      api.dismissFinding(project, fingerprint, body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.sweep(project) })
    },
  })
}

export interface PublishUpdateVars {
  epic: string
  week: string
  /** The sha of the body the operator actually read; a moved draft 409s. */
  bodySha: string
}

/**
 * Publish one stored draft.
 *
 * No optimistic flip: publishing is irreversible (a published update is never
 * overwritten), so showing it as published before the server agreed would be a
 * lie the UI cannot take back. Both update queries refetch on success.
 */
export function usePublishUpdate(project: string) {
  const qc = useQueryClient()
  return useMutation<PublishUpdateResult, ApiError, PublishUpdateVars>({
    mutationFn: ({ epic, week, bodySha }) => api.publishUpdate(project, epic, week, { bodySha }),
    onSuccess: (_result, { epic }) => {
      void qc.invalidateQueries({ queryKey: qk.updates(project, epic) })
      void qc.invalidateQueries({ queryKey: qk.updateSummaries(project) })
    },
  })
}

export function useUndismissFinding(project: string) {
  const qc = useQueryClient()
  return useMutation<
    { dismissed: boolean; fingerprint: string },
    ApiError,
    { fingerprint: string }
  >({
    mutationFn: ({ fingerprint }) => api.undismissFinding(project, fingerprint),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.sweep(project) })
    },
  })
}

/**
 * Write hooks.
 *
 * Shape of every one of these:
 *   onMutate   — cancel in-flight reads, snapshot the caches, apply the guess
 *   onError    — restore the snapshot (see Snapshot), then let the caller toast
 *   onSuccess  — write the server's re-read into the detail cache (it is
 *                authoritative: bd normalises values and recomputes readiness)
 *   onSettled  — invalidate lists, stats and project health
 *
 * The snapshot is the *entire* set of matching list queries rather than one
 * key, because the same issue appears in the backlog query, the board query and
 * any filtered variant, and a partial rollback is worse than none.
 */

/**
 * A cached list is `useIssues`' accumulated pages, not one array (loom-7qh).
 * Every helper below therefore walks `pages` — a row the reader reached by
 * loading page two is as patchable as one that arrived on page one.
 */
type CachedList = InfiniteData<IssueList, number>

/**
 * What a refused write puts back. Lists restore whole. Detail sheets
 * restore one at a time, and only the ones this write guessed into: each entry
 * keeps the sheet as it was and the guess written over it. A sheet that no
 * longer holds the guess has newer data from another write, so the rollback
 * leaves it and asks the server instead of reinstating a stale copy.
 */
interface Snapshot {
  lists: [readonly unknown[], CachedList | undefined][]
  details: { key: readonly unknown[]; before: IssueDetail; guess: IssueDetail }[]
}

function restore(qc: QueryClient, snapshot: Snapshot) {
  for (const [key, data] of snapshot.lists) qc.setQueryData(key, data)
  for (const { key, before, guess } of snapshot.details) {
    if (qc.getQueryData(key) === guess) qc.setQueryData(key, before)
    else void qc.invalidateQueries({ queryKey: key, exact: true })
  }
}

/** Apply a patch to every page of every cached list that contains any of `ids`. */
function patchCachedLists(qc: QueryClient, project: string, ids: string[], patch: IssuePatch) {
  qc.setQueriesData<CachedList>({ queryKey: ['issues', project] }, (old) =>
    old
      ? {
          ...old,
          pages: old.pages.map((page) => ({
            ...page,
            issues: patchList(page.issues, ids, patch),
          })),
        }
      : old,
  )
}

/**
 * Snapshot, then write the guess into every cached list and into each open
 * detail sheet of `ids`, so the sheet does not lag the row. A refused write (a
 * 409 claim, a 422 from bd) hands the snapshot to restore, or the sheet shows a
 * status bd never applied.
 */
async function beginOptimistic(
  qc: QueryClient,
  project: string,
  ids: string[],
  patch: IssuePatch,
): Promise<Snapshot> {
  writes.begin()
  // An in-flight refetch that lands after the optimistic write would clobber
  // it with pre-write data, so stop them first.
  await qc.cancelQueries({ queryKey: ['issues', project] })
  const snapshot: Snapshot = {
    lists: qc.getQueriesData<CachedList>({ queryKey: ['issues', project] }),
    details: [],
  }
  patchCachedLists(qc, project, ids, patch)
  for (const id of ids) {
    const key = qk.issue(project, id)
    const before = qc.getQueryData<IssueDetail>(key)
    if (!before) continue
    // Keep what the cache stored, not what was passed in: structural sharing
    // stores a copy, and restore matches the guess by identity.
    const guess = qc.setQueryData<IssueDetail>(key, applyDetailPatch(before, patch))
    if (guess) snapshot.details.push({ key, before, guess })
  }
  return snapshot
}

/**
 * Everything a write can invalidate: lists, stats, and project health counts.
 *
 * Held back while other writes are still in flight (review finding N8): a
 * refetch triggered by mutation A can return pre-B data and revert B's
 * optimistic patch on screen. The coordinator releases the accumulated set once
 * the last write settles.
 *
 * The coordinator only coalesces writes that *overlap*, though — a run of
 * sequential edits settles one at a time and releases one invalidation each.
 * That is right for the lists, which are cheap and must track the edit the user
 * just made; it is wrong for the initiatives rollup, which since loom-g1v.5 the
 * board mounts and which costs an unbounded recursive CTE per ask. So the
 * rollup goes through its own coalescing window (loom-zx1) and the burst pays
 * for one.
 */
function invalidateAfterWrite(qc: QueryClient, project: string) {
  for (const due of writes.end(project)) {
    void qc.invalidateQueries({ queryKey: ['issues', due] })
    void qc.invalidateQueries({ queryKey: qk.stats(due) })
    void qc.invalidateQueries({ queryKey: qk.projects })
    initiativesRefetch.request(due, {
      run: () => void qc.invalidateQueries({ queryKey: ['initiatives', due] }),
      // Both paths share one window per project, so they must share one hold
      // rule too — otherwise whichever asked last would decide whether the
      // refetch respects an open menu.
      hold: uiIsBusy,
    })
  }
}

export interface PatchVars {
  id: string
  patch: IssuePatch
}

export function usePatchIssue(project: string) {
  const qc = useQueryClient()
  return useMutation<IssueDetail, ApiError, PatchVars, Snapshot>({
    mutationFn: ({ id, patch }) => api.patchIssue(project, id, patch),
    onMutate: async ({ id, patch }) => {
      return beginOptimistic(qc, project, [id], patch)
    },
    onError: (_err, _vars, snapshot) => {
      if (snapshot) restore(qc, snapshot)
    },
    onSuccess: (detail) => {
      qc.setQueryData(qk.issue(project, detail.id), detail)
    },
    onSettled: () => invalidateAfterWrite(qc, project),
  })
}

export interface BulkVars {
  ids: string[]
  patch: IssuePatch
}

export function useBulkPatch(project: string) {
  const qc = useQueryClient()
  return useMutation<BulkResult, ApiError, BulkVars, Snapshot>({
    mutationFn: ({ ids, patch }) => api.bulkPatch(project, { ids, patch }),
    onMutate: async ({ ids, patch }) => {
      return beginOptimistic(qc, project, ids, patch)
    },
    onError: (_err, _vars, snapshot) => {
      if (snapshot) restore(qc, snapshot)
    },
    onSettled: () => invalidateAfterWrite(qc, project),
  })
}

export interface CloseVars {
  id: string
  reason: string
  /** Override another actor's claim. Set only after the user confirms the 409. */
  force?: boolean
}

export interface BulkCloseVars {
  ids: string[]
  reason: string
}

/**
 * Close several issues in one request.
 *
 * `bd close` has always accepted multiple ids; only the HTTP surface lacked a
 * bulk verb, so closing 40 selected issues was 40 sequential POSTs and 40
 * invalidate/refetch cycles (review finding N3).
 *
 * The endpoint is landing in parallel, so a "route not there" answer falls back
 * to the per-issue path rather than failing the user's close. That is 404 *or*
 * 405 — chi answers an unregistered POST on an existing path prefix with 405,
 * which is what the live backend returned while this was written. The fallback
 * is one code path away from deletion once the endpoint ships everywhere.
 */
export function useBulkClose(project: string) {
  const qc = useQueryClient()
  return useMutation<BulkResult, ApiError, BulkCloseVars, Snapshot>({
    mutationFn: async ({ ids, reason }) => {
      try {
        return await api.bulkClose(project, { ids, reason })
      } catch (err) {
        const missing = err instanceof ApiError && (err.status === 404 || err.status === 405)
        if (!missing) throw err
        for (const id of ids) await api.closeIssue(project, id, reason)
        return { updated: ids.length, ids }
      }
    },
    onMutate: async ({ ids }) => {
      return beginOptimistic(qc, project, ids, { status: 'closed' })
    },
    onError: (_err, _vars, snapshot) => {
      if (snapshot) restore(qc, snapshot)
    },
    onSettled: () => invalidateAfterWrite(qc, project),
  })
}

export function useCloseIssue(project: string) {
  const qc = useQueryClient()
  return useMutation<IssueDetail, ApiError, CloseVars, Snapshot>({
    mutationFn: ({ id, reason, force }) => api.closeIssue(project, id, reason, force),
    onMutate: async ({ id }) => {
      // `close` is its own bd verb, but the optimistic shape is a status move.
      return beginOptimistic(qc, project, [id], { status: 'closed' })
    },
    onError: (_err, _vars, snapshot) => {
      if (snapshot) restore(qc, snapshot)
    },
    onSuccess: (detail) => qc.setQueryData(qk.issue(project, detail.id), detail),
    onSettled: () => invalidateAfterWrite(qc, project),
  })
}

export interface ReopenVars {
  id: string
  reason?: string
  /**
   * Where the card will end up once the follow-up patch lands. `bd reopen`
   * itself always produces `open`, but showing the card in Open for one frame
   * on its way to In progress is a visible stutter — so the optimistic move
   * goes straight to the final column.
   */
  landsIn?: IssueStatus
}

export function useReopenIssue(project: string) {
  const qc = useQueryClient()
  return useMutation<IssueDetail, ApiError, ReopenVars, Snapshot>({
    mutationFn: ({ id, reason }) => api.reopenIssue(project, id, reason),
    onMutate: async ({ id, landsIn }) => {
      return beginOptimistic(qc, project, [id], { status: landsIn ?? 'open' })
    },
    onError: (_err, _vars, snapshot) => {
      if (snapshot) restore(qc, snapshot)
    },
    onSuccess: (detail) => qc.setQueryData(qk.issue(project, detail.id), detail),
    onSettled: () => invalidateAfterWrite(qc, project),
  })
}

export function useAddComment(project: string) {
  const qc = useQueryClient()
  return useMutation<IssueDetail, ApiError, { id: string; text: string }>({
    // No optimistic insert: bd assigns the comment id and author, and a
    // half-real comment in the feed is more confusing than a 300ms wait.
    mutationFn: ({ id, text }) => api.addComment(project, id, text),
    // Still counted by the coordinator: a comment's refetch can revert another
    // edit's optimistic patch just as easily as a status change can.
    onMutate: () => {
      writes.begin()
    },
    onSuccess: (detail) => qc.setQueryData(qk.issue(project, detail.id), detail),
    onSettled: () => invalidateAfterWrite(qc, project),
  })
}

export function useCreateIssue(project: string) {
  const qc = useQueryClient()
  return useMutation<IssueDetail, ApiError, CreateIssueRequest>({
    mutationFn: (body) => api.createIssue(project, body),
    onMutate: () => {
      writes.begin()
    },
    onSuccess: (detail) => {
      qc.setQueryData(qk.issue(project, detail.id), detail)
    },
    onSettled: () => invalidateAfterWrite(qc, project),
  })
}

/** Read an issue out of any cached list — used by menus that need current
 *  labels without a fetch. */
export function cachedIssue(qc: QueryClient, project: string, id: string): Issue | undefined {
  for (const [, data] of qc.getQueriesData<CachedList>({ queryKey: ['issues', project] })) {
    for (const page of data?.pages ?? []) {
      const found = page.issues.find((i) => i.id === id)
      if (found) return found
    }
  }
  return undefined
}
