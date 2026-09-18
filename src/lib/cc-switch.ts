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

// Claude Code appends /v1/messages itself. Codex and Grok Build take a /v1 base URL
// and append /responses. CC Switch only switches the live client when enabled=true.
export function ccSwitchEndpoint(base: string, aiProvider: string) {
  return base.replace(/\/+$/, '') + (aiProvider === 'claude' ? '' : '/v1')
}

export function ccSwitchImport(input: { pageURL: string; aiProvider: string; name: string; apiKey: string }) {
  if (!['codex', 'claude', 'grok'].includes(input.aiProvider)) return null
  const base = publicBaseURL(input.pageURL)
  if (!base) return null
  const endpoint = ccSwitchEndpoint(base, input.aiProvider)
  const params = new URLSearchParams({
    resource: 'provider',
    app: input.aiProvider === 'grok' ? 'grokbuild' : input.aiProvider,
    name: input.name,
    endpoint,
    homepage: base,
    apiKey: input.apiKey,
    enabled: 'true',
  })
  return { endpoint, href: 'ccswitch://v1/import?' + params }
}
