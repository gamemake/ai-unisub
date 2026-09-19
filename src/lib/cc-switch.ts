import type { ClientType } from '@/data/types'

// Infer UniSub's public root from the dashboard page the user actually opened.
// Hash, query and index.html are not part of the gateway; a directory prefix is
// (reverse-proxy mounts such as https://host/unisub/#keys).
export function publicBaseURL(pageURL: string) {
  const url = new URL(pageURL)
  if (url.protocol !== 'http:' && url.protocol !== 'https:') return ''
  let path = url.pathname.replace(/\/index\.html$/i, '/')
  path = path.replace(/\/+$/, '')
  return url.origin + path
}

const clientApps: Record<Exclude<ClientType, 'Any'>, string> = {
  Anthropic: 'claude',
  OpenAI: 'codex',
  Grok: 'grokbuild',
}

export function ccSwitchApp(client: ClientType) {
  if (client === 'Any' || !clientApps[client]) return ''
  return clientApps[client]
}

// Claude Code appends /v1/messages itself. Codex and Grok Build take a /v1 base URL
// and append /responses. CC Switch only switches the live client when enabled=true.
export function ccSwitchEndpoint(base: string, client: ClientType) {
  const root = base.replace(/\/+$/, '')
  return client === 'Anthropic' ? root : root + '/v1'
}

export function ccSwitchImport(input: {
  pageURL: string
  clientType: ClientType
  name: string
  apiKey: string
  model: string
}) {
  const app = ccSwitchApp(input.clientType)
  if (!app) return null
  const model = input.model.trim()
  if (!model) return null
  const base = publicBaseURL(input.pageURL)
  if (!base) return null
  const endpoint = ccSwitchEndpoint(base, input.clientType)
  const params = new URLSearchParams({
    resource: 'provider',
    app,
    name: input.name,
    endpoint,
    homepage: base,
    apiKey: input.apiKey,
    model,
    enabled: 'true',
  })
  return { endpoint, href: 'ccswitch://v1/import?' + params }
}
