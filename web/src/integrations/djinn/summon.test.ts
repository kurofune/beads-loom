import { describe, expect, it } from 'vitest'
import type { SummonPreview } from '@/api/types'
import {
  canDispatch,
  DEFAULT_KNOBS,
  dispatchBody,
  droppedBeads,
  summonReducer,
  type SummonEvent,
  type SummonPhase,
} from './summon'

const preview = (over: Partial<SummonPreview> = {}): SummonPreview => ({
  ordered: ['loom-a', 'loom-b'],
  warnings: [],
  needsOperator: [],
  preflight: false,
  ...over,
})

const run = (events: SummonEvent[], from: SummonPhase = { phase: 'idle' }) =>
  events.reduce(summonReducer, from)

describe('summonReducer', () => {
  it('walks tray → preview → confirm → dispatching', () => {
    const state = run([
      { type: 'preview', preflight: false },
      { type: 'previewed', preview: preview() },
      { type: 'proceed', knobs: DEFAULT_KNOBS },
      { type: 'confirm' },
    ])
    expect(state.phase).toBe('dispatching')
  })

  describe('never auto-summon', () => {
    // The single most important property of this flow. `dispatching` spawns
    // autonomous agents and spends real money, so it must be unreachable except
    // through an explicit confirm from the confirm screen.
    it('cannot reach dispatching from idle', () => {
      expect(run([{ type: 'confirm' }]).phase).toBe('idle')
    })

    it('cannot reach dispatching straight from a preview, however clean', () => {
      const state = run([
        { type: 'preview', preflight: false },
        { type: 'previewed', preview: preview({ warnings: [], needsOperator: [] }) },
        { type: 'confirm' },
      ])
      expect(state.phase).toBe('preview')
    })

    it('cannot reach dispatching while a preview is still in flight', () => {
      expect(run([{ type: 'preview', preflight: false }, { type: 'confirm' }]).phase).toBe(
        'previewing',
      )
    })

    it('a previewed result never lands anywhere but the preview sheet', () => {
      for (const p of [preview(), preview({ ordered: [] }), preview({ needsOperator: ['x'] })]) {
        expect(run([{ type: 'previewed', preview: p }]).phase).toBe('preview')
      }
    })
  })

  it('records which kind of preview is running, for the spinner copy', () => {
    const state = run([{ type: 'preview', preflight: true }])
    expect(state).toEqual({ phase: 'previewing', preflight: true })
  })

  it('adjusts knobs only on the confirm screen', () => {
    const confirmed = run([
      { type: 'previewed', preview: preview() },
      { type: 'proceed', knobs: DEFAULT_KNOBS },
      { type: 'knobs', knobs: { maxWorkers: 8, noPush: true } },
    ])
    expect(confirmed).toMatchObject({
      phase: 'confirm',
      knobs: { maxWorkers: 8, maxBeads: 0, noPush: true },
    })
  })

  it('ignores knob changes outside the confirm screen', () => {
    const previewed = run([{ type: 'previewed', preview: preview() }])
    expect(summonReducer(previewed, { type: 'knobs', knobs: { maxWorkers: 9 } })).toBe(previewed)
  })

  it('goes backwards on cancel, never forwards', () => {
    const back = run([
      { type: 'previewed', preview: preview() },
      { type: 'proceed', knobs: DEFAULT_KNOBS },
      { type: 'back' },
    ])
    expect(back.phase).toBe('preview')
    expect(summonReducer(back, { type: 'back' }).phase).toBe('idle')
  })

  it('returns to idle once dispatched, so the tray can clear', () => {
    const state = run([
      { type: 'previewed', preview: preview() },
      { type: 'proceed', knobs: DEFAULT_KNOBS },
      { type: 'confirm' },
      { type: 'dispatched' },
    ])
    expect(state.phase).toBe('idle')
  })

  it('carries the failure message and bd stderr', () => {
    const state = run([{ type: 'failed', message: 'summoner not found in PATH', detail: 'exec: …' }])
    expect(state).toEqual({
      phase: 'error',
      message: 'summoner not found in PATH',
      detail: 'exec: …',
    })
  })

  it('resets from anywhere', () => {
    expect(run([{ type: 'failed', message: 'x' }, { type: 'reset' }]).phase).toBe('idle')
  })
})

describe('canDispatch', () => {
  it('refuses an empty dispatch list even when there are no warnings', () => {
    expect(canDispatch(preview({ ordered: [] }))).toBe(false)
    expect(canDispatch(preview())).toBe(true)
  })
})

describe('dispatchBody', () => {
  it('sends the preview’s ordered list, not the tray’s staged ids', () => {
    // summoner has already told us which beads it would run; dispatching the
    // ones it warned about would ignore the answer we just asked for.
    const body = dispatchBody(preview({ ordered: ['loom-a'] }), DEFAULT_KNOBS)
    expect(body.beads).toEqual(['loom-a'])
  })

  it('passes the knobs through, including maxBeads 0 meaning unbounded', () => {
    const body = dispatchBody(preview(), { maxWorkers: 5, maxBeads: 0, noPush: true })
    expect(body).toMatchObject({ maxWorkers: 5, maxBeads: 0, noPush: true })
  })

  it('includes an epic only when there is one', () => {
    expect(dispatchBody(preview(), DEFAULT_KNOBS)).not.toHaveProperty('epic')
    expect(dispatchBody(preview(), DEFAULT_KNOBS, 'loom-3ft')).toMatchObject({ epic: 'loom-3ft' })
  })

  it('defaults to summoner’s own 3 workers', () => {
    expect(DEFAULT_KNOBS.maxWorkers).toBe(3)
  })
})

describe('droppedBeads', () => {
  it('explains each staged bead the preview will not run', () => {
    const dropped = droppedBeads(
      ['loom-a', 'loom-b', 'loom-c'],
      preview({
        ordered: ['loom-a'],
        warnings: [{ bead: 'loom-b', reason: 'not ready: blocked by 2 dependencies' }],
      }),
    )
    expect(dropped).toEqual([
      { bead: 'loom-b', reason: 'not ready: blocked by 2 dependencies' },
      // No warning came back for loom-c, but it is still missing from the
      // dispatch list — saying so beats silently omitting it.
      { bead: 'loom-c', reason: 'not dispatched (no reason given)' },
    ])
  })

  it('is empty when everything staged will run', () => {
    expect(droppedBeads(['loom-a', 'loom-b'], preview())).toEqual([])
  })
})
