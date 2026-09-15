// Server-state boundary: components observe queries and invoke actions; no view fetches directly.
import { useMutation, useQuery } from '@tanstack/react-query'
import { clearSession, json, queryClient, request } from './client'
import type { Account, AICatalogResponse, APIKey, Call, List, OAuthStart, Proxy, ProxyGroup, Usage, User } from './types'
const id = encodeURIComponent
export const useMe = () => useQuery({ queryKey: ['me'], queryFn: ({ signal }) => request<User | null>('/api/me', { signal }) })
export const useAIProviders = () => useQuery({ queryKey: ['ai-providers'], queryFn: ({ signal }) => request<List<Account>>('/api/ai-providers', { signal }) })
export const useAICatalog = () => useQuery({ queryKey: ['ai-catalog'], queryFn: ({ signal }) => request<AICatalogResponse>('/api/ai-catalog', { signal }) })
export const useProviderOptions = () => useQuery({ queryKey: ['provider-options'], queryFn: ({ signal }) => request<List<Pick<Account, 'id' | 'name' | 'provider' | 'enabled'>>>('/api/keys/providers', { signal }) })
export const useKeys = () => useQuery({ queryKey: ['keys'], queryFn: ({ signal }) => request<List<APIKey>>('/api/keys', { signal }) })
export const useUsers = () => useQuery({ queryKey: ['users'], queryFn: ({ signal }) => request<List<User>>('/api/users', { signal }) })
export const useProxies = (enabled = true) => useQuery({ queryKey: ['proxies'], enabled, queryFn: ({ signal }) => request<ProxyGroup[] | null>('/api/proxy-groups', { signal }) })
export const useCalls = (params: Record<string, string> = {}) => useQuery({ queryKey: ['calls', params], staleTime: 0, refetchInterval: 15_000, queryFn: ({ signal }) => request<List<Call>>('/api/calls?' + new URLSearchParams(params), { signal }) })
export const useUsage = (kind: 'subscriptions' | 'users', params: Record<string, string>) => useQuery({ queryKey: ['usage', kind, params], queryFn: ({ signal }) => request<Usage>(`/api/usage/${kind}?` + new URLSearchParams(params), { signal }) })
export function useAction<T, R = unknown>(action: (input: T) => Promise<R>, invalidate: string[] = []) {
  return useMutation({ mutationFn: action, onSuccess: async () => { await Promise.all(invalidate.map(key => queryClient.invalidateQueries({ queryKey: [key] }))) } })
}
export const actions = {
  login: async (input: { username: string; password: string }) => { await request('/api/login', json('POST', input)); await clearSession(); await queryClient.invalidateQueries({ queryKey: ['me'] }) },
  logout: async () => { await request('/api/logout', json('POST')); await clearSession() },
  saveAIProvider: (input: { id?: string; name: string; provider: string; config: Account['config'] }) => request<Account>('/api/ai-providers' + (input.id ? '/' + id(input.id) : ''), json(input.id ? 'PUT' : 'POST', input)),
  deleteAIProvider: (key: string) => request('/api/ai-providers/' + id(key), json('DELETE')),
  createKey: (input: { name: string; account_id: string; valid_seconds: number }) => request<APIKey>('/api/keys', json('POST', input)),
  deleteKey: (key: string) => request('/api/keys/' + id(key), json('DELETE')),
  createUser: (input: { name: string; password: string; role: string }) => request<User>('/api/users', json('POST', input)),
  updateUser: (input: { id: string; role?: string; enabled?: boolean }) => request<User>('/api/users/' + id(input.id), json('PUT', input)),
  resetPassword: (input: { id: string; password: string }) => request('/api/users/' + id(input.id) + '/password', json('POST', input)),
  deleteUser: (key: string) => request('/api/users/' + id(key), json('DELETE')),
  password: (input: { old_password: string; new_password: string }) => request('/api/password', json('POST', input)),
  saveProxy: (input: Partial<ProxyGroup>) => request<ProxyGroup>('/api/proxy-groups' + (input.id ? '/' + id(input.id) : ''), json(input.id ? 'PUT' : 'POST', input)),
  deleteProxy: (key: string) => request('/api/proxy-groups/' + id(key), json('DELETE')),
  testProxy: (input: { group_id?: string; proxy_id?: string; url?: string }) => request<Proxy>('/api/proxy-groups/test', json('POST', input)),
  proxyErrors: (input: { group_id: string; proxy_id: string }) => request<Proxy>('/api/proxy-groups/errors?' + new URLSearchParams(input)),
  callDetail: (call: Call) => request<Record<string, unknown>>('/api/calls/' + id(call.started_at.slice(0, 10).replaceAll('-', '')) + '/' + id(call.id)),
  oauthStart: (input: { aiProvider: string; proxy_group_id?: string }) => request<OAuthStart>(`/api/oauth/${id(input.aiProvider)}/start`, json('POST', { proxy_group_id: input.proxy_group_id })),
  oauthPoll: (input: { aiProvider: string; session_id: string }) => request<{ status: string; result_id?: string; interval_seconds?: number }>(`/api/oauth/${id(input.aiProvider)}/poll/${id(input.session_id)}`, json('POST', {})),
  oauthComplete: (input: { aiProvider: string; session_id: string; code: string; state: string }) => request<{ result_id: string }>(`/api/oauth/${id(input.aiProvider)}/complete/${id(input.session_id)}`, json('POST', input)),
  oauthResult: (key: string) => request<{ result: unknown }>('/api/oauth/results/' + id(key)),
  oauthStatus: (input: { aiProvider: string; session_id: string }) => request<{ status: string; result_id?: string; interval_seconds?: number }>(`/api/oauth/${id(input.aiProvider)}/status/${id(input.session_id)}`),
}
