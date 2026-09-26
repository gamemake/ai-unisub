import type { SubscriptionPlan } from '@/data/types'

export interface SubscriptionPlanOption {
  id: SubscriptionPlan
  label: string
}

const codexPlans: SubscriptionPlanOption[] = [
  { id: 'codex_plus', label: 'Plus' },
  { id: 'codex_pro_5x', label: 'Pro 5x' },
  { id: 'codex_pro_20x', label: 'Pro 20x' },
]

const claudePlans: SubscriptionPlanOption[] = [
  { id: 'claude_pro', label: 'Pro' },
  { id: 'claude_max_5x', label: 'Max 5x' },
  { id: 'claude_max_20x', label: 'Max 20x' },
]

const grokPlans: SubscriptionPlanOption[] = [
  { id: 'super_grok', label: 'SuperGrok' },
  { id: 'super_grok_plus', label: 'SuperGrok Plus' },
  { id: 'super_grok_heavy', label: 'SuperGrok Heavy' },
]

/** Plans owned by a subscription supplier id (openai / anthropic / xai). Dummy uses Claude plans. */
export function subscriptionPlansForSupplier(supplierID: string): SubscriptionPlanOption[] {
  switch (supplierID) {
    case 'openai':
      return codexPlans
    case 'anthropic':
    case 'dummy':
      return claudePlans
    case 'xai':
      return grokPlans
    default:
      return []
  }
}

export function samePlanWeights(a: Record<string, number> = {}, b: Record<string, number> = {}) {
  const keys = new Set([...Object.keys(a), ...Object.keys(b)])
  for (const key of keys) {
    if ((a[key] ?? 0) !== (b[key] ?? 0)) return false
  }
  return true
}

export function defaultSubscriptionPlan(supplierID: string): SubscriptionPlan | '' {
  return subscriptionPlansForSupplier(supplierID)[0]?.id || ''
}

export function subscriptionPlanLabel(id: string | undefined): string {
  if (!id) return ''
  for (const plans of [codexPlans, claudePlans, grokPlans]) {
    const match = plans.find(p => p.id === id)
    if (match) return match.label
  }
  return id
}
