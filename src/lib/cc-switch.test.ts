import { expect, it } from 'vitest'
import { ccSwitchEndpoint, ccSwitchImport, publicBaseURL } from './cc-switch'

it('infers the public base from the current page URL', () => {
  expect(publicBaseURL('http://192.168.1.8:8080/#keys')).toBe('http://192.168.1.8:8080')
  expect(publicBaseURL('http://192.168.1.8:8080/index.html#keys')).toBe('http://192.168.1.8:8080')
  expect(publicBaseURL('https://nas.local/unisub/#keys')).toBe('https://nas.local/unisub')
  expect(publicBaseURL('https://nas.local/unisub/index.html?x=1#keys')).toBe('https://nas.local/unisub')
  expect(publicBaseURL('https://unisub.example/')).toBe('https://unisub.example')
})

it('uses the page root for Claude and /v1 for Codex and Grok Build', () => {
  expect(ccSwitchEndpoint('https://unisub.example', 'claude')).toBe('https://unisub.example')
  expect(ccSwitchEndpoint('https://unisub.example', 'codex')).toBe('https://unisub.example/v1')
  expect(ccSwitchEndpoint('https://nas.local/unisub', 'grok')).toBe('https://nas.local/unisub/v1')
})

it('imports Claude, Codex and Grok keys as enabled CC Switch providers', () => {
  const claude = ccSwitchImport({ pageURL: 'http://192.168.1.8:8080/#keys', aiProvider: 'claude', name: 'My Claude', apiKey: 'sk-claude' })
  const params = new URL(claude!.href).searchParams
  expect(claude!.href.startsWith('ccswitch://v1/import?')).toBe(true)
  expect(Object.fromEntries(params)).toEqual({
    resource: 'provider',
    app: 'claude',
    name: 'My Claude',
    endpoint: 'http://192.168.1.8:8080',
    homepage: 'http://192.168.1.8:8080',
    apiKey: 'sk-claude',
    enabled: 'true',
  })
  expect(new URL(ccSwitchImport({ pageURL: 'https://nas.local/unisub/#keys', aiProvider: 'codex', name: 'Codex', apiKey: 'sk-codex' })!.href).searchParams.get('endpoint')).toBe('https://nas.local/unisub/v1')
  expect(new URL(ccSwitchImport({ pageURL: 'http://192.168.1.8:8080/#keys', aiProvider: 'grok', name: 'Grok', apiKey: 'sk-grok' })!.href).searchParams.get('app')).toBe('grokbuild')
  expect(ccSwitchImport({ pageURL: 'http://192.168.1.8:8080/#keys', aiProvider: 'api', name: 'API', apiKey: 'sk' })).toBeNull()
})
