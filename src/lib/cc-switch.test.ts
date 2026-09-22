import { expect, it } from 'vitest'
import { ccSwitchApp, ccSwitchEndpoint, ccSwitchImport, ccSwitchImportForClient, publicBaseURL } from './cc-switch'

it('infers the public base from the current page URL', () => {
  expect(publicBaseURL('http://192.168.1.8:8080/#keys')).toBe('http://192.168.1.8:8080')
  expect(publicBaseURL('http://192.168.1.8:8080/index.html#keys')).toBe('http://192.168.1.8:8080')
  expect(publicBaseURL('https://nas.local/unisub/#keys')).toBe('https://nas.local/unisub')
  expect(publicBaseURL('https://nas.local/unisub/index.html?x=1#keys')).toBe('https://nas.local/unisub')
  expect(publicBaseURL('https://unisub.example/')).toBe('https://unisub.example')
})

it('uses the page root for claude and /v1 for codex and grok', () => {
  expect(ccSwitchEndpoint('https://unisub.example', 'claude')).toBe('https://unisub.example')
  expect(ccSwitchEndpoint('https://unisub.example', 'codex')).toBe('https://unisub.example/v1')
  expect(ccSwitchEndpoint('https://nas.local/unisub', 'grok')).toBe('https://nas.local/unisub/v1')
  expect(ccSwitchApp('claude')).toBe('claude')
  expect(ccSwitchApp('codex')).toBe('codex')
  expect(ccSwitchApp('grok')).toBe('grokbuild')
})

it('imports keys as enabled CC Switch providers with model', () => {
  const claude = ccSwitchImport({ pageURL: 'http://192.168.1.8:8080/#keys', clientType: 'claude', name: 'My Claude', apiKey: 'sk-claude', model: 'claude-sonnet-4-6' })
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
  expect(new URL(ccSwitchImport({ pageURL: 'https://nas.local/unisub/#keys', clientType: 'codex', name: 'Codex', apiKey: 'sk-codex', model: 'gpt-5' })!.href).searchParams.get('endpoint')).toBe('https://nas.local/unisub/v1')
  expect(new URL(ccSwitchImport({ pageURL: 'http://192.168.1.8:8080/#keys', clientType: 'grok', name: 'Grok', apiKey: 'sk-grok', model: 'grok-4' })!.href).searchParams.get('app')).toBe('grokbuild')
  expect(ccSwitchImport({ pageURL: 'http://192.168.1.8:8080/#keys', clientType: 'claude', name: 'API', apiKey: 'sk', model: '  ' })).toBeNull()
})

it('allows the CC Switch window to export without a model', () => {
  const imported = ccSwitchImportForClient({
    pageURL: 'http://192.168.1.8:8080/#keys',
    client: 'codex',
    name: 'Codex',
    apiKey: 'sk-codex',
    config: { supplier_name: 'Codex', remark: '', default_model: '' },
  })
  expect(imported).not.toBeNull()
  expect(new URL(imported!.href).searchParams.has('model')).toBe(false)
})
