import { TableRow, TableCell } from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { AppSelect } from '@/components/app-select'
import { SelectItem } from '@/components/ui/select'
import { useEffect, useState } from 'react'
import { ExternalLink, Pencil, Plus, Search, Trash2 } from 'lucide-react'
import { actions, useAIProviders, useAction, useProxies } from '@/data/store'
import type { Account, OAuthStart, AIProviderConfig } from '@/data/types'
import { Badge, Confirm, Empty, ErrorMessage, Field, Modal, PageHeader, QueryState, Submit, Table } from '@/components/shared'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

const platforms = [['codex', 'OpenAI / Codex'], ['claude', 'Anthropic / Claude'], ['grok', 'Grok'], ['dummy', 'Dummy']]
export function AIProviders() {
  const query = useAIProviders(), [search, setSearch] = useState(''), [platform, setPlatform] = useState(''), [editing, setEditing] = useState<Account | 'new' | null>(null), [removing, setRemoving] = useState<Account | null>(null)
  const remove = useAction(actions.deleteAIProvider, ['ai-providers', 'keys', 'usage'])
  const rows = query.data?.items.filter(a => (!platform || a.provider === platform) && `${a.name} ${a.provider}`.toLowerCase().includes(search.toLowerCase())) || []
  return <><PageHeader title="订阅管理" description="连接上游订阅或 API Key，统一管理访问配置与并发。" action={<Button onClick={() => setEditing('new')}><Plus />添加订阅</Button>} />
    <Card><div className="flex flex-wrap gap-3 p-5"><div className="relative min-w-48 flex-1"><Search className="absolute left-3 top-3 size-4 text-muted-foreground" /><Input aria-label="搜索订阅" className="pl-9" placeholder="搜索名称或平台" value={search} onChange={e => setSearch(e.target.value)} /></div><AppSelect aria-label="筛选平台" className="w-44" value={platform} onValueChange={value => setPlatform(value)}><SelectItem value="">全部平台</SelectItem>{platforms.map(([v, n]) => <SelectItem key={v} value={v}>{n}</SelectItem>)}</AppSelect></div>
    <QueryState query={query}>{rows.length ? <Table headers={['订阅名称', '平台', '认证方式', '并发', '状态', '操作']}>{rows.map(a => <TableRow key={a.id}><TableCell className="font-medium">{a.name}</TableCell><TableCell>{platforms.find(p => p[0] === a.provider)?.[1] || a.provider}</TableCell><TableCell>{a.auth_type === 'api_key' ? 'API Key' : 'OAuth Token'}</TableCell><TableCell>{a.config.max_concurrent_connections || 1}</TableCell><TableCell><Badge enabled={a.enabled} /></TableCell><TableCell><div className="flex gap-1"><Button variant="ghost" size="icon" aria-label={`编辑 ${a.name}`} onClick={() => setEditing(a)}><Pencil /></Button><Button variant="ghost" size="icon" aria-label={`删除 ${a.name}`} onClick={() => { remove.reset(); setRemoving(a) }}><Trash2 /></Button></div></TableCell></TableRow>)}</Table> : <Empty>{query.data?.items.length ? '没有匹配的订阅' : '添加第一个上游账号，开始使用 UniSub。'}</Empty>}</QueryState></Card>
    {editing && <AIProviderForm account={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} />}
    {removing && <Confirm title={`删除订阅「${removing.name}」？`} pending={remove.isPending} error={remove.error} onClose={() => setRemoving(null)} onConfirm={() => remove.mutate(removing.id, { onSuccess: () => setRemoving(null) })} />}
  </>
}
function AIProviderForm({ account, onClose }: { account?: Account; onClose: () => void }) {
  const [name, setName] = useState(account?.name || ''), [aiProvider, setAIProvider] = useState(account?.provider || 'codex'), [auth, setAuth] = useState(account?.auth_type || 'oauth')
  const [endpoint, setEndpoint] = useState(account?.config.api_endpoint || ''), [apiKey, setAPIKey] = useState(''), [credential, setCredential] = useState('')
  const [enabled, setEnabled] = useState(account?.enabled ?? true), [concurrency, setConcurrency] = useState(account?.config.max_concurrent_connections || 1), [timeout, setTimeout] = useState(account?.config.queue_timeout_seconds || 0), [proxy, setProxy] = useState(account?.config.proxy_group_id || '')
  const [oauth, setOAuth] = useState(false), [validation, setValidation] = useState<Error | null>(null)
  const proxies = useProxies(), save = useAction(actions.saveAIProvider, ['ai-providers'])
  function submit(e: React.FormEvent) {
    e.preventDefault(); setValidation(null)
    try {
      const config: AIProviderConfig = { ...account?.config, auth_type: auth, api_endpoint: endpoint.trim(), enabled, max_concurrent_connections: concurrency, queue_timeout_seconds: timeout, proxy_group_id: proxy }
      delete config.api_key; delete config.credential
      if (auth === 'api_key') { delete config.credential_id; delete config.oauth; if (apiKey.trim()) config.api_key = apiKey.trim() }
      else if (credential.trim()) { const parsed = JSON.parse(credential); if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed) || !parsed.access_token) throw new Error('凭据必须是包含 access_token 的 JSON 对象'); config.credential = parsed; delete config.credential_id; delete config.oauth }
      if (auth === 'oauth' && aiProvider !== 'dummy' && !config.credential && !config.credential_id && !config.oauth?.credential_id) throw new Error('请完成授权或填写 OAuth 凭据')
      save.mutate({ id: account?.id, name, provider: aiProvider, config }, { onSuccess: onClose })
    } catch (e) { setValidation(e instanceof SyntaxError ? new Error('凭据不是有效的 JSON') : e as Error) }
  }
  return <Modal title={account ? '编辑订阅' : '添加订阅'} onClose={onClose}><form onSubmit={submit} className="space-y-5">
    <div className="grid gap-4 sm:grid-cols-2"><Field label="账号名称"><Input required value={name} onChange={e => setName(e.target.value)} placeholder="例如 codex-main" /></Field><Field label="平台"><AppSelect disabled={!!account} value={aiProvider} onValueChange={value => { setAIProvider(value); setCredential('') }}>{platforms.map(([v, n]) => <SelectItem key={v} value={v}>{n}</SelectItem>)}</AppSelect></Field><Field label="最大并发"><Input type="number" min={1} max={100} required value={concurrency} onChange={e => setConcurrency(Number(e.target.value))} /></Field><Field label="排队超时（秒）" hint="0 使用默认 180 秒"><Input type="number" min={0} max={300} required value={timeout} onChange={e => setTimeout(Number(e.target.value))} /></Field></div>
    <Field label="代理组"><AppSelect value={proxy} onValueChange={value => setProxy(value)}><SelectItem value="">不使用代理组</SelectItem>{proxies.data?.map(p => <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>)}</AppSelect></Field><ErrorMessage error={proxies.error} />
    <div className="grid gap-4 sm:grid-cols-2"><Field label="认证方式"><AppSelect value={auth} onValueChange={value => setAuth(value)}><SelectItem value="oauth">OAuth Token</SelectItem><SelectItem value="api_key">API Key</SelectItem></AppSelect></Field><Field label="状态"><AppSelect value={String(enabled)} onValueChange={value => setEnabled(value === 'true')}><SelectItem value="true">已启用</SelectItem><SelectItem value="false">已停用</SelectItem></AppSelect></Field></div>
    <Field label="Base URL" hint="留空使用平台默认地址；可填写兼容接口的地址。"><Input type="url" value={endpoint} onChange={e => setEndpoint(e.target.value)} placeholder="https://api.example.com/v1" /></Field>
    {auth === 'api_key' ? <Field label="上游 API Key" hint={account?.auth_type === 'api_key' ? '留空保留原密钥' : undefined}><Input type="password" required={account?.auth_type !== 'api_key'} autoComplete="off" value={apiKey} onChange={e => setAPIKey(e.target.value)} placeholder={account?.auth_type === 'api_key' ? '••••••••（已保存）' : 'sk-…'} /></Field> : <div className="space-y-3"><div className="flex items-center justify-between"><span className="text-sm font-medium">OAuth 凭据</span><Button type="button" size="sm" variant="outline" onClick={() => setOAuth(true)}>网页登录授权<ExternalLink /></Button></div><Textarea aria-label="OAuth 凭据 JSON" value={credential} onChange={e => setCredential(e.target.value)} spellCheck={false} placeholder={account ? '留空保留现有凭据，或粘贴新的 JSON' : '{"access_token":"…","refresh_token":"…"}'} /></div>}
    <ErrorMessage error={validation || save.error} /><div className="flex justify-end gap-3"><Button type="button" variant="outline" onClick={onClose}>取消</Button><Submit pending={save.isPending} /></div>
  </form>{oauth && <OAuthForm aiProvider={aiProvider} proxy={account?.config.proxy} onClose={() => setOAuth(false)} onComplete={value => { setCredential(JSON.stringify(value, null, 2)); setOAuth(false) }} />}</Modal>
}
function OAuthForm({ aiProvider, proxy, onClose, onComplete }: { aiProvider: string; proxy?: string; onClose: () => void; onComplete: (value: unknown) => void }) {
  const [session, setSession] = useState<OAuthStart | null>(null), [code, setCode] = useState(''), [error, setError] = useState<Error | null>(null), [busy, setBusy] = useState(false)
  useEffect(() => {
    let active = true, timer: ReturnType<typeof setTimeout>
    async function poll(s: OAuthStart) {
      if (!active) return
      if (s.expires_at && Date.parse(s.expires_at) <= Date.now()) { setError(new Error('授权已过期，请关闭后重试')); return }
      try {
        const result = await (s.user_code || aiProvider === 'dummy' ? actions.oauthPoll : actions.oauthStatus)({ aiProvider, session_id: s.session_id })
        if (!active) return
        if (result.result_id) { const data = await actions.oauthResult(result.result_id); if (active) onComplete(data.result) }
        else timer = setTimeout(() => poll(s), Math.max(5, result.interval_seconds || 5) * 1000)
      } catch (e) { if (active) setError(e as Error) }
    }
    actions.oauthStart({ aiProvider, proxy }).then(s => { if (!active) return; setSession(s); timer = setTimeout(() => poll(s), 5000) }).catch(e => { if (active) setError(e) })
    return () => { active = false; clearTimeout(timer) }
  }, [aiProvider, proxy])
  async function complete(e: React.FormEvent) {
    e.preventDefault(); if (!session) return; setBusy(true); setError(null)
    try {
      let rawCode = code.trim(), state = new URL(session.authorization_url || session.auth_url || '').searchParams.get('state') || ''
      if (/^https?:\/\//.test(rawCode)) { const url = new URL(rawCode); state = url.searchParams.get('state') || state; rawCode = url.searchParams.get('code') || '' }
      else if (rawCode.includes('#')) [rawCode, state] = rawCode.split('#')
      const result = await actions.oauthComplete({ aiProvider, session_id: session.session_id, code: rawCode, state })
      const data = await actions.oauthResult(result.result_id); onComplete(data.result)
    } catch (e) { setError(e as Error) } finally { setBusy(false) }
  }
  const url = session?.verification_uri || session?.authorization_url || session?.auth_url
  const safeURL = url && /^https?:\/\//i.test(url) ? url : undefined
  return <Modal title={`${platforms.find(p => p[0] === aiProvider)?.[1]} 授权`} description="在平台官方页面完成授权。UniSub 不会接触你的账号密码。" onClose={onClose}>
    <div className="space-y-5"><ErrorMessage error={error} />{!session && !error && <p role="status">正在申请授权…</p>}{session?.user_code && <div className="rounded-lg border border-dashed p-5 text-center font-mono text-3xl tracking-widest">{session.user_code}</div>}{safeURL && <Button nativeButton={false} render={<a href={safeURL} target="_blank" rel="noopener noreferrer" />}>打开授权页面<ExternalLink /></Button>}{session && (session.user_code || aiProvider === 'dummy' ? <p role="status" className="text-sm text-muted-foreground">等待授权完成，将自动绑定凭据…</p> : <form onSubmit={complete} className="space-y-4"><Field label="授权码或回调 URL"><Textarea required value={code} onChange={e => setCode(e.target.value)} placeholder="粘贴授权码、code#state 或回调地址" /></Field><Submit pending={busy}>完成绑定</Submit></form>)}</div>
  </Modal>
}
