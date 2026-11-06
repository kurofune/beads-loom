import { createContext, useContext, type ReactNode } from 'react'
import type { LabelRule } from '@/lib/beads'

const NO_RULES: readonly LabelRule[] = []

const LabelRules = createContext<readonly LabelRule[]>(NO_RULES)

/**
 * The label rules of the open project's mounted integrations. The shell
 * provides them once, so every chip under it — rows, cards, the issue sheet,
 * the pickers — draws a label the same way.
 */
export function LabelRulesProvider({ rules, children }: { rules: readonly LabelRule[]; children: ReactNode }) {
  return <LabelRules.Provider value={rules}>{children}</LabelRules.Provider>
}

export function useLabelRules(): readonly LabelRule[] {
  return useContext(LabelRules)
}
