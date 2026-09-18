import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { KeyDetails } from './keys'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

it('shows Base URL for Claude and copy buttons beside detail fields', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  vi.stubGlobal('navigator', {
    ...navigator,
    clipboard: { writeText },
  })
  render(<KeyDetails value={{ id: 1, name: 'My Claude', account_id: 2, key: 'sk-test', valid_seconds: 0, created_at: '2026-01-01T00:00:00Z' }} aiProvider="claude" onClose={() => {}} />)
  const base = location.origin
  expect((screen.getByLabelText('Base URL') as HTMLInputElement).value).toBe(base)
  expect((screen.getByLabelText('API Key') as HTMLInputElement).value).toBe('sk-test')
  expect(screen.queryByRole('link', { name: /CC Switch/i })).toBeNull()
  expect(screen.queryByRole('button', { name: /复制密钥/ })).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: '复制API Key到剪贴板' }))
  await waitFor(() => expect(writeText).toHaveBeenCalledWith('sk-test'))
  fireEvent.click(screen.getByRole('button', { name: '复制Base URL到剪贴板' }))
  await waitFor(() => expect(writeText).toHaveBeenCalledWith(base))
})

it('uses Codex /v1 endpoint in the Base URL field', () => {
  render(<KeyDetails value={{ id: 2, name: 'Codex', account_id: 3, key: 'sk-codex', valid_seconds: 0, created_at: '2026-01-01T00:00:00Z' }} aiProvider="codex" onClose={() => {}} />)
  expect((screen.getByLabelText('Base URL') as HTMLInputElement).value).toBe(location.origin + '/v1')
})
