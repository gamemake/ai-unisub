import { useEffect, useState, type MouseEvent, type ReactNode } from 'react'
import { Check, ChevronRight, Copy } from 'lucide-react'
import { actions, useAction } from '@/data/store'
import type { Call, CallDetail, CallHeaders } from '@/data/types'
import { Badge, ErrorMessage, Modal } from '@/components/shared'
import { Button } from '@/components/ui/button'
import { cn, date, number } from '@/lib/utils'
import { Table, TableBody, TableCell, TableRow } from '@/components/ui/table'

/** Decode Go `[]byte` JSON (standard base64) and pretty-print JSON when possible. */
export function decodeBody(value: unknown): string {
  if (value == null || value === '') return ''
  if (typeof value !== 'string') {
    if (Array.isArray(value) && value.every(item => typeof item === 'number')) {
      return decodeBody(new TextDecoder().decode(Uint8Array.from(value)))
    }
    return prettyJSON(value)
  }
  let text = value
  const compact = value.replace(/\s/g, '')
  // encoding/json always emits standard base64 for []byte; try decode without a full-string regex
  // (large bodies can make charset regexes fail or hang).
  if (compact.length >= 4 && compact.length % 4 === 0) {
    try {
      const decoded = base64ToUtf8(compact)
      if (decoded && !decoded.includes('\uFFFD')) text = decoded
    } catch {
      // Keep the original string when base64 decoding fails.
    }
  }
  return prettyJSON(text)
}

function base64ToUtf8(value: string): string {
  const binary = atob(value)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return new TextDecoder().decode(bytes)
}

function prettyJSON(value: unknown): string {
  if (typeof value === 'string') {
    try {
      return JSON.stringify(JSON.parse(value), null, 2)
    } catch {
      return value
    }
  }
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

/** Normalize Go http.Header JSON (or a plain string map) into name → display value. */
export function headerEntries(headers?: CallHeaders | null): [string, string][] {
  if (!headers || typeof headers !== 'object') return []
  return Object.entries(headers)
    .map(([name, value]) => [name, formatHeaderValue(value)] as [string, string])
    .sort(([a], [b]) => a.localeCompare(b, undefined, { sensitivity: 'base' }))
}

function formatHeaderValue(value: string[] | string | undefined): string {
  if (value == null) return ''
  if (Array.isArray(value)) return value.filter(Boolean).join(', ')
  return String(value)
}

export type HeaderChange = 'same' | 'modified' | 'added' | 'removed'
export type HeaderDiff = { name: string; original: string; outbound: string; change: HeaderChange }

/** Merge original/outbound request headers and classify each name's change. */
export function compareRequestHeaders(original?: CallHeaders | null, outbound?: CallHeaders | null): HeaderDiff[] {
  const left = new Map(headerEntries(original))
  const right = new Map(headerEntries(outbound))
  const names = new Set([...left.keys(), ...right.keys()])
  return [...names]
    .sort((a, b) => a.localeCompare(b, undefined, { sensitivity: 'base' }))
    .map(name => {
      const o = left.get(name) ?? ''
      const u = right.get(name) ?? ''
      let change: HeaderChange = 'same'
      if (!o && u) change = 'added'
      else if (o && !u) change = 'removed'
      else if (o !== u) change = 'modified'
      return { name, original: o, outbound: u, change }
    })
}

function durationMs(started?: string, finished?: string): number | null {
  if (!started || !finished) return null
  const ms = Date.parse(finished) - Date.parse(started)
  return Number.isFinite(ms) && ms >= 0 ? ms : null
}

function formatDuration(ms: number | null): string {
  if (ms == null) return '—'
  if (ms < 1000) return `${ms} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 2 : 1)} s`
  const m = Math.floor(ms / 60_000)
  const s = ((ms % 60_000) / 1000).toFixed(1)
  return `${m} m ${s} s`
}

function tokenLine(detail: CallDetail): string {
  const parts = [`${number(detail.input_tokens)} → ${number(detail.output_tokens)}`]
  if (detail.cache_creation_tokens) parts.push(`创建缓存 ${number(detail.cache_creation_tokens)}`)
  if (detail.cache_read_tokens) parts.push(`读取缓存 ${number(detail.cache_read_tokens)}`)
  return parts.join(' · ')
}

function InfoItem({ label, children, mono, className = '' }: { label: string; children: ReactNode; mono?: boolean; className?: string }) {
  return (
    <div className={`min-w-0 ${className}`}>
      <dt className="text-[11px] font-medium tracking-wide text-muted-foreground uppercase">{label}</dt>
      <dd className={`mt-0.5 break-all text-sm leading-snug ${mono ? 'font-mono text-xs' : ''}`}>{children || '—'}</dd>
    </div>
  )
}

/** Format header rows as `Name: value` lines for clipboard. */
export function formatHeadersCopy(rows: [string, string][]): string {
  return rows.map(([name, value]) => `${name}: ${value}`).join('\n')
}

/** Format request-header diffs, preserving change info when values differ. */
export function formatRequestHeadersCopy(rows: HeaderDiff[]): string {
  return rows.map(row => {
    if (row.change === 'same') return `${row.name}: ${row.outbound}`
    if (row.change === 'added') return `${row.name}: ${row.outbound}`
    if (row.change === 'removed') return `${row.name}: ${row.original}`
    return `${row.name}:\n  original: ${row.original}\n  outbound: ${row.outbound}`
  }).join('\n')
}

function CopyButton({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false)
  async function copy(event: MouseEvent) {
    event.preventDefault()
    event.stopPropagation()
    if (!text) return
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      setCopied(false)
    }
  }
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon-xs"
      className="text-muted-foreground"
      aria-label={copied ? `已复制${label}` : `复制${label}到剪贴板`}
      disabled={!text}
      onClick={copy}
    >
      {copied ? <Check className="size-3.5 text-emerald-300" /> : <Copy className="size-3.5" />}
    </Button>
  )
}

