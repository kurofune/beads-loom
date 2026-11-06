import type { SummonPreview, SummonRequest } from '@/api/types'

/**
 * The dispatch state machine: tray → preview → confirm → dispatched.
 *
 * Modelled explicitly because this is the one flow in Loom that spends real
 * money and starts autonomous agents against a real repository. The invariant
 * the work package states — **NEVER auto-summon** — is easier to guarantee in a
 * machine where `dispatching` is unreachable except through `confirm`, than in
 * a pile of booleans where one stray effect can skip a step.
 */
export type SummonPhase =
  | { phase: 'idle' }
  /** Preview requested; `preflight` says which kind is in flight. */
  | { phase: 'previewing'; preflight: boolean }
  | { phase: 'preview'; preview: SummonPreview }
  | { phase: 'confirm'; preview: SummonPreview; knobs: SummonKnobs }
  | { phase: 'dispatching'; preview: SummonPreview; knobs: SummonKnobs }
  | { phase: 'error'; message: string; detail?: string }

export interface SummonKnobs {
  maxWorkers: number
  /** 0 = unbounded, matching summoner's own --max-beads semantics. */
  maxBeads: number
  noPush: boolean
}

/** summoner's own default is 3 workers (verified --help, ARCHITECTURE.md). */
export const DEFAULT_KNOBS: SummonKnobs = { maxWorkers: 3, maxBeads: 0, noPush: false }

export type SummonEvent =
  | { type: 'preview'; preflight: boolean }
  | { type: 'previewed'; preview: SummonPreview }
  | { type: 'proceed'; knobs: SummonKnobs }
  | { type: 'knobs'; knobs: Partial<SummonKnobs> }
  | { type: 'back' }
  | { type: 'confirm' }
  | { type: 'dispatched' }
  | { type: 'failed'; message: string; detail?: string }
  | { type: 'reset' }

export function summonReducer(state: SummonPhase, event: SummonEvent): SummonPhase {
  switch (event.type) {
    case 'preview':
      return { phase: 'previewing', preflight: event.preflight }

    case 'previewed':
      // A preview always lands on the preview sheet — never straight into
      // dispatch, however clean it looks.
      return { phase: 'preview', preview: event.preview }

    case 'proceed':
      return state.phase === 'preview'
        ? { phase: 'confirm', preview: state.preview, knobs: event.knobs }
        : state

    case 'knobs':
      return state.phase === 'confirm'
        ? { ...state, knobs: { ...state.knobs, ...event.knobs } }
        : state

    case 'back':
      // Cancel is the safe action, and it always goes backwards, never forwards.
      if (state.phase === 'confirm') return { phase: 'preview', preview: state.preview }
      return { phase: 'idle' }

    case 'confirm':
      // The ONLY transition into dispatching, and it is reachable only from an
      // explicit confirm. This is where "never auto-summon" is enforced.
      return state.phase === 'confirm'
        ? { phase: 'dispatching', preview: state.preview, knobs: state.knobs }
        : state

    case 'dispatched':
      return { phase: 'idle' }

    case 'failed':
      return { phase: 'error', message: event.message, ...(event.detail ? { detail: event.detail } : {}) }

    case 'reset':
      return { phase: 'idle' }
  }
}

/** Is a preview worth dispatching at all? */
export const canDispatch = (preview: SummonPreview): boolean => preview.ordered.length > 0

/**
 * Build the dispatch body.
 *
 * Sends the **preview's** ordered list, not the tray's staged ids: summoner has
 * already told us which of them it would actually run, and dispatching beads it
 * warned about would ignore the answer we just asked for.
 */
export function dispatchBody(
  preview: SummonPreview,
  knobs: SummonKnobs,
  epic?: string,
): SummonRequest {
  return {
    beads: preview.ordered,
    ...(epic ? { epic } : {}),
    maxWorkers: knobs.maxWorkers,
    maxBeads: knobs.maxBeads,
    noPush: knobs.noPush,
  }
}

/** Beads the user staged that the preview will not run, with the reason. */
export function droppedBeads(
  staged: readonly string[],
  preview: SummonPreview,
): { bead: string; reason: string }[] {
  const ordered = new Set(preview.ordered)
  const reasons = new Map(preview.warnings.map((w) => [w.bead, w.reason]))
  return staged
    .filter((bead) => !ordered.has(bead))
    .map((bead) => ({ bead, reason: reasons.get(bead) ?? 'not dispatched (no reason given)' }))
}
