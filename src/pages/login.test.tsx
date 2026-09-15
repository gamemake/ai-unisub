import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Login } from './login'

afterEach(() => { cleanup(); vi.unstubAllGlobals() })
it('submits credentials through the data layer and presents login errors', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: 'invalid username or password' }), { status: 401 })))
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><Login /></QueryClientProvider>)
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('用户名'), 'tester')
  await user.type(screen.getByLabelText('密码'), 'wrong-password')
  await user.click(screen.getByRole('button', { name: '登录控制台' }))
  expect((await screen.findByRole('alert')).textContent).toContain('用户名或密码错误')
  expect(fetch).toHaveBeenCalledWith('/api/login', expect.objectContaining({ method: 'POST', credentials: 'same-origin', body: JSON.stringify({ username: 'tester', password: 'wrong-password' }) }))
  client.clear()
})
