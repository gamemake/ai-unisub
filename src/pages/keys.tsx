import { DetailTableRow } from '@/components/detail-table-row'
import { TableCell } from '@/components/ui/table'
import { AppSelect } from '@/components/app-select'
import { SelectItem } from '@/components/ui/select'
import { useState } from 'react'
import { Copy, Eye, Plus, Trash2 } from 'lucide-react'
import { actions, useProviderOptions, useAction, useKeys } from '@/data/store'
import type { APIKey } from '@/data/types'
import { Badge, Confirm, Empty, ErrorMessage, Field, Modal, PageHeader, QueryState, Submit, Table } from '@/components/shared'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Card } from '@/components/ui/card'
import { date } from '@/lib/utils'

export function Keys() {
  const query = useKeys(), accounts = useProviderOptions(), [adding, setAdding] = useState(false), [revealed, setRevealed] = useState<APIKey | null>(null), [removing, setRemoving] = useState<APIKey | null>(null)
  const remove = useAction(actions.deleteKey, ['keys'])
  const rows = query.data?.items || []
  return <><PageHeader title="API Key" description="为客户端签发访问密钥，每把 Key 绑定一个 AI Provider（订阅、API 或组）。" action={<Button onClick={() => setAdding(true)}><Plus />签发 Key</Button>} /><Card><QueryState query={query}>{rows.length ? <Table headers={['名称', '绑定 AI Provider', '密钥', '有效期', '操作']}>{rows.map(k => <DetailTableRow key={k.id} aria-label={`查看 ${k.name} 详情`} onOpen={() => setRevealed(k)}><TableCell className="font-medium">{k.name}</TableCell><TableCell>{accounts.data?.items.find(a => a.id === k.account_id)?.name || k.account_id}</TableCell><TableCell className="font-mono text-muted-foreground">••••••••{k.key.slice(-4)}</TableCell><TableCell>{k.expires_at ? <><Badge enabled={Date.parse(k.expires_at) > Date.now()}>{Date.parse(k.expires_at) > Date.now() ? '有效' : '已过期'}</Badge><div className="mt-1 text-xs text-muted-foreground">{date(k.expires_at)}</div></> : <Badge enabled>长期有效</Badge>}</TableCell><TableCell><div className="flex gap-1"><Button variant="ghost" size="icon" aria-label={`查看 ${k.name}`} onClick={() => setRevealed(k)}><Eye /></Button><Button variant="ghost" size="icon" aria-label={`删除 ${k.name}`} onClick={() => { remove.reset(); setRemoving(k) }}><Trash2 /></Button></div></TableCell></DetailTableRow>)}</Table> : <Empty>暂无 API Key</Empty>}</QueryState></Card>
    {adding && <KeyForm onClose={() => setAdding(false)} onCreated={k => { setAdding(false); setRevealed(k) }} />}{revealed && <KeyDetails value={revealed} aiProvider={accounts.data?.items.find(a => a.id === revealed.account_id)?.provider || ''} onClose={() => setRevealed(null)} />}{removing && <Confirm title={`删除密钥「${removing.name}」？`} error={remove.error} pending={remove.isPending} onClose={() => setRemoving(null)} onConfirm={() => remove.mutate(removing.id, { onSuccess: () => setRemoving(null) })} />}
  </>
}
function KeyForm({ onClose, onCreated }: { onClose: () => void; onCreated: (key: APIKey) => void }) {
  const accounts = useProviderOptions(), [name, setName] = useState(''), [account, setAccount] = useState(''), [days, setDays] = useState(0), create = useAction(actions.createKey, ['keys'])
  return <Modal title="签发 API Key" onClose={onClose}><QueryState query={accounts}><form className="space-y-5" onSubmit={e => { e.preventDefault(); create.mutate({ name, account_id: account, valid_seconds: days * 86400 }, { onSuccess: onCreated }) }}><Field label="名称"><Input required maxLength={64} value={name} onChange={e => setName(e.target.value)} placeholder="例如 Claude Code" /></Field><Field label="绑定 AI Provider"><AppSelect required value={account} onValueChange={value => setAccount(value)}><SelectItem value="">请选择 AI Provider</SelectItem>{accounts.data?.items.filter(a => a.enabled).map(a => <SelectItem key={a.id} value={a.id}>{a.name} · {a.provider}</SelectItem>)}</AppSelect></Field><Field label="有效期（天）" hint="0 表示长期有效"><Input required type="number" min={0} max={36500} step={1} value={days} onChange={e => setDays(Number(e.target.value))} /></Field><ErrorMessage error={create.error} /><div className="flex justify-end"><Submit pending={create.isPending}>签发 Key</Submit></div></form></QueryState></Modal>
}
export function KeyDetails({ value, aiProvider, onClose }: { value: APIKey; aiProvider: string; onClose: () => void }) {
  const [copied, setCopied] = useState(false), [error, setError] = useState<Error | null>(null)
  async function copy() { try { await navigator.clipboard.writeText(value.key); setCopied(true) } catch { setError(new Error('无法访问剪贴板，请手动选择并复制密钥')) } }
  const endpoint = location.origin + (aiProvider === 'claude' ? '' : '/v1')
  const params = new URLSearchParams({ resource: 'provider', app: aiProvider === 'grok' ? 'grokbuild' : aiProvider, name: value.name, endpoint, homepage: location.origin, apiKey: value.key })
  return <Modal title={value.name} description="请妥善保存密钥，仅与需要调用此 AI Provider的客户端共享。" onClose={onClose}><div className="space-y-4"><Field label="API Key"><Input readOnly value={value.key} className="font-mono" onFocus={e => e.target.select()} /></Field><Field label="Base URL"><Input readOnly value={endpoint} /></Field><ErrorMessage error={error} /><div className="flex gap-3"><Button onClick={copy} variant="outline"><Copy />{copied ? '已复制' : '复制密钥'}</Button>{['codex', 'claude', 'grok'].includes(aiProvider) && <Button variant="outline" nativeButton={false} render={<a href={'ccswitch://v1/import?' + params} />}>导入 CC Switch</Button>}</div></div></Modal>
}
