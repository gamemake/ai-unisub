export interface User { id: string; name: string; role: 'admin' | 'user'; enabled?: boolean; server_version?: string; created_at?: string }
export type ClientType = 'Any' | 'Anthropic' | 'OpenAI' | 'Grok'
export interface GroupMember { id: string; weight: number }
export interface Supplier { id: string; name: string; claude_url: string; codex_url: string }
export interface AICatalog { suppliers: Supplier[] }
export interface AICatalogResponse { catalog: AICatalog; builtin_suppliers: Supplier[] }
export interface AIProviderConfig { kind?: 'subscription' | 'api' | 'group'; supplier?: string; client_type?: ClientType; official_only?: boolean; members?: GroupMember[]; auth_type?: string; api_endpoint?: string; api_key?: string; credential_id?: string; credential?: unknown; oauth?: { credential_id?: string }; proxy_group_id?: string; enabled?: boolean; max_concurrent_connections?: number; queue_timeout_seconds?: number; [key: string]: unknown }
// Wire DTO: provider/provider_type retain their serialized names for existing clients and data.
export interface Account { id: string; name: string; provider: string; auth_type: string; enabled: boolean; config: AIProviderConfig; credential?: unknown }
export interface APIKey { id: string; name: string; account_id: string; key: string; valid_seconds: number; created_at: string; expires_at?: string }
export interface ProxyHealth { status: string; requests: number; failures: number; consecutive_failures: number; cooldown_until?: string; probe_requests: number; probe_failures: number }
export interface Proxy { id?: string; url: string; enabled: boolean; status?: string; last_available?: string; network?: ProxyHealth; applications?: Record<string, ProxyHealth>; error_records?: { start_at: string; count: number }[] }
export interface ProxyGroup { id: string; name: string; remark: string; max_retries: number; proxies: Proxy[] }
export interface Call { id: string; session_id: string; source_ip: string; request_id: string; account_id: string; provider_type: string; url: string; model: string; http_error_code: number; http_error_info?: string; input_tokens: number; output_tokens: number; started_at: string; finished_at: string }
export interface List<T> { items: T[]; total: number; page?: number; page_size?: number }
export interface UsageTotals { requests?: number; input_tokens?: number; output_tokens?: number; cache_creation_tokens?: number; cache_read_tokens?: number }
export interface Usage { data: { subscription_id?: string; subscription_name?: string; provider?: string; user_id?: string; username?: string; role?: string; usage: UsageTotals }[]; totals: UsageTotals }
export interface OAuthStart { session_id: string; auth_url?: string; authorization_url?: string; verification_uri?: string; user_code?: string; interval_seconds?: number; expires_at?: string; state?: string }
