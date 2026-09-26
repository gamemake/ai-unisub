import { DetailTableRow } from '@/components/detail-table-row'
import { TableCell } from '@/components/ui/table'
import { useState } from 'react'
import { ArrowUp, ArrowDown, Pencil, Plus, Trash2 } from 'lucide-react'
import { actions, useAction, useProxies } from '@/data/store'
import type { ProxyGroup, ProxyHealth, ProxyState } from '@/data/types'
import { Badge, Confirm, Empty, ErrorMessage, Field, Modal, PageHeader, QueryState, Submit, Table } from '@/components/shared'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { date } from '@/lib/utils'

function healthSummary(group: ProxyGroup) {
  const states = group.config.proxies.map(url => group.state.proxies?.[url]).filter((s): s is ProxyState => !!s)
  if (!states.length) return '未使用'
  return `${states.filter(s => s.healthy).length} / ${group.config.proxies.length} 可用`
}
function HealthBadge({ health }: { health?: ProxyHealth }) { return health ? <Badge enabled={health.healthy}>{health.healthy ? '可用' : '不可用'}</Badge> : <Badge enabled={false}>未使用</Badge> }
function healthTimes(health: ProxyHealth) { return `最后成功：${date(health.last_success)} · 最后失败：${date(health.last_failure)}` }

export function Proxies() {
  const query = useProxies(), [editing, setEditing] = useState<ProxyGroup | 'new' | null>(null), [removing, setRemoving] = useState<ProxyGroup | null>(null), remove = useAction(actions.deleteProxy, ['proxies'])
  return <><PageHeader title="代理管理" description="配置上游连接的代理组和按优先级排列的代理地址。" action={<Button onClick={() => setEditing('new')}><Plus />添加代理组</Button>} /><Card><QueryState query={query}>{query.data?.length ? <Table headers={['名称', '代理数量', '健康状态', '操作']}>{query.data.map(g => <DetailTableRow key={g.id} aria-label={`查看 ${g.config.name} 详情`} onOpen={() => setEditing(g)}><TableCell className="font-medium">{g.config.name}</TableCell><TableCell>{g.config.proxies.length}</TableCell><TableCell className="text-muted-foreground">{healthSummary(g)}</TableCell><TableCell><div className="flex gap-1"><Button variant="ghost" size="icon" aria-label={`编辑 ${g.config.name}`} onClick={() => setEditing(g)}><Pencil /></Button><Button variant="ghost" size="icon" aria-label={`删除 ${g.config.name}`} onClick={() => { remove.reset(); setRemoving(g) }}><Trash2 /></Button></div></TableCell></DetailTableRow>)}</Table> : <Empty>暂无代理组；不使用代理时可以直接连接上游。</Empty>}</QueryState></Card>{editing && <ProxyForm group={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} />}{removing && <Confirm title={`删除代理组「${removing.config.name}」？`} pending={remove.isPending} error={remove.error} onClose={() => setRemoving(null)} onConfirm={() => remove.mutate(removing.id, { onSuccess: () => setRemoving(null) })} />}</>
}
type EditorProxy = { url: string; rowID: string }
function ProxyForm({ group, onClose }: { group?: ProxyGroup; onClose: () => void }) {
  const [name, setName] = useState(group?.config.name || ''), [rows, setRows] = useState<EditorProxy[]>(() => (group?.config.proxies || []).map(url => ({ url, rowID: crypto.randomUUID() })))
  const [validation, setValidation] = useState<Error | null>(null), save = useAction(actions.saveProxy, ['proxies'])
  function update(rowID: string, url: string) { setRows(old => old.map(p => p.rowID === rowID ? { ...p, url } : p)) }
  function move(index: number, direction: number) { setRows(old => { const next = [...old]; [next[index], next[index + direction]] = [next[index + direction], next[index]]; return next }) }
  function valid(url: string) {
    try {
      const value = url.trim(), parsed = new URL(value)
      return !/\s/.test(value) && ['http:', 'https:', 'socks5:', 'socks5h:'].includes(parsed.protocol.toLowerCase()) && !!parsed.hostname && !parsed.search && !parsed.hash && (!parsed.port || (Number(parsed.port) >= 1 && Number(parsed.port) <= 65535))
    } catch { return false }
  }
  return <Modal wide title={group ? '编辑代理组' : '添加代理组'} onClose={onClose}><form className="space-y-5" onSubmit={e => {
    e.preventDefault(); setValidation(null)
    const proxies = rows.map(p => p.url.trim())
    if (!proxies.length) { setValidation(new Error('请至少添加一个代理地址')); return }
    if (proxies.some(url => !valid(url))) { setValidation(new Error('代理地址需要是有效的 http://、https://、socks5:// 或 socks5h:// URL')); return }
    if (new Set(proxies).size !== proxies.length) { setValidation(new Error('代理地址不能重复')); return }
    save.mutate({ id: group?.id, name, proxies }, { onSuccess: onClose })
  }}><Field label="代理组名称"><Input required maxLength={64} value={name} onChange={e => setName(e.target.value)} /></Field>
    <div className="flex items-center justify-between"><h3 className="text-sm font-medium">代理地址</h3><Button type="button" variant="outline" size="sm" onClick={() => setRows(old => [...old, { rowID: crypto.randomUUID(), url: '' }])}><Plus />添加地址</Button></div>
    {rows.map((p, index) => { const state = group?.state.proxies?.[p.url.trim()]; return <div key={p.rowID} className="space-y-3 rounded-xl border p-4"><div className="flex items-center gap-2"><span className="flex-1 text-sm text-muted-foreground">优先级 {index + 1}（越靠前越优先）</span><Button type="button" variant="ghost" size="icon" aria-label="提高代理优先级" disabled={index === 0} onClick={() => move(index, -1)}><ArrowUp /></Button><Button type="button" variant="ghost" size="icon" aria-label="降低代理优先级" disabled={index === rows.length - 1} onClick={() => move(index, 1)}><ArrowDown /></Button><Button type="button" variant="ghost" size="icon" aria-label="移除代理地址" onClick={() => setRows(old => old.filter(r => r.rowID !== p.rowID))}><Trash2 /></Button></div><Input aria-label="代理 URL" required type="url" value={p.url} placeholder="socks5h://user:password@127.0.0.1:1080" onChange={e => { update(p.rowID, e.target.value); setValidation(null) }} /><div className="flex flex-wrap items-center gap-3"><HealthBadge health={state} />{state && <span className="text-xs text-muted-foreground">{healthTimes(state)}</span>}</div>{Object.entries(state?.applications || {}).map(([app, health]) => <p key={app} className="text-xs text-muted-foreground">{app}：{health.healthy ? '可用' : '不可用'} · {healthTimes(health)}</p>)}</div> })}
    <ErrorMessage error={validation || save.error} /><div className="flex justify-end gap-3 pt-5"><Button type="button" variant="outline" onClick={onClose}>取消</Button><Submit pending={save.isPending} /></div></form></Modal>
}
