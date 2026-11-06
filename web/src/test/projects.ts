import type { IntegrationState, Project } from '@/api/types'

/** djinn with every capability: runs, evidence and dispatch. */
export const DJINN: Record<string, IntegrationState> = {
  djinn: { capabilities: ['runs', 'evidence', 'dispatch'] },
}

export const NO_DISPATCH_REASON = 'the summoner binary was not found on PATH; install it to dispatch from Loom'

/** djinn whose summoner binary is missing: history and evidence, no dispatch. */
export const DJINN_NO_DISPATCH: Record<string, IntegrationState> = {
  djinn: { capabilities: ['runs', 'evidence'], reasons: { dispatch: NO_DISPATCH_REASON } },
}

/** A reachable project, with the integrations given (none by default). */
export function testProject(name: string, integrations: Record<string, IntegrationState> = {}): Project {
  return {
    name,
    db: name,
    workdir: `/work/${name}`,
    source: 'config',
    reachable: true,
    reason: '',
    writable: true,
    writeReason: '',
    integrations,
    health: { ready: 1, inProgress: 0, blocked: 0, open: 1, total: 1 },
  }
}
