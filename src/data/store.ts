// Server-state boundary: components observe queries and invoke actions; no view fetches directly.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { clearSession, json, queryClient, request } from './client'
import type { Account, AccountOption, APIKey, CCSwitchClientConfig, Call, CallDetail, CreateAPIKeyRequest, CreateUserRequest, List, LoginRequest, OAuthResult, OAuthStart, OAuthStatus, ProxyGroup, ProxyGroupConfig, Supplier, SupplierListResponse, UpdateUserRequest, Usage, Quota, User } from './types'
const id = (value: string | number) => encodeURIComponent(String(value))
export const useMe = () => useQuery({ queryKey: ['me'], queryFn: ({ signal }) => request<User | null>('/api/me', { signal }) })
export const useAccounts = () => useQuery({ queryKey: ['accounts'], queryFn: ({ signal }) => request<List<Account>>('/api/accounts', { signal }) })
export function useFetchQuota() {
  const client = useQueryClient()
  return useMutation({ mutationFn: actions.fetchQuota, onSuccess: (data, accountID) => {
    client.setQueryData<List<Account>>(['accounts'], current => current && ({ ...current, items: current.items.map(account => account.id === accountID ? { ...account, quota: data } : account) }))
  } })
}
export const useSuppliers = () => useQuery({ queryKey: ['suppliers'], queryFn: ({ signal }) => request<SupplierListResponse>('/api/suppliers', { signal }) })
export const useAccountOptions = () => useQuery({ queryKey: ['account-options'], queryFn: ({ signal }) => request<List<AccountOption>>('/api/keys/accounts', { signal }) })
export const useKeys = () => useQuery({ queryKey: ['keys'], queryFn: ({ signal }) => request<List<APIKey>>('/api/keys', { signal }) })
export const useKeyConfig = (keyID?: number, client?: string) => useQuery({ queryKey: ['key-config', keyID, client], enabled: !!keyID && !!client, queryFn: ({ signal }) => request<CCSwitchClientConfig>(`/api/keys/${id(keyID!)}/config/${id(client!)}`, { signal }) })
export const useAccountModels = (accountID?: number) => useQuery({ queryKey: ['account-models', accountID], enabled: !!accountID, queryFn: ({ signal }) => request<{ models: { id: string }[] }>(`/api/accounts/${id(accountID!)}/models`, { signal }) })
export const useUsers = () => useQuery({ queryKey: ['users'], queryFn: ({ signal }) => request<List<User>>('/api/users', { signal }) })
export const useProxies = (enabled = true) => useQuery({ queryKey: ['proxies'], enabled, queryFn: ({ signal }) => request<ProxyGroup[]>('/api/proxy-groups', { signal }) })
export const useCalls = (params: Record<string, string> = {}) => useQuery({ queryKey: ['calls', params], staleTime: 0, refetchInterval: 15_000, queryFn: ({ signal }) => request<List<Call>>('/api/calls?' + new URLSearchParams(params), { signal }) })
export const useUsage = (kind: 'subscriptions' | 'users', params: Record<string, string>) => useQuery({ queryKey: ['usage', kind, params], queryFn: ({ signal }) => request<Usage>(`/api/usage/${kind}?` + new URLSearchParams(params), { signal }) })
export function useAction<T, R = unknown>(action: (input: T) => Promise<R>, invalidate: string[] = []) {
  return useMutation({ mutationFn: action, onSuccess: async () => { await Promise.all(invalidate.map(key => queryClient.invalidateQueries({ queryKey: [key] }))) } })
}
export const actions = {
  fetchQuota: (accountID: number) => request<Quota>(`/api/accounts/${id(accountID)}/refresh-quota`, json('POST')),
  fetchModels: (accountID: number) => request<{ models: { id: string }[] }>(`/api/accounts/${id(accountID)}/fetch-models`, json('POST')),
  listAccountModels: (accountID: number) => request<{ models: { id: string }[] }>(`/api/accounts/${id(accountID)}/models`),
  login: async (input: LoginRequest) => { await request('/api/login', json('POST', input)); await clearSession(); await queryClient.invalidateQueries({ queryKey: ['me'] }) },
  logout: async () => { await request('/api/logout', json('POST')); await clearSession() },
  saveAccount: (input: { id?: number; name: string; config: Account['config'] }) => request<Account>('/api/accounts' + (input.id ? '/' + id(input.id) : ''), json(input.id ? 'PUT' : 'POST', input)),
  deleteAccount: (key: number) => request('/api/accounts/' + id(key), json('DELETE')),
  saveSupplier: (input: { id: string; name: string; models: string[]; model_mappings: { from: string; to: string }[]; subscription_plan_weights?: Record<string, number> }) => request<Supplier>('/api/suppliers/' + id(input.id), json('PUT', input)),
  createKey: (input: CreateAPIKeyRequest) => request<APIKey>('/api/keys', json('POST', input)),
  deleteKey: (key: number) => request('/api/keys/' + id(key), json('DELETE')),
  saveKeyConfig: (input: { id: number; client: string; config: CCSwitchClientConfig }) => request<CCSwitchClientConfig>(`/api/keys/${id(input.id)}/config/${id(input.client)}`, json('PUT', input.config)),
  createUser: (input: CreateUserRequest) => request<User>('/api/users', json('POST', input)),
  updateUser: (input: UpdateUserRequest & { id: number }) => request<User>('/api/users/' + id(input.id), json('PUT', input)),
  resetPassword: (input: { id: number; password: string }) => request('/api/users/' + id(input.id) + '/password', json('POST', input)),
  deleteUser: (key: number) => request('/api/users/' + id(key), json('DELETE')),
  password: (input: { old_password: string; new_password: string }) => request('/api/password', json('POST', input)),
  saveProxy: ({ id: groupID, ...config }: ProxyGroupConfig & { id?: number }) => request<ProxyGroup>('/api/proxy-groups' + (groupID ? '/' + id(groupID) : ''), json(groupID ? 'PUT' : 'POST', config)),
  deleteProxy: (key: number) => request('/api/proxy-groups/' + id(key), json('DELETE')),
  callDetail: (call: Call) => request<CallDetail>('/api/calls/' + id(call.finished_at.slice(0, 10).replaceAll('-', '')) + '/' + id(call.id)),
  oauthStart: (input: { aiProvider: string; proxy_group_id?: number }) => request<OAuthStart>(`/api/oauth/${id(input.aiProvider)}/start`, json('POST', input.proxy_group_id ? { proxy_group_id: input.proxy_group_id } : {})),
  oauthPoll: (input: { aiProvider: string; session_id: string }) => request<OAuthStatus>(`/api/oauth/${id(input.aiProvider)}/poll/${id(input.session_id)}`, json('POST', {})),
  oauthComplete: (input: { aiProvider: string; session_id: string; code: string; state: string }) => request<{ result_id: string }>(`/api/oauth/${id(input.aiProvider)}/complete/${id(input.session_id)}`, json('POST', input)),
  oauthResult: (key: string) => request<OAuthResult>('/api/oauth/results/' + id(key)),
  oauthStatus: (input: { aiProvider: string; session_id: string }) => request<OAuthStatus>(`/api/oauth/${id(input.aiProvider)}/status/${id(input.session_id)}`),
}
