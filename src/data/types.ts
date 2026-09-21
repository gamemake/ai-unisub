import type { Quota } from './quota'
export type * from './quota'

export interface User { id: number; name: string; role: 'admin' | 'user'; enabled?: boolean; labels?: string[]; server_version?: string; created_at?: string; updated_at?: string }
export type ClientType = 'Any' | 'Anthropic' | 'OpenAI' | 'Grok'
export type CCSwitchClient = 'claude_code' | 'claude_desktop' | 'codex' | 'grok_build'
export interface CCSwitchModelConfig { model: string; supports_1m: boolean }
export interface CCSwitchClientConfig { supplier_name: string; remark: string; models?: Record<string, CCSwitchModelConfig>; default_model?: string }
export interface GroupMember { id: number; weight: number }
// Only fixed, non-authentication header overrides; request defaults live in Go.
// claude_url / openai_url / supported_clients are code-owned; models/name are configurable.
export interface ModelMapping {
  from: string
  to: string
}
export interface Supplier {
  id: string
  name: string
  claude_url: string
  openai_url: string
  models: string[]
  /** Client model → upstream model; `from` allows one `*` wildcard. */
  model_mappings?: ModelMapping[]
  supported_clients: ClientType[]
  /** Flat plan_id → usage weight for same-tier load balancing. */
  subscription_plan_weights?: Record<string, number>
  subscription_usage_header_overrides?: Record<string, string>
  api_usage_header_overrides?: Record<string, string>
}
export interface AICatalog { suppliers: Supplier[] }
export interface AICatalogResponse { catalog: AICatalog; builtin_suppliers: Supplier[] }
/** Highest subscription tier for kind=subscription; config-only, adapter-prefixed IDs. */
export type SubscriptionPlan =
  | 'codex_plus' | 'codex_pro_5x' | 'codex_pro_20x'
  | 'claude_pro' | 'claude_max_5x' | 'claude_max_20x'
  | 'super_grok' | 'super_grok_plus' | 'super_grok_heavy'

export interface AIProviderConfig {
  kind?: 'subscription' | 'api' | 'group'
  supplier?: string
  subscription_plan?: SubscriptionPlan | string
  client_type?: ClientType
  official_only?: boolean
  members?: GroupMember[]
  auth_type?: string
  api_endpoint?: string
  api_key?: string
  credential_id?: string
  credential?: unknown
  oauth?: { credential_id?: string }
  proxy_group_id?: number
  enabled?: boolean
  max_concurrent_connections?: number
  queue_timeout_seconds?: number
  [key: string]: unknown
}
// Wire DTO: provider/provider_type retain their serialized names for existing clients and data.
export interface Account { id: number; name: string; provider: string; auth_type: string; enabled: boolean; config: AIProviderConfig; credential?: unknown; quota?: Quota; created_at?: string; updated_at?: string }
export interface ProviderOption { id: number; name: string; provider: string; enabled: boolean; client_types: ClientType[] }
export interface APIKey { id: number; name: string; account_id: number; key: string; valid_seconds: number; created_at: string; updated_at?: string; expires_at?: string; client_types?: ClientType[] }
export interface ProxyHealth { status: string; requests: number; failures: number; consecutive_failures: number; cooldown_until?: string; probe_requests: number; probe_failures: number }
export interface Proxy { id?: string; url: string; enabled: boolean; status?: string; last_available?: string; network?: ProxyHealth; applications?: Record<string, ProxyHealth>; error_records?: { start_at: string; count: number }[] }
export interface ProxyGroup { id: number; name: string; remark: string; max_retries: number; proxies: Proxy[]; created_at?: string; updated_at?: string }
export interface Call { id: number; session_id: string; source_ip: string; username?: string; request_id: string; account_id: number; provider_type: string; url: string; outbound_url?: string; model: string; http_error_code: number; http_error_info?: string; input_tokens: number; output_tokens: number; cache_creation_tokens?: number; cache_read_tokens?: number; started_at: string; finished_at: string }
/** Full call trace from GET /api/calls/:day/:id. Header maps follow Go http.Header JSON (name → string[]). Bodies are base64. */
export type CallHeaders = Record<string, string[] | string>
export interface CallDetail extends Call {
  original_request_headers?: CallHeaders | null
  outbound_request_headers?: CallHeaders | null
  request_body?: string | null
  response_headers?: CallHeaders | null
  response_body?: string | null
}
export interface List<T> { items: T[]; total: number; page?: number; page_size?: number }
export interface UsageTotals { requests?: number; input_tokens?: number; output_tokens?: number; cache_creation_tokens?: number; cache_read_tokens?: number; total_tokens?: number }
export interface Usage { data: { subscription_id?: number; subscription_name?: string; provider?: string; user_id?: number; username?: string; role?: string; usage: UsageTotals }[]; totals: UsageTotals; has_records?: boolean }
export interface OAuthStart { session_id: string; auth_url?: string; authorization_url?: string; verification_uri?: string; user_code?: string; interval_seconds?: number; expires_at?: string; state?: string }
