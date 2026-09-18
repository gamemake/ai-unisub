import { afterEach, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { KeyDetails } from './keys'

afterEach(() => cleanup())

it('imports a Claude key into CC Switch using the current page URL', () => {
  render(<KeyDetails value={{ id: 1, name: 'My Claude', account_id: 2, key: 'sk-test', valid_seconds: 0, created_at: '2026-01-01T00:00:00Z' }} aiProvider="claude" onClose={() => {}} />)
  const href = screen.getByRole('link', { name: '导入 CC Switch' }).getAttribute('href') || ''
  const params = new URL(href).searchParams
  const base = location.origin
  expect(href.startsWith('ccswitch://v1/import?')).toBe(true)
  expect(params.get('app')).toBe('claude')
  expect(params.get('endpoint')).toBe(base)
  expect(params.get('homepage')).toBe(base)
  expect(params.get('enabled')).toBe('true')
  expect(params.get('apiKey')).toBe('sk-test')
  expect((screen.getByLabelText('Base URL') as HTMLInputElement).value).toBe(base)
})
