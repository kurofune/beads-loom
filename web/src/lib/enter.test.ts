import { describe, expect, it } from 'vitest'
import { isSubmitEnter, type EnterKeyEvent } from './enter'

function keydown(init: KeyboardEventInit): EnterKeyEvent {
  const nativeEvent = new KeyboardEvent('keydown', init)
  return { key: nativeEvent.key, keyCode: nativeEvent.keyCode, nativeEvent }
}

describe('isSubmitEnter', () => {
  it('accepts a plain Enter', () => {
    expect(isSubmitEnter(keydown({ key: 'Enter' }))).toBe(true)
  })

  it('rejects an Enter that confirms an IME composition (isComposing)', () => {
    expect(isSubmitEnter(keydown({ key: 'Enter', isComposing: true }))).toBe(false)
  })

  it('rejects an Enter with keyCode 229, as Safari reports the confirming keydown', () => {
    expect(isSubmitEnter(keydown({ key: 'Enter', keyCode: 229 }))).toBe(false)
  })

  it('rejects a key that is not Enter', () => {
    expect(isSubmitEnter(keydown({ key: 'a' }))).toBe(false)
  })
})
