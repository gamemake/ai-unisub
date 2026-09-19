import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { KeyDetails } from './keys'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

it('merges key detail and CC Switch fields in one dialog', async () => {
  render(<KeyDetails value={{ id: 1, name: 'My Claude', account_id: 2, key: 'sk-test', valid_seconds: 0, created_at: '2026-01-01T00:00:00Z', client_types: ['Anthropic'] }} onClose={() => {}} />)
  const base = location.origin
  expect((screen.getByLabelText('Base URL') as HTMLInputElement).value).toBe(base)
  expect((screen.getByLabelText('API Key') as HTMLInputElement).value).toBe('sk-test')
  expect(screen.getByLabelText('客户端')).toBeTruthy()
  expect(screen.getByLabelText('模型名')).toBeTruthy()
  expect(screen.queryByRole('button', { name: '导入 CC Switch' })).toBeNull()
  expect((screen.getByRole('button', { name: '打开 CC Switch' }) as HTMLButtonElement).disabled).toBe(true)

  fireEvent.change(screen.getByLabelText('模型名'), { target: { value: 'claude-sonnet-4-6' } })
  await waitFor(() => {
    const link = screen.getByRole('link', { name: '打开 CC Switch' }) as HTMLAnchorElement
    expect(link.href.startsWith('ccswitch://v1/import?')).toBe(true)
    expect(new URL(link.href).searchParams.get('app')).toBe('claude')
    expect(new URL(link.href).searchParams.get('model')).toBe('claude-sonnet-4-6')
    expect(new URL(link.href).searchParams.get('apiKey')).toBe('sk-test')
  })
})

it('uses OpenAI /v1 endpoint when OpenAI client is selected', () => {
  render(<KeyDetails value={{ id: 2, name: 'Codex', account_id: 3, key: 'sk-codex', valid_seconds: 0, created_at: '2026-01-01T00:00:00Z', client_types: ['OpenAI'] }} onClose={() => {}} />)
  expect((screen.getByLabelText('Base URL') as HTMLInputElement).value).toBe(location.origin + '/v1')
})

it('hides CC Switch controls when no client types are available', () => {
  render(<KeyDetails value={{ id: 3, name: 'Plain', account_id: 4, key: 'sk-plain', valid_seconds: 0, created_at: '2026-01-01T00:00:00Z', client_types: [] }} onClose={() => {}} />)
  expect(screen.getByLabelText('API Key')).toBeTruthy()
  expect(screen.getByLabelText('Base URL')).toBeTruthy()
  expect(screen.queryByLabelText('客户端')).toBeNull()
  expect(screen.queryByLabelText('模型名')).toBeNull()
  expect(screen.queryByRole('button', { name: '打开 CC Switch' })).toBeNull()
  expect(screen.getAllByRole('button', { name: '关闭' }).length).toBeGreaterThan(0)
})
