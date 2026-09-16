// Quota describes account allowances: subscription usage, limits and reset times, or API balance.
// It is not a normalized remaining allowance.
// Historical usage and costs are excluded; groups have no quota.
// Upstream display DTOs, separate from local call-history Usage and token counts.
// API balances preserve original field names and raw JSON text; not for arithmetic.
// source is a local discriminator for multi-response queries, not an upstream field.
export interface QuotaItem { name: string; value: string; source?: string }
// Subscription usage is percent; reset_at is UTC (Go zero time means unknown).
export interface SubscriptionQuotaItem { time_dimension: string; usage: number; reset_at: string }
// A flat snapshot for one provider. Group accounts omit quota entirely.
export interface Quota { subscription?: SubscriptionQuotaItem[]; items?: QuotaItem[]; cache_status: QuotaCacheStatus; updated_at?: string }
export type QuotaCacheStatus = 'missing' | 'fresh' | 'stale'
