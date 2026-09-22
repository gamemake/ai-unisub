import type { CCSwitchClient, CCSwitchClientConfig, ClientType } from '@/data/types'

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

const clientApps: Record<ClientType, string> = {
  claude: 'claude',
  codex: 'codex',
  grok: 'grokbuild',
}

export function ccSwitchApp(client: ClientType) {
  if (!clientApps[client]) return ''
  return clientApps[client]
}

// Claude Code appends /v1/messages itself. Codex and Grok Build take a /v1 base URL
// and append /responses. CC Switch only switches the live client when enabled=true.
export function ccSwitchEndpoint(base: string, client: ClientType) {
  const root = base.replace(/\/+$/, '')
  return client === 'claude' ? root : root + '/v1'
}

export const ccSwitchClientLabel: Record<CCSwitchClient, string> = { claude_code: 'Claude Code', claude_desktop: 'Claude Desktop', codex: 'Codex', grok_build: 'Grok Build' }
export function clientsForTypes(types: ClientType[]): CCSwitchClient[] {
  const out: CCSwitchClient[] = []
  for (const type of types) {
    if (type === 'claude') out.push('claude_code', 'claude_desktop')
    if (type === 'codex') out.push('codex')
    if (type === 'grok') out.push('grok_build')
  }
  return [...new Set(out)]
}
export function protocolForClient(client: CCSwitchClient): ClientType { return client === 'claude_code' || client === 'claude_desktop' ? 'claude' : client === 'codex' ? 'codex' : 'grok' }
export function ccSwitchImportForClient(input: { pageURL: string; client: CCSwitchClient; name: string; apiKey: string; config: CCSwitchClientConfig }) {
  const base = publicBaseURL(input.pageURL); if (!base) return null
  const protocol = protocolForClient(input.client)
  const endpoint = ccSwitchEndpoint(base, protocol)
  const model = input.client === 'grok_build' || input.client === 'codex' ? input.config.default_model : input.config.models?.sonnet?.model
  const params = new URLSearchParams({ resource: 'provider', app: input.client === 'claude_code' ? 'claude' : input.client === 'claude_desktop' ? 'claude-desktop' : input.client === 'codex' ? 'codex' : 'grokbuild', name: input.config.supplier_name || input.name, endpoint, homepage: base, apiKey: input.apiKey, enabled: 'true' })
  if (model?.trim()) params.set('model', model.trim())
  return { endpoint, href: 'ccswitch://v1/import?' + params }
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
