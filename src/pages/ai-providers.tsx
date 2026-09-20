import { ProviderQuota } from '@/components/provider-quota'
import { AddAIProviderButton, type ProviderKind } from '@/components/add-ai-provider-button'
import { DetailTableRow } from '@/components/detail-table-row'
import { Table as UITable, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Checkbox } from '@/components/ui/checkbox'
import { Textarea } from '@/components/ui/textarea'
import { AppSelect } from '@/components/app-select'
import { SelectItem } from '@/components/ui/select'
import { useEffect, useState } from 'react'
import { ExternalLink, Pencil, Search, Trash2 } from 'lucide-react'
import { actions, useAIProviders, useAction, useProxies, useAICatalog } from '@/data/store'
import type { Account, OAuthStart, AIProviderConfig, ClientType, GroupMember } from '@/data/types'
import { Badge, Confirm, Empty, ErrorMessage, Field, Modal, PageHeader, QueryState, Submit, Table } from '@/components/shared'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { clientTypeLabel } from '@/lib/utils'
import { defaultSubscriptionPlan, subscriptionPlanLabel, subscriptionPlansForProvider } from '@/lib/subscription-plan'

const platforms = [['codex', 'OpenAI / Codex'], ['claude', 'Anthropic / Claude'], ['grok', 'Grok'], ['api', 'API 服务'], ['group', '账号组'], ['dummy', 'Dummy']]
const kinds = [['subscription', '订阅'], ['api', 'API'], ['group', '组']] as const
const supplierNames: Record<string, string> = { anthropic: 'Anthropic', openai: 'OpenAI', grok: 'xAI', deepseek: 'Deepseek', zhipu: '智谱', kimi: 'Kimi' }
const platformClients: Record<string, ClientType> = { claude: 'Anthropic', dummy: 'Anthropic', codex: 'OpenAI', grok: 'Grok' }
const platformSuppliers: Record<string, string> = { claude: 'anthropic', codex: 'openai', grok: 'grok' }
function providerKind(a: Account) { return a.config.kind || (a.provider === 'group' ? 'group' : a.auth_type === 'api_key' ? 'api' : 'subscription') }
function allowedClient(a: Account): ClientType {
  if (providerKind(a) === 'subscription' && a.config.official_only) {
    const client = platformClients[a.provider]
    if (client) return client
  }
  return a.config.client_type || 'Any'
}
function supplierCell(a: Account) {
  if (providerKind(a) === 'group') return null
  const name = supplierNames[a.config.supplier || platformSuppliers[a.provider]] || (a.provider === 'api' ? '自定义' : a.provider)
  const plan = providerKind(a) === 'subscription' ? subscriptionPlanLabel(a.config.subscription_plan) : ''
  return plan ? <>{name}<div className="mt-1 text-xs text-muted-foreground">{plan}</div></> : <>{name}</>
}
export function AIProviders() {
  const query = useAIProviders(), [search, setSearch] = useState(''), [platform, setPlatform] = useState(''), [editing, setEditing] = useState<Account | ProviderKind | null>(null), [removing, setRemoving] = useState<Account | null>(null)
  const remove = useAction(actions.deleteAIProvider, ['ai-providers', 'provider-options', 'keys', 'usage'])
  const rows = query.data?.items.filter(a => {
    if (platform && providerKind(a) !== platform) return false
    const plan = subscriptionPlanLabel(a.config.subscription_plan)
    const haystack = `${a.name} ${a.provider} ${a.config.supplier || ''} ${supplierNames[a.config.supplier || ''] || ''} ${a.config.subscription_plan || ''} ${plan}`
    return haystack.toLowerCase().includes(search.toLowerCase())
  }) || []
  return <><PageHeader title="账号管理" description="统一管理订阅账户、API 服务与调度组，配置客户端访问和成员权重。" action={<div className="flex flex-wrap gap-2"><Button variant="outline" nativeButton={false} role="link" render={<a href="#ai-catalog" />}>模型供应商</Button><AddAIProviderButton onSelect={setEditing} /></div>} />
    <Card><div className="flex flex-wrap gap-3 p-5"><div className="relative min-w-48 flex-1"><Search className="absolute left-3 top-3 size-4 text-muted-foreground" /><Input aria-label="搜索账号" className="pl-9" placeholder="搜索名称、供应商或套餐" value={search} onChange={e => setSearch(e.target.value)} /></div><AppSelect aria-label="筛选类型" className="w-44" value={platform} onValueChange={setPlatform}><SelectItem value="">全部类型</SelectItem>{kinds.map(([v, n]) => <SelectItem key={v} value={v}>{n}</SelectItem>)}</AppSelect></div>
    <QueryState query={query}>{rows.length ? <Table headers={['名称', '类型', '供应商 / 成员', '允许的客户端', '并发', '状态', '额度', '操作']}>{rows.map(a => <DetailTableRow key={a.id} aria-label={`查看 ${a.name} 详情`} onOpen={() => setEditing(a)}><TableCell className="font-medium">{a.name}</TableCell><TableCell>{kinds.find(k => k[0] === providerKind(a))?.[1]}</TableCell><TableCell>{providerKind(a) === 'group' ? <><span>{a.config.members?.length || 0} 个成员</span><div className="mt-1 max-w-64 truncate text-xs text-muted-foreground" title={a.config.members?.map(m => `${query.data?.items.find(p => p.id === m.id)?.name || m.id}（${m.weight}）`).join('、')}>{a.config.members?.map(m => `${query.data?.items.find(p => p.id === m.id)?.name || m.id}（${m.weight}）`).join('、') || '尚未配置成员'}</div></> : supplierCell(a)}</TableCell><TableCell>{allowedClient(a) === 'Any' ? '不限客户端' : clientTypeLabel(allowedClient(a))}{providerKind(a) === 'subscription' && allowedClient(a) !== 'Any' && <div className="mt-1 text-xs text-muted-foreground">仅原厂客户端</div>}</TableCell><TableCell>{providerKind(a) === 'group' ? '按成员限制' : a.config.max_concurrent_connections || 1}</TableCell><TableCell><Badge enabled={a.enabled} /></TableCell><TableCell>{providerKind(a) !== 'group' && <ProviderQuota account={a} />}</TableCell><TableCell><div className="flex gap-1"><Button variant="ghost" size="icon" aria-label={`编辑 ${a.name}`} onClick={() => setEditing(a)}><Pencil /></Button><Button variant="ghost" size="icon" aria-label={`删除 ${a.name}`} onClick={() => { remove.reset(); setRemoving(a) }}><Trash2 /></Button></div></TableCell></DetailTableRow>)}</Table> : <Empty>{query.data?.items.length ? '没有匹配的账号' : '添加订阅账户或 API 服务，再通过组统一调度多个账号。'}</Empty>}</QueryState></Card>
    {editing && (typeof editing === 'string' ? <AIProviderForm initialKind={editing} onClose={() => setEditing(null)} /> : <AIProviderForm account={editing} onClose={() => setEditing(null)} />)}
    {removing && <Confirm title={`删除账号「${removing.name}」？`} pending={remove.isPending} error={remove.error} onClose={() => setRemoving(null)} onConfirm={() => remove.mutate(removing.id, { onSuccess: () => setRemoving(null) })} />}
  </>
}
export function AIProviderForm({ account, initialKind, onClose }: ({ account: Account; initialKind?: never } | { account?: undefined; initialKind: ProviderKind }) & { onClose: () => void }) {
  const catalog = useAICatalog(), providers = useAIProviders()
  const kind = account ? providerKind(account) : initialKind
  const [supplier, setSupplier] = useState(account?.config.supplier || (account ? platformSuppliers[account.provider] : '') || '')
  const [apiEndpoint, setAPIEndpoint] = useState(account?.config.api_endpoint || '')
  const [client, setClient] = useState<ClientType>(account ? allowedClient(account) : 'Any')
  const [members, setMembers] = useState<GroupMember[]>(account?.config.members || [])
  const [name, setName] = useState(account?.name || ''), [aiProvider, setAIProvider] = useState(account?.provider || (kind === 'subscription' ? 'codex' : kind)), auth = kind === 'api' ? 'api_key' : 'oauth'
  const [apiKey, setAPIKey] = useState(''), [credential, setCredential] = useState('')
  const [enabled, setEnabled] = useState(account?.enabled ?? true), [concurrency, setConcurrency] = useState(account?.config.max_concurrent_connections || 1), [timeout, setTimeout] = useState(account?.config.queue_timeout_seconds || 0), [proxy, setProxy] = useState(account?.config.proxy_group_id ? String(account.config.proxy_group_id) : '')
  const initialProvider = account?.provider || (kind === 'subscription' ? 'codex' : kind)
  const planOptions = subscriptionPlansForProvider(aiProvider)
  const [subscriptionPlan, setSubscriptionPlan] = useState(
    () => account?.config.subscription_plan || defaultSubscriptionPlan(initialProvider) || ''
  )
  const [oauth, setOAuth] = useState(false), [validation, setValidation] = useState<Error | null>(null)
  const proxies = useProxies(), save = useAction(actions.saveAIProvider, ['ai-providers', 'provider-options'])
  const isGroup = kind === 'group'
  const nativeClient = platformClients[aiProvider]
  const selectedClient = kind === 'subscription' ? (client !== 'Any' && nativeClient ? nativeClient : 'Any') : client
  const memberOptions = providers.data?.items.filter(p => p.id !== account?.id && providerKind(p) !== 'group') || []
  const incompatibleMembers = isGroup ? memberOptions.filter(p => members.some(m => m.id === p.id) && allowedClient(p) !== client) : []
  function changeSubscriptionPlatform(value: string) {
    setAIProvider(value)
    setCredential('')
    setValidation(null)
    const next = subscriptionPlansForProvider(value)
    setSubscriptionPlan(prev => next.some(p => p.id === prev) ? prev : defaultSubscriptionPlan(value) || next[0]?.id || '')
  }
  function submit(e: React.FormEvent) {
    e.preventDefault(); setValidation(null)
    try {
      if (auth === 'api_key' && !supplier) throw new Error('请选择模型供应商，服务地址在模型供应商中配置')
      const config: AIProviderConfig = { ...account?.config, kind: isGroup ? 'group' : auth === 'api_key' ? 'api' : 'subscription', auth_type: auth, enabled, max_concurrent_connections: concurrency, queue_timeout_seconds: timeout, client_type: selectedClient }
      if (proxy) config.proxy_group_id = Number(proxy); else delete config.proxy_group_id
      delete config.official_only
      delete config.client_types; delete config.proxy; delete config.members; delete config.supplier
      delete config.subscription_plan
      if (isGroup) {
        if (!members.length) throw new Error('请选择至少一个组成员')
        if (incompatibleMembers.length) throw new Error('组的允许客户端必须与所有组成员一致')
        config.members = members; config.auth_type = 'oauth'
        delete config.max_concurrent_connections; delete config.queue_timeout_seconds; delete config.api_endpoint; delete config.proxy_group_id; delete config.credential_id; delete config.oauth; delete config.api_key; delete config.credential
        save.mutate({ id: account?.id, name, provider: aiProvider, config }, { onSuccess: onClose }); return
      }
      if (auth === 'api_key' && supplier) config.supplier = supplier
      if (auth === 'api_key' && apiEndpoint.trim()) config.api_endpoint = apiEndpoint.trim()
      else delete config.api_endpoint
      if (kind === 'subscription') {
        if (!subscriptionPlan || !planOptions.some(p => p.id === subscriptionPlan)) throw new Error('请选择订阅套餐')
        config.subscription_plan = subscriptionPlan
      }
      delete config.api_key; delete config.credential
      if (auth === 'api_key') { delete config.credential_id; delete config.oauth; if (apiKey.trim()) config.api_key = apiKey.trim() }
      else if (credential.trim()) { const parsed = JSON.parse(credential); if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed) || !parsed.access_token) throw new Error('凭据必须是包含 access_token 的 JSON 对象'); config.credential = parsed; delete config.credential_id; delete config.oauth }
      if (auth === 'oauth' && aiProvider !== 'dummy' && !config.credential && !config.credential_id && !config.oauth?.credential_id) throw new Error('请完成授权或填写 OAuth 凭据')
      save.mutate({ id: account?.id, name, provider: aiProvider, config }, { onSuccess: onClose })
    } catch (e) { setValidation(e instanceof SyntaxError ? new Error('凭据不是有效的 JSON') : e as Error) }
  }
  return <Modal wide title={`${account ? '编辑' : '添加'}${kinds.find(([value]) => value === kind)?.[1]}账号`} description={kind === 'group' ? '组合已有账号；权重高者优先，同权重优先复用会话，否则随机选择。' : kind === 'api' ? '使用 API Key 连接模型服务；未填写覆盖地址时使用模型供应商的默认 URL。' : '绑定 Anthropic、OpenAI 或 Grok 订阅账户，由账户决定模型供应商。'} onClose={onClose}><form onSubmit={submit} className="space-y-5">
    {isGroup ? <>
      <div className="grid items-start gap-6 sm:grid-cols-2">
        <div className="space-y-4">
          <Field label="名称"><Input required value={name} onChange={e => setName(e.target.value)} placeholder="例如 provider-group" /></Field>
          <Field label="状态"><AppSelect value={String(enabled)} onValueChange={value => setEnabled(value === 'true')}><SelectItem value="true">已启用</SelectItem><SelectItem value="false">已停用</SelectItem></AppSelect></Field>
          <Field label="允许的客户端" hint="必须与所有组成员一致；Any 组仅允许 Any 成员。"><AppSelect value={client} onValueChange={value => { setClient(value as ClientType); setValidation(null) }}>{(['Any', 'Anthropic', 'OpenAI', 'Grok'] as ClientType[]).map(value => <SelectItem key={value} value={value}>{clientTypeLabel(value)}</SelectItem>)}</AppSelect></Field>
        </div>
        <section aria-label="组成员" className="min-w-0 space-y-3">
          <h3 className="text-sm font-medium">组成员</h3>
          <div className="overflow-hidden rounded-lg border">
            <UITable aria-label="组成员"><TableHeader><TableRow>
              <TableHead className="w-10 px-2">选择</TableHead><TableHead className="px-2">成员</TableHead><TableHead className="px-2">状态</TableHead><TableHead className="w-20 px-2">权重</TableHead>
            </TableRow></TableHeader><TableBody>{memberOptions.map(p => {
              const member = members.find(m => m.id === p.id)
              return <TableRow key={p.id} className={member && allowedClient(p) !== client ? 'bg-destructive/10' : undefined}>
                <TableCell className="px-2 py-3"><Checkbox aria-label={p.name} checked={!!member} onCheckedChange={checked => setMembers(checked ? [...members, { id: p.id, weight: 3 }] : members.filter(m => m.id !== p.id))} /></TableCell>
                <TableCell className="whitespace-normal px-2 py-3"><div className="break-words font-medium">{p.name}</div><div className="mt-1 text-xs text-muted-foreground">{allowedClient(p) === 'Any' ? '不限客户端' : clientTypeLabel(allowedClient(p))}</div></TableCell>
                <TableCell className="px-2 py-3"><span className={p.enabled ? 'text-emerald-300' : 'text-muted-foreground'} title={p.enabled ? undefined : '已停用，不参与调度'}>{p.enabled ? '已启用' : '已停用'}</span></TableCell>
                <TableCell className="px-2 py-3"><Input aria-label={`${p.name} 权重`} className="w-16" type="number" min={1} max={5} step={1} disabled={!member} required={!!member} value={member?.weight ?? 3} onChange={e => setMembers(members.map(m => m.id === p.id ? { ...m, weight: Number(e.target.value) } : m))} /></TableCell>
              </TableRow>
            })}</TableBody></UITable>
          </div>
          {!providers.isPending && !memberOptions.length && <p className="text-sm text-muted-foreground">暂无可选成员，请先创建订阅或 API 类型的账号。组不能嵌套。</p>}
          {providers.isPending && <p role="status">正在加载成员…</p>}
          <p className="text-xs text-muted-foreground">权重 1～5，默认 3；优先使用高权重，同权重优先复用会话，否则随机选择。</p>
          {incompatibleMembers.length > 0 && <p role="alert" className="text-sm text-destructive">客户端不一致：{incompatibleMembers.map(p => p.name).join('、')}。请调整组的允许客户端或移除这些成员。</p>}
          <ErrorMessage error={providers.error} />
        </section>
      </div>
    </> : <>
      <div>
        <div className={`grid items-start gap-6 ${kind === 'api' ? 'sm:grid-cols-3' : 'sm:grid-cols-2'}`}>
          <div className="space-y-4">
            <Field label="名称"><Input required value={name} onChange={e => setName(e.target.value)} placeholder="例如 codex-main" /></Field>
            <Field label="状态"><AppSelect value={String(enabled)} onValueChange={value => setEnabled(value === 'true')}><SelectItem value="true">已启用</SelectItem><SelectItem value="false">已停用</SelectItem></AppSelect></Field>
          </div>
          <div className={kind === 'api' ? 'contents' : 'space-y-4'}>
            <div className="space-y-4">
            {kind === 'subscription' ? <>
              <Field label="订阅平台"><AppSelect disabled={!!account} value={aiProvider} onValueChange={changeSubscriptionPlatform}>{platforms.filter(([v]) => !['api', 'group'].includes(v)).map(([v, n]) => <SelectItem key={v} value={v}>{n}</SelectItem>)}</AppSelect></Field>
              <Field label="订阅套餐" hint="只使用配置值，不从上游推断。"><AppSelect required value={subscriptionPlan} onValueChange={setSubscriptionPlan}>{planOptions.map(p => <SelectItem key={p.id} value={p.id}>{p.label}</SelectItem>)}</AppSelect></Field>
              <label className="flex min-h-10 items-center gap-2"><input type="checkbox" disabled={!nativeClient} checked={selectedClient !== 'Any'} onChange={e => setClient(e.target.checked ? nativeClient : 'Any')} />仅允许原厂客户端</label>
            </> : <>
              <Field label="模型供应商"><AppSelect required value={supplier} onValueChange={setSupplier}><SelectItem value="">请选择模型供应商</SelectItem>{catalog.data?.catalog.suppliers.map(s => <SelectItem key={s.id} value={s.id}>{s.name}</SelectItem>)}</AppSelect></Field>
              <Field label="覆盖 URL" hint="可选；留空使用模型供应商默认 URL"><Input type="url" value={apiEndpoint} onChange={e => setAPIEndpoint(e.target.value)} placeholder="https://api.example.com/v1" /></Field>
              <Field label="上游 API Key" hint={account?.auth_type === 'api_key' ? '留空保留原密钥' : undefined}><Input type="text" required={account?.auth_type !== 'api_key'} autoComplete="off" value={apiKey} onChange={e => setAPIKey(e.target.value)} placeholder={account?.auth_type === 'api_key' ? '••••••••（已保存）' : 'sk-…'} /></Field>
              <Field label="允许的客户端" hint="单选；Any 包括未知客户端。"><AppSelect value={client} onValueChange={value => setClient(value as ClientType)}>{(['Any', 'Anthropic', 'OpenAI', 'Grok'] as ClientType[]).map(value => <SelectItem key={value} value={value}>{clientTypeLabel(value)}</SelectItem>)}</AppSelect></Field>
            </>}
            </div>
            <div className="space-y-4">
            <Field label="代理组"><AppSelect value={proxy} onValueChange={value => setProxy(value)}><SelectItem value="">不使用代理组</SelectItem>{proxies.data?.map(p => <SelectItem key={p.id} value={String(p.id)}>{p.name}</SelectItem>)}</AppSelect></Field>
            <Field label="最大并发"><Input type="number" min={1} max={100} required value={concurrency} onChange={e => setConcurrency(Number(e.target.value))} /></Field>
            <Field label="排队超时（秒）" hint="0 使用默认 180 秒"><Input type="number" min={0} max={300} required value={timeout} onChange={e => setTimeout(Number(e.target.value))} /></Field>
            </div>
          </div>
        </div>
      </div>
      <ErrorMessage error={proxies.error} />
      {kind === 'api' && <ErrorMessage error={catalog.error} />}
      {kind === 'subscription' && <section aria-label="OAuth 凭据" className="border-t pt-5"><div className="space-y-3"><div className="flex items-center justify-between"><h3 className="text-sm font-medium">OAuth 凭据</h3><Button type="button" size="sm" variant="outline" onClick={() => setOAuth(true)}>网页登录授权<ExternalLink /></Button></div><Textarea aria-label="OAuth 凭据 JSON" value={credential} onChange={e => setCredential(e.target.value)} spellCheck={false} placeholder={account ? '留空保留现有凭据，或粘贴新的 JSON' : '{"access_token":"…","refresh_token":"…"}'} /></div></section>}
    </>}
    <ErrorMessage error={validation || save.error} /><div className="flex justify-end gap-3"><Button type="button" variant="outline" onClick={onClose}>取消</Button><Submit pending={save.isPending} /></div>
  </form>{oauth && <OAuthForm aiProvider={aiProvider} proxyGroupID={proxy ? Number(proxy) : undefined} onClose={() => setOAuth(false)} onComplete={value => { setCredential(JSON.stringify(value, null, 2)); setOAuth(false) }} />}</Modal>
}
function OAuthForm({ aiProvider, proxyGroupID, onClose, onComplete }: { aiProvider: string; proxyGroupID?: number; onClose: () => void; onComplete: (value: unknown) => void }) {
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
    actions.oauthStart({ aiProvider, proxy_group_id: proxyGroupID }).then(s => { if (!active) return; setSession(s); timer = setTimeout(() => poll(s), 5000) }).catch(e => { if (active) setError(e) })
    return () => { active = false; clearTimeout(timer) }
  }, [aiProvider, proxyGroupID])
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
