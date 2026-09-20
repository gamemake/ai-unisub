import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { KeyDetails } from './keys'

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

it('API Key detail only shows request URL and API key', () => {
  render(<KeyDetails value={{ id: 1, name: 'My Claude', account_id: 2, key: 'sk-test', valid_seconds: 0, created_at: '2026-01-01T00:00:00Z', client_types: ['Anthropic'] }} onClose={() => {}} />)
  expect(screen.getByLabelText('请求地址')).toBeTruthy()
  expect(screen.getByLabelText('API Key')).toBeTruthy()
  expect(screen.queryByLabelText('客户端')).toBeNull()
  expect(screen.queryByLabelText('模型名')).toBeNull()
  expect(screen.queryByRole('button', { name: '打开 CC Switch' })).toBeNull()
})

it('API Key detail uses the public root URL for every protocol', () => {
  render(<KeyDetails value={{ id: 2, name: 'Codex', account_id: 3, key: 'sk-codex', valid_seconds: 0, created_at: '2026-01-01T00:00:00Z', client_types: ['OpenAI'] }} onClose={() => {}} />)
  expect((screen.getByLabelText('请求地址') as HTMLInputElement).value).toBe(location.origin)
})
