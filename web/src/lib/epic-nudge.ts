import type { Initiative } from '@/api/types'

/** A dismissal belongs to this exact eligible set, independent of query order. */
export function epicNudgeSignature(eligible: readonly Pick<Initiative, 'id'>[]): string {
  return eligible.map((initiative) => initiative.id).sort().join(',')
}

export function isEpicNudgeDismissed(stored: string | null, signature: string): boolean {
  return signature.length > 0 && stored === signature
}
