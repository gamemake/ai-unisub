import { Pagination, PaginationContent, PaginationItem } from '@/components/ui/pagination'
import { TableRow, TableCell } from '@/components/ui/table'
import { useState } from 'react'
import { ChevronLeft, ChevronRight, Eye, Search } from 'lucide-react'
import { actions, useAction, useCalls } from '@/data/store'
import { Badge, Empty, ErrorMessage, Modal, PageHeader, QueryState, Table } from '@/components/shared'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { date, number } from '@/lib/utils'

function decodeBody(value: unknown) {
  if (typeof value !== 'string') return JSON.stringify(value, null, 2)
  try { return new TextDecoder().decode(Uint8Array.from(atob(value), c => c.charCodeAt(0))) } catch { return value }
}
export function Logs() {
  const [page, setPage] = useState(1), [search, setSearch] = useState(''), [q, setQ] = useState(''), [open, setOpen] = useState(false)
  const query = useCalls({ page: String(page), page_size: '20', q }), detail = useAction(actions.callDetail)
  const pages = Math.max(1, Math.ceil((query.data?.total || 0) / 20))
  return <><PageHeader title="调用记录" description="查看请求状态、Token 用量和完整调用详情。" /><Card><form className="flex max-w-lg gap-3 p-5" onSubmit={e => { e.preventDefault(); setPage(1); setQ(search.trim()) }}><Input aria-label="搜索调用记录" value={search} onChange={e => setSearch(e.target.value)} placeholder="精确搜索 IP、模型或 Request ID" /><Button variant="outline" type="submit"><Search />查询</Button></form><QueryState query={query}>{query.data?.items.length ? <Table headers={['时间', '平台 / 模型', '请求路径', '状态', 'Token 输入 / 输出', '详情']}>{query.data.items.map(c => <TableRow key={c.id}><TableCell className="whitespace-nowrap text-muted-foreground">{date(c.started_at)}</TableCell><TableCell><p>{c.provider_type}</p><p className="mt-1 text-xs text-muted-foreground">{c.model || '—'}</p></TableCell><TableCell className="max-w-64 truncate" title={c.url}>{c.url}</TableCell><TableCell><Badge enabled={c.http_error_code < 400}>{c.http_error_code >= 400 ? c.http_error_code : '成功'}</Badge></TableCell><TableCell className="tabular-nums">{number(c.input_tokens)} / {number(c.output_tokens)}</TableCell><TableCell><Button variant="ghost" size="icon" aria-label={`查看调用 ${c.request_id || c.id}`} onClick={() => { detail.reset(); setOpen(true); detail.mutate(c) }}><Eye /></Button></TableCell></TableRow>)}</Table> : <Empty>没有符合条件的调用记录</Empty>}</QueryState><div className="flex items-center justify-between border-t p-4 text-sm text-muted-foreground"><span>共 {number(query.data?.total)} 条</span><Pagination aria-label="调用记录分页" className="mx-0 w-auto"><PaginationContent className="gap-3"><PaginationItem><Button variant="outline" size="icon" aria-label="上一页" disabled={page <= 1} onClick={() => setPage(p => p - 1)}><ChevronLeft /></Button></PaginationItem><PaginationItem aria-current="page">{page} / {pages}</PaginationItem><PaginationItem><Button variant="outline" size="icon" aria-label="下一页" disabled={page >= pages} onClick={() => setPage(p => p + 1)}><ChevronRight /></Button></PaginationItem></PaginationContent></Pagination></div></Card>{open && <Modal wide title="调用详情" onClose={() => setOpen(false)}><ErrorMessage error={detail.error} />{detail.isPending && <p role="status">正在加载详情…</p>}{detail.data && <div className="space-y-5">{Object.entries(detail.data).map(([key, value]) => <section key={key}><h3 className="mb-2 text-sm font-medium text-muted-foreground">{key}</h3><pre className="max-h-80 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-background p-4 text-xs">{key.endsWith('_body') ? decodeBody(value) : typeof value === 'object' ? JSON.stringify(value, null, 2) : String(value ?? '—')}</pre></section>)}</div>}</Modal>}</>
}
