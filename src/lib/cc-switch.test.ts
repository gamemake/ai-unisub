import { expect, it } from 'vitest'
import { ccSwitchApp, ccSwitchEndpoint, ccSwitchImport, publicBaseURL } from './cc-switch'

it('infers the public base from the current page URL', () => {
  expect(publicBaseURL('http://192.168.1.8:8080/#keys')).toBe('http://192.168.1.8:8080')
  expect(publicBaseURL('http://192.168.1.8:8080/index.html#keys')).toBe('http://192.168.1.8:8080')
  expect(publicBaseURL('https://nas.local/unisub/#keys')).toBe('https://nas.local/unisub')
  expect(publicBaseURL('https://nas.local/unisub/index.html?x=1#keys')).toBe('https://nas.local/unisub')
  expect(publicBaseURL('https://unisub.example/')).toBe('https://unisub.example')
})

it('uses the page root for Anthropic and /v1 for OpenAI and Grok', () => {
  expect(ccSwitchEndpoint('https://unisub.example', 'Anthropic')).toBe('https://unisub.example')
  expect(ccSwitchEndpoint('https://unisub.example', 'OpenAI')).toBe('https://unisub.example/v1')
  expect(ccSwitchEndpoint('https://nas.local/unisub', 'Grok')).toBe('https://nas.local/unisub/v1')
  expect(ccSwitchApp('Anthropic')).toBe('claude')
  expect(ccSwitchApp('OpenAI')).toBe('codex')
  expect(ccSwitchApp('Grok')).toBe('grokbuild')
})

it('imports keys as enabled CC Switch providers with model', () => {
  const claude = ccSwitchImport({ pageURL: 'http://192.168.1.8:8080/#keys', clientType: 'Anthropic', name: 'My Claude', apiKey: 'sk-claude', model: 'claude-sonnet-4-6' })
  const params = new URL(claude!.href).searchParams
  expect(claude!.href.startsWith('ccswitch://v1/import?')).toBe(true)
  expect(Object.fromEntries(params)).toEqual({
    resource: 'provider',
    app: 'claude',
    name: 'My Claude',
    endpoint: 'http://192.168.1.8:8080',
    homepage: 'http://192.168.1.8:8080',
    apiKey: 'sk-claude',
    model: 'claude-sonnet-4-6',
    enabled: 'true',
  })
  expect(new URL(ccSwitchImport({ pageURL: 'https://nas.local/unisub/#keys', clientType: 'OpenAI', name: 'Codex', apiKey: 'sk-codex', model: 'gpt-5' })!.href).searchParams.get('endpoint')).toBe('https://nas.local/unisub/v1')
  expect(new URL(ccSwitchImport({ pageURL: 'http://192.168.1.8:8080/#keys', clientType: 'Grok', name: 'Grok', apiKey: 'sk-grok', model: 'grok-4' })!.href).searchParams.get('app')).toBe('grokbuild')
  expect(ccSwitchImport({ pageURL: 'http://192.168.1.8:8080/#keys', clientType: 'Any', name: 'API', apiKey: 'sk', model: 'x' })).toBeNull()
  expect(ccSwitchImport({ pageURL: 'http://192.168.1.8:8080/#keys', clientType: 'Anthropic', name: 'API', apiKey: 'sk', model: '  ' })).toBeNull()
})
