import { DetailTableRow } from '@/components/detail-table-row'
import { CallDetails } from '@/components/call-details'
import { KeyDetails } from '@/pages/keys'
import { AccountForm } from '@/pages/accounts'
import { TableRow, TableCell } from '@/components/ui/table'
import { AppSelect } from '@/components/app-select'
import { SelectItem } from '@/components/ui/select'
import { useState } from 'react'
import { Activity, ArrowDownLeft, ArrowUpRight, KeyRound, Layers3 } from 'lucide-react'
import { useAccountOptions, useAccounts, useCalls, useKeys, useUsage } from '@/data/store'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge, Empty, PageHeader, QueryState, Table } from '@/components/shared'
import { TimeRangeFilter, type TimeFilter } from '@/components/time-range-filter'
import { date, number } from '@/lib/utils'
import type { User, APIKey, Call, Account } from '@/data/types'

function Stat({ label, value, icon: Icon, detail }: { label: string; value: string; icon: typeof Activity; detail: string }) { return <Card><CardContent className="pt-5"><div className="flex items-center justify-between text-sm text-muted-foreground">{label}<Icon className="size-4 text-primary" /></div><div className="my-4 text-3xl font-semibold tabular-nums">{value}</div><p className="text-xs text-muted-foreground">{detail}</p></CardContent></Card> }
export function Personal({ me }: { me: User }) {
  const [selectedKey, setSelectedKey] = useState<APIKey | null>(null), [selectedCall, setSelectedCall] = useState<Call | null>(null)
  const keys = useKeys(), accounts = useAccountOptions(), calls = useCalls({ page_size: '10', mine: '1' })
  return <><PageHeader title="个人总览" description={`你好，${me.name}。这里是你的 AI 工作空间。`} action={<Badge enabled>已登录</Badge>} />
    <div className="mb-6 grid gap-4 sm:grid-cols-3"><Stat label="我的 API Key" value={keys.data ? number(keys.data.total) : '—'} icon={KeyRound} detail="当前账号签发的调用密钥" /><Stat label="可选账号" value={accounts.data ? number(accounts.data.items.filter(a => a.enabled).length) : '—'} icon={Layers3} detail="处于启用状态的订阅、API 或组" /><Stat label="我的累计请求" value={calls.data ? number(calls.data.total) : '—'} icon={Activity} detail="已记录的历史调用" /></div>
    <div className="grid gap-6 xl:grid-cols-[1fr_1.6fr]"><Card><CardHeader><CardTitle>我的密钥</CardTitle></CardHeader><QueryState query={keys}><CardContent>{keys.data?.items.length ? keys.data.items.slice(0, 6).map(k => <button type="button" key={k.id} aria-label={`查看 ${k.name} 详情`} onClick={() => setSelectedKey(k)} className="flex w-full cursor-pointer items-center justify-between border-b py-4 text-left last:border-0 hover:bg-muted/50 focus-visible:outline-ring"><div><p className="text-sm font-medium">{k.name}</p><p className="mt-1 text-xs text-muted-foreground">{accounts.data?.items.find(a => a.id === k.account_id)?.name || k.account_id}</p></div><Badge enabled={!k.expires_at || Date.parse(k.expires_at) > Date.now()}>{k.expires_at ? '限时密钥' : '长期有效'}</Badge></button>) : <Empty>还没有密钥，前往 API Key 页面签发。</Empty>}</CardContent></QueryState></Card>
    <Card><CardHeader><CardTitle>最近调用</CardTitle></CardHeader><QueryState query={calls}>{calls.data?.items.length ? <Table headers={['模型 / 路径', '状态', '时间']}>{calls.data.items.map(c => <DetailTableRow key={c.id} aria-label={`查看调用 ${c.request_id || c.id}`} onOpen={() => setSelectedCall(c)}><TableCell className="max-w-52 truncate">{c.model || c.url}</TableCell><TableCell><Badge enabled={c.http_error_code > 0 && c.http_error_code < 400}>{c.http_error_code === 0 ? '网络错误' : c.http_error_code >= 400 ? c.http_error_code : '成功'}</Badge></TableCell><TableCell className="whitespace-nowrap text-muted-foreground">{date(c.finished_at)}</TableCell></DetailTableRow>)}</Table> : <Empty>发起第一次 API 调用后，记录会显示在这里。</Empty>}</QueryState></Card></div>
    {selectedKey && <KeyDetails value={selectedKey} account={accounts.data?.items.find(a => a.id === selectedKey.account_id)} onClose={() => setSelectedKey(null)} />}
    {selectedCall && <CallDetails call={selectedCall} onClose={() => setSelectedCall(null)} />}
  </>
}
export function Overview() {
  const [selectedProvider, setSelectedProvider] = useState<Account | null>(null)
  const [params, setParams] = useState<TimeFilter>({ range: '1d' }), [subscription, setSubscription] = useState('')
  const subs = useUsage('subscriptions', params), users = useUsage('users', { ...params, ...(subscription ? { subscription_id: subscription } : {}) }), accounts = useAccounts()
  const totals = subs.data?.totals
  return <><PageHeader title="系统总览" description="查看所有账号与用户在选定时间范围内的调用用量。" />
    <div className="mb-6"><TimeRangeFilter onChange={setParams} /></div>
    <div className="mb-6 grid gap-4 sm:grid-cols-3"><Stat label="请求数" value={totals ? number(totals.requests) : '—'} detail="所选范围 · 全部账号" icon={Activity} /><Stat label="输入 Token" value={totals ? number(totals.input_tokens) : '—'} detail="上游返回的用量" icon={ArrowDownLeft} /><Stat label="输出 Token" value={totals ? number(totals.output_tokens) : '—'} detail="上游返回的用量" icon={ArrowUpRight} /></div>
    <div className="space-y-6"><Card><CardHeader><CardTitle>账号用量</CardTitle></CardHeader><QueryState query={subs}>{subs.data?.data.length ? <Table headers={['账号', '平台', '请求', '输入 Token', '输出 Token']}>{subs.data.data.map((r, i) => { const provider = accounts.data?.items.find(a => a.id === r.subscription_id); return <DetailTableRow key={r.subscription_id || i} onOpen={provider ? () => setSelectedProvider(provider) : undefined}><TableCell>{r.subscription_name || r.subscription_id}</TableCell><TableCell>{r.provider}</TableCell><TableCell>{number(r.usage.requests)}</TableCell><TableCell>{number(r.usage.input_tokens)}</TableCell><TableCell>{number(r.usage.output_tokens)}</TableCell></DetailTableRow> })}</Table> : <Empty>所选范围内没有调用记录</Empty>}</QueryState></Card>
    <Card><CardHeader><div className="flex flex-wrap items-center justify-between gap-3"><CardTitle>用户用量</CardTitle><AppSelect aria-label="按账号筛选用户用量" className="max-w-60" value={subscription} onValueChange={value => setSubscription(value)}><SelectItem value="">所有账号</SelectItem>{accounts.data?.items.map(a => <SelectItem key={a.id} value={String(a.id)}>{a.name}</SelectItem>)}</AppSelect></div></CardHeader><QueryState query={users}>{users.data?.data.length ? <Table headers={['用户', '角色', '请求', '输入 Token', '输出 Token']}>{users.data.data.map((r, i) => <TableRow key={r.user_id || i}><TableCell>{r.username || '未归属'}</TableCell><TableCell>{r.role || '—'}</TableCell><TableCell>{number(r.usage.requests)}</TableCell><TableCell>{number(r.usage.input_tokens)}</TableCell><TableCell>{number(r.usage.output_tokens)}</TableCell></TableRow>)}</Table> : <Empty>所选范围内没有调用记录</Empty>}</QueryState></Card></div>{selectedProvider && <AccountForm account={selectedProvider} onClose={() => setSelectedProvider(null)} />}
  </>
}
