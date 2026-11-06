import { djinn } from './djinn'
import type { Integration } from './types'

/**
 * The registry: every integration this build can mount. This list is the only
 * place core learns that an integration exists. A function, not a constant:
 * integration modules import core modules that import this one, and a
 * constant read at load time would see an integration still initialising.
 */
export function registeredIntegrations(): readonly Integration[] {
  return [djinn]
}
