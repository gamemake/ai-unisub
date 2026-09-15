import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { PropsWithChildren } from 'react'
import { actions, useAIProviders } from './store'
import { request } from './client'

afterEach(() => { cleanup(); vi.unstubAllGlobals() })
describe('server data boundary', () => {
  it('shares requests between views and refreshes subscribers after invalidation', async () => {
    const fetch = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ items: [{ id: '1', name: 'first' }], total: 1 }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ items: [{ id: '1', name: 'updated' }], total: 1 }), { status: 200 }))
    vi.stubGlobal('fetch', fetch)
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
    const wrapper = ({ children }: PropsWithChildren) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
    const one = renderHook(useAIProviders, { wrapper }), two = renderHook(useAIProviders, { wrapper })
    await waitFor(() => expect(one.result.current.data?.items[0].name).toBe('first'))
    expect(two.result.current.data?.items[0].name).toBe('first')
    expect(fetch).toHaveBeenCalledTimes(1)
    await act(async () => { await client.invalidateQueries({ queryKey: ['ai-providers'] }) })
    await waitFor(() => expect(two.result.current.data?.items[0].name).toBe('updated'))
    expect(one.result.current.data?.items[0].name).toBe('updated')
    client.clear()
  })
  it('handles 204 responses and communicates server/network errors', async () => {
    const fetch = vi.fn().mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: 'invalid username or password' }), { status: 401 }))
      .mockRejectedValueOnce(new TypeError('offline'))
    vi.stubGlobal('fetch', fetch)
    await expect(actions.deleteKey('key/id')).resolves.toBeUndefined()
    expect(fetch.mock.calls[0][0]).toBe('/api/keys/key%2Fid')
    await expect(actions.login({ username: 'u', password: 'p' })).rejects.toThrow('用户名或密码错误')
    await expect(request('/api/me')).rejects.toThrow('网络请求失败')
  })
  it('uses compact UTC dates for call-detail endpoints', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }))
    vi.stubGlobal('fetch', fetch)
    await actions.callDetail({ id: 'abc', started_at: '2026-09-15T00:00:00Z' } as Parameters<typeof actions.callDetail>[0])
    expect(fetch.mock.calls[0][0]).toBe('/api/calls/20260915/abc')
  })
})
