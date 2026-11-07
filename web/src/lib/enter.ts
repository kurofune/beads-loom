/** The fields of a React keydown event that decide whether Enter may act. */
export interface EnterKeyEvent {
  key: string
  keyCode: number
  nativeEvent: { isComposing: boolean }
}

/**
 * True for an Enter keydown that may submit or act; false for any other key and
 * for an Enter that confirms an IME composition (Japanese, Chinese, Korean).
 * Safari reports isComposing false on the confirming keydown, so keyCode 229
 * also marks a composition. A handler must not call preventDefault on a
 * composing Enter: the IME needs the key to confirm its candidate.
 */
export function isSubmitEnter(event: EnterKeyEvent): boolean {
  if (event.key !== 'Enter') return false
  return !event.nativeEvent.isComposing && event.keyCode !== 229
}