function CollapsibleSection({
  title,
  defaultOpen = false,
  meta,
  copyText,
  copyLabel,
  children,
}: {
  title: string
  defaultOpen?: boolean
  meta?: ReactNode
  copyText?: string
  copyLabel?: string
  children: ReactNode
}) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <section className="min-w-0 overflow-hidden rounded-lg border">
      <div className="flex items-center gap-1 pr-1.5">
        <button
          type="button"
          className="flex min-w-0 flex-1 items-center gap-2 px-3 py-2.5 text-left transition-colors hover:bg-muted/40"
          aria-expanded={open}
          onClick={() => setOpen(value => !value)}
        >
          <ChevronRight className={cn('size-4 shrink-0 text-muted-foreground transition-transform', open && 'rotate-90')} aria-hidden />
          <span className="min-w-0 flex-1 text-sm font-medium">{title}</span>
          {meta}
        </button>
        {copyText != null ? <CopyButton text={copyText} label={copyLabel || title} /> : null}
      </div>
      {open ? <div className="border-t px-3 py-3">{children}</div> : null}
    </section>
  )
}

function EmptyBlock({ children = '无内容' }: { children?: ReactNode }) {
  return <p className="rounded-lg border border-dashed px-3 py-4 text-center text-sm text-muted-foreground">{children}</p>
}

function BodyBlock({ value }: { value: unknown }) {
  const text = decodeBody(value)
  if (!text) return <EmptyBlock>空 body</EmptyBlock>
  return <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-background/60 p-3 font-mono text-xs leading-relaxed ring-1 ring-border/60">{text}</pre>
}

