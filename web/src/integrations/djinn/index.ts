import type { Integration } from '../types'
import { DjinnBadge, EvidenceSection, SpendTile } from './contributions'
import { djinnSse } from './live'
import { RunsView } from './RunsView'
import {
  requestSummon,
  StageBulkAction,
  StageIssueAction,
  TrayProvider,
  TrayRoute,
  useDjinnWorkspace,
} from './staging'

/** The capabilities internal/integration/djinn reports. */
export const CAP_RUNS = 'runs'
export const CAP_EVIDENCE = 'evidence'
export const CAP_DISPATCH = 'dispatch'

/** The views a workspace (and so the summon flow) lives in. */
const WORKSPACE_VIEWS = new Set(['backlog', 'board', 'initiatives'])

/**
 * djinn: staging, summoning, run history, evidence and spend, for projects
 * with a .djinn directory.
 */
export const djinn: Integration = {
  id: 'djinn',

  views: [
    {
      id: 'runs',
      label: 'Runs',
      path: 'runs',
      routes: ['runs', 'runs/:runId'],
      element: RunsView,
      chord: 'g r',
      capability: CAP_RUNS,
      surface: { label: 'Runs', shortcuts: ['row.next', 'row.prev'] },
    },
  ],

  projectBadge: { component: DjinnBadge },

  issueActions: [{ id: 'stage', component: StageIssueAction }],

  bulkActions: [{ id: 'stage', component: StageBulkAction }],

  detailSections: [{ id: 'evidence', component: EvidenceSection, capability: CAP_EVIDENCE }],

  // Spend is summed from run history.
  statTiles: [{ id: 'spend', component: SpendTile, capability: CAP_RUNS }],

  root: { component: TrayProvider, route: TrayRoute },

  labels: [
    {
      name: 'needs-operator',
      tone: 'operator',
      hint: 'needs-operator — summoner cannot complete this unattended',
      seed: true,
    },
  ],

  sse: djinnSse,

  paletteCommands: [
    {
      id: 'view:runs',
      group: 'Views',
      label: 'Go to runs',
      hint: 'g r',
      keywords: 'summoner djinn dispatch log',
      capability: CAP_RUNS,
      run: (host) => host.goView('runs'),
    },
    {
      id: 'action:summon',
      group: 'Actions',
      label: 'Summon djinn',
      keywords: 'dispatch run workers agents preview',
      capability: CAP_DISPATCH,
      run: (host) => {
        requestSummon()
        if (!WORKSPACE_VIEWS.has(host.currentView)) host.goView('backlog')
      },
    },
  ],

  shortcuts: [
    {
      shortcut: {
        id: 'issue.stage',
        surfaces: ['backlog', 'board'],
        keys: ['q'],
        label: 'Stage the focused or selected issues for djinn, or unstage them if all are queued',
        group: 'Issue',
        alsoBy: 'Stage in the bulk bar or the issue sheet',
      },
    },
    {
      capability: CAP_RUNS,
      shortcut: {
        id: 'view.runs',
        keys: ['g', 'r'],
        label: 'Go to runs',
        group: 'View',
        alsoBy: 'Click Runs in the top bar',
      },
      run: (host) => host.goView('runs'),
    },
  ],

  workspace: { use: useDjinnWorkspace },
}
