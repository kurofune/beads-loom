/**
 * Vitest environment shim: every Node version tests against jsdom's own Storage.
 *
 * vitest's jsdom environment copies `localStorage` from the jsdom window onto
 * globalThis only when globalThis does not already have one. Node 22 and 24 have none,
 * so tests get jsdom's Storage. Node 25+ defines its own `localStorage`, which
 * is undefined without `--localstorage-file`, so vitest skips the copy and the
 * global stays undefined. Point that global at the same jsdom Storage, so a
 * `vi.spyOn(Storage.prototype, ...)` intercepts on every runner.
 */
declare global {
  var jsdom: { window: Window } | undefined
}

for (const name of ['localStorage', 'sessionStorage'] as const) {
  if (typeof globalThis[name] === 'undefined') {
    const storage = globalThis.jsdom?.window[name]
    if (!storage) throw new Error(`vitest setup: jsdom does not provide ${name}`)
    Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: storage })
  }
}

export {}
