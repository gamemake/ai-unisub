import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CallDetails, compareRequestHeaders, decodeBody, formatHeadersCopy, formatRequestHeadersCopy, headerEntries } from './call-details'
import type { Call, CallDetail } from '@/data/types'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

it('decodes base64 JSON bodies and pretty-prints them', () => {
  const raw = btoa(JSON.stringify({ model: 'claude', n: 1 }))
  expect(decodeBody(raw)).toBe('{\n  "model": "claude",\n  "n": 1\n}')
})

it('decodes real call-trace request bodies from the database sample', () => {
  // Compact JSON from data/ai-unisub.db call id=3 shape (Go stores raw UTF-8 JSON bytes).
  const sample = '{"include":["reasoning.encrypted_content"],"model":"grok-4.5","stream":true}'
  const encoded = btoa(sample)
  expect(decodeBody(encoded)).toContain('"model": "grok-4.5"')
  expect(decodeBody(encoded)).toContain('"stream": true')
  // Large base64 must still decode (regex-based detection previously struggled here).
  const large = btoa(`{"input":"${'x'.repeat(20_000)}"}`)
  expect(decodeBody(large).startsWith('{\n  "input":')).toBe(true)
})

it('keeps plain text and SSE bodies when not JSON', () => {
  expect(decodeBody('hello world')).toBe('hello world')
  expect(decodeBody('')).toBe('')
  expect(decodeBody(null)).toBe('')
  const sse = 'event: response.created\ndata: {"ok":true}\n\n'
  expect(decodeBody(btoa(sse))).toBe(sse)
})

it('flattens header maps into sorted name/value rows', () => {
  expect(headerEntries({
    'X-Test': ['a', 'b'],
    Accept: 'text/plain',
  })).toEqual([
    ['Accept', 'text/plain'],
    ['X-Test', 'a, b'],
  ])
  expect(headerEntries(null)).toEqual([])
})

it('classifies request header changes for comparison', () => {
  expect(compareRequestHeaders(
    { Authorization: ['Bearer old'], 'Content-Type': ['application/json'], Host: ['a.example'] },
    { Authorization: ['Bearer new'], 'X-Request-Id': ['abc'], Host: ['a.example'] },
  )).toEqual([
    { name: 'Authorization', original: 'Bearer old', outbound: 'Bearer new', change: 'modified' },
    { name: 'Content-Type', original: 'application/json', outbound: '', change: 'removed' },
    { name: 'Host', original: 'a.example', outbound: 'a.example', change: 'same' },
    { name: 'X-Request-Id', original: '', outbound: 'abc', change: 'added' },
  ])
})

it('renders collapsible sections and header change markers', async () => {
  const detail: CallDetail = {
    id: 7,
    session_id: 'sess-1',
    source_ip: '10.0.0.1',
    request_id: 'req-1',
    account_id: 3,
    provider_type: 'claude',
    url: '/v1/messages',
    outbound_url: 'https://api.anthropic.com/v1/messages',
    model: 'claude-sonnet',
    http_error_code: 200,
    input_tokens: 12,
    output_tokens: 34,
    cache_creation_tokens: 1,
    cache_read_tokens: 2,
    queue_duration_ms: 25,
    request_duration_ms: 975,
    finished_at: '2026-09-15T01:00:01.250Z',
    original_request_headers: { Authorization: ['Bearer client'], Accept: ['application/json'] },
    outbound_request_headers: { Authorization: ['Bearer upstream'], Accept: ['application/json'], 'X-Api-Key': ['k'] },
    request_body: btoa('{"stream":true}'),
    response_headers: { 'Content-Type': ['application/json'] },
    response_body: btoa('event: done\ndata: {"ok":true}\n\n'),
  }
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(detail), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })))

  const call: Call = {
    id: 7,
    session_id: 'sess-1',
    source_ip: '10.0.0.1',
    request_id: 'req-1',
    account_id: 3,
    provider_type: 'claude',
    url: '/v1/messages',
    outbound_url: 'https://api.anthropic.com/v1/messages',
    model: 'claude-sonnet',
    http_error_code: 200,
    input_tokens: 12,
    output_tokens: 34,
    queue_duration_ms: 25,
    request_duration_ms: 975,
    finished_at: '2026-09-15T01:00:01.250Z',
  }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <CallDetails call={call} onClose={() => {}} />
    </QueryClientProvider>,
  )

  const toggleHeaders = () => screen.getByRole('button', { name: /Request Headers.*处变更/ })
  const toggleRequestBody = () => screen.getByRole('button', { name: /Request Body.*字符/ })
  const toggleResponseHeaders = () => screen.getByRole('button', { name: /Response Headers.*项/ })
  const toggleResponseBody = () => screen.getByRole('button', { name: /Response Body.*字符/ })
  await waitFor(() => expect(toggleHeaders()).toBeTruthy())
  expect(screen.getByText('/v1/messages')).toBeTruthy()
  expect(screen.getByText('https://api.anthropic.com/v1/messages')).toBeTruthy()

  // Headers and bodies start collapsed.
  expect(screen.queryByText('Bearer client')).toBeNull()
  expect(screen.queryByText(/"stream": true/)).toBeNull()
  expect(screen.queryByText(/event: done/)).toBeNull()

  await user.click(toggleHeaders())
  expect(screen.getByText('已修改')).toBeTruthy()
  expect(screen.getByText('新增')).toBeTruthy()
  expect(screen.getByText('Bearer client')).toBeTruthy()
  expect(screen.getByText('Bearer upstream')).toBeTruthy()
  expect(screen.queryByText('原始请求')).toBeNull()
  expect(screen.queryByText('出站请求')).toBeNull()

  await user.click(toggleRequestBody())
  expect(screen.getByText(/"stream": true/)).toBeTruthy()
  await user.click(toggleResponseHeaders())
  expect(screen.getByText('Content-Type')).toBeTruthy()
  await user.click(toggleResponseBody())
  expect(screen.getByText(/event: done/)).toBeTruthy()

  // Collapse request headers — content should hide.
  await user.click(toggleHeaders())
  expect(screen.queryByText('Bearer client')).toBeNull()
  await user.click(toggleHeaders())
  expect(screen.getByText('Bearer client')).toBeTruthy()

  const writeText = vi.fn(async () => {})
  vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })
  await user.click(screen.getByRole('button', { name: '复制Request Headers到剪贴板' }))
  expect(writeText).toHaveBeenCalledWith(expect.stringContaining('Authorization:\n  original: Bearer client\n  outbound: Bearer upstream'))
  await user.click(screen.getByRole('button', { name: '复制Request Body到剪贴板' }))
  expect(writeText).toHaveBeenCalledWith(expect.stringContaining('"stream": true'))
  await user.click(screen.getByRole('button', { name: '复制Response Headers到剪贴板' }))
  expect(writeText).toHaveBeenCalledWith('Content-Type: application/json')
  await user.click(screen.getByRole('button', { name: '复制Response Body到剪贴板' }))
  expect(writeText).toHaveBeenCalledWith(expect.stringContaining('event: done'))
})

it('formats headers for clipboard', () => {
  expect(formatHeadersCopy([['Accept', 'text/plain'], ['Host', 'a']])).toBe('Accept: text/plain\nHost: a')
  expect(formatRequestHeadersCopy([
    { name: 'Host', original: 'a', outbound: 'a', change: 'same' },
    { name: 'Authorization', original: 'old', outbound: 'new', change: 'modified' },
  ])).toBe('Host: a\nAuthorization:\n  original: old\n  outbound: new')
})

