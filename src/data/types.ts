import type { components } from './openapi.gen'

type Schemas = components['schemas']
type WithoutSchema<T> = Omit<T, '$schema'>

// Public DTOs are aliases or small ergonomic refinements of the types generated
// from the Go/Huma contract. Keep UI-only unions here; do not duplicate server
// response shapes by hand.
export type User = WithoutSchema<Schemas['UserResponse']>
export type ClientType = 'claude' | 'codex' | 'grok'
export type CCSwitchClient = 'claude_code' | 'claude_desktop' | 'codex' | 'grok_build'
export type CCSwitchModelConfig = Schemas['CCSwitchModelConfig']
export type CCSwitchClientConfig = WithoutSchema<Schemas['CCSwitchClientConfig']>
export type GroupMember = Schemas['GroupMember']
export type ModelMapping = Schemas['ModelMappingResponse']

type GeneratedSupplier = WithoutSchema<Schemas['SupplierResponse']>
export type Supplier = Omit<GeneratedSupplier, 'models' | 'model_mappings' | 'supported_clients'> & {
  models: string[]
  model_mappings: ModelMapping[]
  supported_clients: ClientType[]
}
export interface SupplierListResponse { suppliers: Supplier[]; builtin_suppliers: Supplier[] }

/** Highest configured subscription tier. The server accepts adapter-defined IDs. */
export type SubscriptionPlan =
  | 'codex_plus' | 'codex_pro_5x' | 'codex_pro_20x'
  | 'claude_pro' | 'claude_max_5x' | 'claude_max_20x'
  | 'super_grok' | 'super_grok_plus' | 'super_grok_heavy'

type GeneratedAccountConfig = WithoutSchema<Schemas['AccountConfig']>
export type AccountConfig = Partial<GeneratedAccountConfig> & {
  kind?: 'subscription' | 'api' | 'group'
  supplier?: string
  subscription_plan?: SubscriptionPlan | string
  client_type?: ClientType
  members?: GroupMember[]
  credential?: unknown
  // Legacy fields remain accepted by the editor so it can remove them while
  // normalizing an older response before saving.
  auth_type?: string
  credential_id?: string
  oauth?: { credential_id?: string }
  client_types?: ClientType[]
  proxy?: unknown
  [key: string]: unknown
}

export type QuotaItem = Schemas['QuotaItem']
export type SubscriptionQuotaItem = Schemas['SubscriptionQuotaItem']
export type QuotaCacheStatus = 'missing' | 'fresh' | 'stale'
export type Quota = Omit<WithoutSchema<Schemas['AccountQuota']>, 'cache_status' | 'items' | 'subscription'> & {
  cache_status: QuotaCacheStatus
  items?: QuotaItem[]
  subscription?: SubscriptionQuotaItem[]
}

type GeneratedAccount = WithoutSchema<Schemas['AccountResponse']>
export type Account = Omit<GeneratedAccount, 'config' | 'quota'> & {
  config: AccountConfig
  quota?: Quota
}

type GeneratedAccountOption = Schemas['AccountOption']
export type AccountOption = Omit<GeneratedAccountOption, 'client_types'> & { client_types: ClientType[] }

type GeneratedAPIKey = WithoutSchema<Schemas['APIKeyResponse']>
export type APIKey = Omit<GeneratedAPIKey, 'client_types'> & { client_types?: ClientType[] }

export type ProxyHealth = Schemas['ProxyApplicationState']
type GeneratedProxyState = Schemas['ProxyState']
export type ProxyState = Omit<GeneratedProxyState, 'applications'> & { applications?: Record<string, ProxyHealth> }
type GeneratedProxyGroupConfig = WithoutSchema<Schemas['ProxyGroupConfig']>
export type ProxyGroupConfig = Omit<GeneratedProxyGroupConfig, 'proxies'> & { proxies: string[] }
type GeneratedProxyGroup = WithoutSchema<Schemas['ProxyGroup']>
export type ProxyGroup = Omit<GeneratedProxyGroup, 'config' | 'state'> & {
  config: ProxyGroupConfig
  state: { proxies?: Record<string, ProxyState> }
}

type GeneratedCall = Schemas['PersistedCallTraceSummary']
type OptionalCallFields = 'apikey' | 'user_id' | 'request_method' | 'cache_creation_tokens' | 'cache_read_tokens' | 'username'
export type Call = Omit<GeneratedCall, OptionalCallFields> & Partial<Pick<GeneratedCall, OptionalCallFields>>
export type CallHeaders = Record<string, string[] | string | null>
type GeneratedCallDetail = WithoutSchema<Schemas['PersistedCallTrace']>
type OptionalCallDetailFields = 'apikey' | 'user_id' | 'request_method'
export type CallDetail = Omit<GeneratedCallDetail, 'original_request_headers' | 'outbound_request_headers' | 'request_body' | 'response_headers' | 'response_body' | OptionalCallDetailFields> & Partial<Pick<GeneratedCallDetail, OptionalCallDetailFields>> & {
  original_request_headers?: CallHeaders | null
  outbound_request_headers?: CallHeaders | null
  request_body?: string | null
  response_headers?: CallHeaders | null
  response_body?: string | null
}

export interface List<T> { items: T[]; total: number; page?: number; page_size?: number }
export type UsageTotals = Schemas['UsageTotals']
type GeneratedUsage = WithoutSchema<Schemas['UsageResponse']>
export type Usage = Omit<GeneratedUsage, 'data'> & { data: Schemas['UsageItem'][] }

export type OAuthStart = WithoutSchema<Schemas['StartResult']>
export type OAuthStatus = WithoutSchema<Schemas['OAuthStatusResponse']>
export type OAuthResult = WithoutSchema<Schemas['OAuthResultResponse']>

export type LoginRequest = WithoutSchema<Schemas['LoginRequest']>
export type CreateUserRequest = WithoutSchema<Schemas['CreateUserRequest']>
export type UpdateUserRequest = WithoutSchema<Schemas['UpdateUserRequest']>
export type CreateAPIKeyRequest = WithoutSchema<Schemas['CreateAPIKeyRequest']>