function HeaderTable({ rows }: { rows: [string, string][] }) {
  if (!rows.length) return <EmptyBlock>无 headers</EmptyBlock>
  return (
    <div className="overflow-hidden rounded-lg ring-1 ring-border/60">
      <Table>
        <TableBody>
          {rows.map(([name, value]) => (
            <TableRow key={name} className="hover:bg-transparent">
              <TableCell className="w-[28%] max-w-48 whitespace-normal px-3 py-2 align-top font-mono text-xs font-medium text-muted-foreground">{name}</TableCell>
              <TableCell className="whitespace-normal break-all px-3 py-2 font-mono text-xs leading-relaxed">{value || '—'}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

const changeMeta: Record<HeaderChange, { label: string; className: string }> = {
  same: { label: '', className: '' },
  modified: { label: '已修改', className: 'bg-amber-400/15 text-amber-200 ring-amber-400/30' },
  added: { label: '新增', className: 'bg-emerald-400/15 text-emerald-300 ring-emerald-400/30' },
  removed: { label: '已移除', className: 'bg-rose-400/15 text-rose-300 ring-rose-400/30' },
}

function ChangeBadge({ change }: { change: HeaderChange }) {
  const meta = changeMeta[change]
  if (!meta.label) return null
  return <span className={cn('inline-flex shrink-0 rounded-md px-1.5 py-0.5 text-[10px] font-medium ring-1 ring-inset', meta.className)}>{meta.label}</span>
}

function RequestHeaderCompare({ original, outbound }: { original?: CallHeaders | null; outbound?: CallHeaders | null }) {
  const rows = compareRequestHeaders(original, outbound)
  if (!rows.length) return <EmptyBlock>无 headers</EmptyBlock>
  const changed = rows.filter(row => row.change !== 'same').length
  return (
    <div className="space-y-2">
      {changed > 0 ? (
        <p className="text-xs text-muted-foreground">{changed} 个 header 与原始请求不同</p>
      ) : (
        <p className="text-xs text-muted-foreground">出站请求 header 与原始请求一致</p>
      )}
      <div className="overflow-hidden rounded-lg ring-1 ring-border/60">
        <Table>
          <TableBody>
            {rows.map(row => (
              <TableRow
                key={row.name}
                className={cn(
                  'hover:bg-transparent',
                  row.change === 'modified' && 'bg-amber-400/5',
                  row.change === 'added' && 'bg-emerald-400/5',
                  row.change === 'removed' && 'bg-rose-400/5',
                )}
              >
                <TableCell className="w-[26%] max-w-52 whitespace-normal px-3 py-2 align-top">
                  <div className="flex flex-wrap items-center gap-1.5">
                    <span className="font-mono text-xs font-medium text-muted-foreground">{row.name}</span>
                    <ChangeBadge change={row.change} />
                  </div>
                </TableCell>
                <TableCell className="whitespace-normal break-all px-3 py-2 align-top font-mono text-xs leading-relaxed">
                  {row.change === 'same' ? (
                    <span>{row.outbound || '—'}</span>
                  ) : row.change === 'added' ? (
                    <span className="text-emerald-100/90">{row.outbound || '—'}</span>
                  ) : row.change === 'removed' ? (
                    <span className="text-rose-100/80 line-through opacity-80">{row.original || '—'}</span>
                  ) : (
                    <div className="space-y-1">
                      <div className="flex gap-2">
                        <span className="w-8 shrink-0 text-[10px] font-medium tracking-wide text-muted-foreground">原始</span>
                        <span className="min-w-0 text-muted-foreground line-through decoration-muted-foreground/50">{row.original || '—'}</span>
                      </div>
                      <div className="flex gap-2">
                        <span className="w-8 shrink-0 text-[10px] font-medium tracking-wide text-amber-200/80">出站</span>
                        <span className="min-w-0 text-amber-50">{row.outbound || '—'}</span>
                      </div>
                    </div>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}

function CallDetailContent({ detail }: { detail: CallDetail }) {
  // 0 means network/transport failure with no HTTP response; 2xx/3xx are success-ish.
  const ok = detail.http_error_code > 0 && detail.http_error_code < 400 && !detail.http_error_info
  const ms = durationMs(detail.started_at, detail.finished_at)
  const requestHeaders = compareRequestHeaders(detail.original_request_headers, detail.outbound_request_headers)
  const requestHeaderChanges = requestHeaders.filter(row => row.change !== 'same').length
  const responseHeaders = headerEntries(detail.response_headers)
  const requestBody = decodeBody(detail.request_body)
  const responseBody = decodeBody(detail.response_body)
  return (
    <div className="space-y-4">
      <section className="min-w-0 space-y-3 rounded-xl border bg-background/40 p-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 space-y-1.5">
            <div className="flex flex-wrap items-center gap-2">
              <Badge enabled={ok}>{detail.http_error_code ?? 0}</Badge>
              <span className="text-sm font-medium">{detail.model || '—'}</span>
              <span className="text-xs text-muted-foreground">{detail.provider_type || '—'}</span>
            </div>
            <p className="break-all font-mono text-xs leading-relaxed text-muted-foreground" title={detail.url}>{detail.url || '—'}</p>
          </div>
          <div className="shrink-0 text-right">
            <p className="text-[11px] font-medium tracking-wide text-muted-foreground uppercase">耗时</p>
            <p className="mt-0.5 tabular-nums text-sm font-medium">{formatDuration(ms)}</p>
          </div>
        </div>

        {detail.http_error_info ? (
          <p className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{detail.http_error_info}</p>
        ) : null}

        <dl className="grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-3 lg:grid-cols-4">
          <InfoItem label="Request ID" mono>{detail.request_id}</InfoItem>
          <InfoItem label="Session ID" mono>{detail.session_id}</InfoItem>
          <InfoItem label="Source IP" mono>{detail.source_ip}</InfoItem>
          <InfoItem label="Account">{detail.account_id || '—'}</InfoItem>
          <InfoItem label="Tokens" className="col-span-2 sm:col-span-2 lg:col-span-2">{tokenLine(detail)}</InfoItem>
          <InfoItem label="开始">{date(detail.started_at)}</InfoItem>
          <InfoItem label="结束">{date(detail.finished_at)}</InfoItem>
        </dl>
      </section>

      <CollapsibleSection
        title="Request Headers"
        meta={
          <span className="text-xs text-muted-foreground">
            {requestHeaderChanges > 0 ? `${requestHeaderChanges} 处变更` : '无变更'}
          </span>
        }
        copyText={formatRequestHeadersCopy(requestHeaders)}
        copyLabel="Request Headers"
      >
        <RequestHeaderCompare original={detail.original_request_headers} outbound={detail.outbound_request_headers} />
      </CollapsibleSection>

      <CollapsibleSection
        title="Request Body"
        meta={requestBody ? <span className="text-xs text-muted-foreground tabular-nums">{number(requestBody.length)} 字符</span> : null}
        copyText={requestBody}
        copyLabel="Request Body"
      >
        <BodyBlock value={detail.request_body} />
      </CollapsibleSection>

      <CollapsibleSection
        title="Response Headers"
        meta={responseHeaders.length ? <span className="text-xs text-muted-foreground tabular-nums">{responseHeaders.length} 项</span> : null}
        copyText={formatHeadersCopy(responseHeaders)}
        copyLabel="Response Headers"
      >
        <HeaderTable rows={responseHeaders} />
      </CollapsibleSection>

      <CollapsibleSection
        title="Response Body"
        meta={responseBody ? <span className="text-xs text-muted-foreground tabular-nums">{number(responseBody.length)} 字符</span> : null}
        copyText={responseBody}
        copyLabel="Response Body"
      >
        <BodyBlock value={detail.response_body} />
      </CollapsibleSection>
    </div>
  )
}

export function CallDetails({ call, onClose }: { call: Call; onClose: () => void }) {
  const { mutate, ...detail } = useAction(actions.callDetail)
  useEffect(() => { mutate(call) }, [call, mutate])
  return (
    <Modal wide title="调用详情" onClose={onClose}>
      <ErrorMessage error={detail.error} />
      {detail.isPending && <p role="status">正在加载详情…</p>}
      {detail.data && <CallDetailContent detail={detail.data} />}
    </Modal>
  )
}
