import { Pagination, PaginationContent, PaginationItem } from '@/components/ui/pagination'
import { TableRow, TableCell } from '@/components/ui/table'
import { useState } from 'react'
import { ChevronLeft, ChevronRight, Search } from 'lucide-react'
import { actions, useAction, useCalls, useAIProviders } from '@/data/store'
import { Badge, Empty, ErrorMessage, Modal, PageHeader, QueryState, Table } from '@/components/shared'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { AppSelect } from '@/components/app-select'
import { SelectItem } from '@/components/ui/select'
import { TimeRangeFilter, type TimeFilter } from '@/components/time-range-filter'
import { Input } from '@/components/ui/input'
import type { Call } from '@/data/types'
import { date, number } from '@/lib/utils'

const codeOptions = [
  [0, '无 HTTP 错误'],
  [200, '成功'],
  [201, '已创建'],
  [202, '已接受'],
  [204, '无内容'],
  [301, '永久移动'],
  [302, '临时跳转'],
  [304, '未修改'],
  [307, '临时重定向'],
  [308, '永久重定向'],
  [400, '请求错误'],
  [401, '未认证'],
  [403, '禁止访问'],
  [404, '未找到'],
  [405, '方法不允许'],
  [408, '请求超时'],
  [409, '冲突'],
  [413, '请求体过大'],
  [415, '不支持的媒体类型'],
  [422, '无法处理请求内容'],
  [429, '请求过于频繁'],
  [499, '客户端关闭请求'],
  [500, '服务器内部错误'],
  [501, '未实现'],
  [502, '网关错误'],
  [503, '服务不可用'],
  [504, '网关超时'],
] as const

function decodeBody(value: unknown) {
  if (typeof value !== 'string') return JSON.stringify(value, null, 2)
  try { return new TextDecoder().decode(Uint8Array.from(atob(value), c => c.charCodeAt(0))) } catch { return value }
}
export function Logs() {
  const [page, setPage] = useState(1), [search, setSearch] = useState(''), [q, setQ] = useState(''), [open, setOpen] = useState(false)
  const [account, setAccount] = useState(''), [code, setCode] = useState(''), [time, setTime] = useState<TimeFilter>({ range: '1d' })
  const accounts = useAIProviders()
  const query = useCalls({ page: String(page), page_size: '20', q, account_id: account, code, ...time }), detail = useAction(actions.callDetail)
  function showDetail(call: Call) { detail.reset(); setOpen(true); detail.mutate(call) }
  const pages = Math.max(1, Math.ceil((query.data?.total || 0) / 20))
  return <><PageHeader title="调用记录" description="查看请求状态、Token 用量和完整调用详情。" />
    <div className="mb-6 space-y-2">
      <TimeRangeFilter onChange={value => { setTime(value); setPage(1) }} />
      <form className="flex flex-wrap items-center gap-3" onSubmit={event => { event.preventDefault(); setPage(1); setQ(search.trim()) }}>
        <Input aria-label="搜索调用记录" className="min-w-60 flex-1" value={search} onChange={event => setSearch(event.target.value)} placeholder="精确搜索 IP、模型、Session ID、Request ID" />
        <AppSelect aria-label="账号" className="w-48" value={account} onValueChange={value => { setAccount(value); setPage(1) }}>
          <SelectItem value="">全部账号</SelectItem>
          {accounts.data?.items.map(item => <SelectItem key={item.id} value={item.id}>{item.name} · {item.provider}</SelectItem>)}
        </AppSelect>
        <AppSelect aria-label="状态" className="w-24" contentClassName="min-w-56" valueLabel={code || '全部'} value={code} onValueChange={value => { setCode(value); setPage(1) }}>
          <SelectItem value="">全部</SelectItem>
          {codeOptions.map(([value, description]) => <SelectItem key={value} value={String(value)}>{value} · {description}</SelectItem>)}
        </AppSelect>
        <Button variant="outline" size="icon" type="submit" aria-label="查询"><Search /></Button>
      </form>
      <ErrorMessage error={accounts.error} />
    </div>
    <Card><QueryState query={query}>{query.data?.items.length ? <Table headers={['时间', '账号 / 模型', 'IP', 'Session ID / Request ID', '请求路径', '状态', 'Token 输入 / 输出']}>{query.data.items.map(c => <TableRow key={c.id} tabIndex={0} aria-label={`查看调用 ${c.request_id || c.id}`} aria-haspopup="dialog" className="cursor-pointer focus-visible:bg-muted/50 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring" onClick={() => showDetail(c)} onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); showDetail(c) } }}><TableCell className="whitespace-nowrap text-muted-foreground">{date(c.started_at)}</TableCell><TableCell><p>{accounts.data?.items.find(item => item.id === c.account_id)?.name || c.account_id || c.provider_type}</p><p className="mt-1 text-xs text-muted-foreground">{c.model || '—'}</p></TableCell><TableCell className="whitespace-nowrap text-muted-foreground">{c.source_ip || '—'}</TableCell><TableCell className="max-w-56 font-mono text-xs"><p className="truncate" title={c.session_id}>{c.session_id || '—'}</p><p className="mt-1 truncate text-muted-foreground" title={c.request_id}>{c.request_id || '—'}</p></TableCell><TableCell className="max-w-64 truncate" title={c.url}>{c.url}</TableCell><TableCell><Badge enabled={c.http_error_code < 400 && !c.http_error_info}>{c.http_error_code}</Badge></TableCell><TableCell className="tabular-nums">{number(c.input_tokens)} / {number(c.output_tokens)}</TableCell></TableRow>)}</Table> : <Empty>没有符合条件的调用记录</Empty>}</QueryState><div className="flex items-center justify-between border-t p-4 text-sm text-muted-foreground"><span>共 {number(query.data?.total)} 条</span><Pagination aria-label="调用记录分页" className="mx-0 w-auto"><PaginationContent className="gap-3"><PaginationItem><Button variant="outline" size="icon" aria-label="上一页" disabled={page <= 1} onClick={() => setPage(p => p - 1)}><ChevronLeft /></Button></PaginationItem><PaginationItem aria-current="page">{page} / {pages}</PaginationItem><PaginationItem><Button variant="outline" size="icon" aria-label="下一页" disabled={page >= pages} onClick={() => setPage(p => p + 1)}><ChevronRight /></Button></PaginationItem></PaginationContent></Pagination></div></Card>{open && <Modal wide title="调用详情" onClose={() => setOpen(false)}><ErrorMessage error={detail.error} />{detail.isPending && <p role="status">正在加载详情…</p>}{detail.data && <div className="space-y-5">{Object.entries(detail.data).map(([key, value]) => <section key={key}><h3 className="mb-2 text-sm font-medium text-muted-foreground">{key}</h3><pre className="max-h-80 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-background p-4 text-xs">{key.endsWith('_body') ? decodeBody(value) : typeof value === 'object' ? JSON.stringify(value, null, 2) : String(value ?? '—')}</pre></section>)}</div>}</Modal>}</>
}
