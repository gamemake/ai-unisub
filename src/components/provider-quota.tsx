import { RefreshCw } from 'lucide-react'
import { useFetchQuota } from '@/data/store'
import type { Account, Quota } from '@/data/types'
import { ErrorMessage } from '@/components/shared'
import { Button } from '@/components/ui/button'

const statusLabels: Partial<Record<Quota['cache_status'], string>> = { missing: '未知', stale: '部分或全部已过期' }
const windowLabels: Record<string, string> = { weekly: '周', weekly_sonnet: '周（Sonnet）', weekly_opus: '周（Opus）', monthly: '月' }

function resetCountdown(resetAt: string) {
  const remaining = new Date(resetAt).getTime() - Date.now()
  if (!Number.isFinite(remaining)) return ''
  if (remaining <= 0) return '即将重置'
  const seconds = remaining / 1000
  if (seconds >= 86_400) return `${(seconds / 86_400).toFixed(2)}d 重置`
  if (seconds >= 3_600) return `${(seconds / 3_600).toFixed(2)}h 重置`
  if (seconds >= 60) return `${(seconds / 60).toFixed(2)}m 重置`
  return `${seconds.toFixed(2)}s 重置`
}

function QuotaDetails({ data }: { data: Quota }) {
  const statusLabel = statusLabels[data.cache_status]
  return <div className="space-y-1">
    <div className="flex flex-wrap gap-3 text-xs text-muted-foreground">
      {statusLabel && <span>{statusLabel}</span>}
    </div>
    {!!data.subscription?.length && <dl>{data.subscription.map((window, index) => { const countdown = resetCountdown(window.reset_at); return <div key={index} className="flex flex-wrap items-baseline gap-x-3 py-0.5 text-xs"><dt className="break-words text-muted-foreground">{windowLabels[window.time_dimension] || window.time_dimension}</dt><dd>{window.usage.toLocaleString(undefined, { maximumFractionDigits: 2 })}%</dd><dd className="whitespace-nowrap text-muted-foreground">{countdown}</dd></div> })}</dl>}
    {!!data.items?.length && <dl>{data.items.map((item, index) => <div key={index} className="flex flex-wrap items-baseline gap-x-3 py-0.5 text-xs"><dt className="break-all text-muted-foreground" title={item.source}>{item.name === 'blance' || item.name === 'balance' ? '余额' : item.name}</dt><dd className="whitespace-pre-wrap break-all font-mono">{item.value === 'not available' ? '不可用' : item.value}</dd></div>)}</dl>}
    {!data.subscription?.length && !data.items?.length && data.cache_status !== 'missing' && <p className="text-sm text-muted-foreground">暂无可展示的数据。</p>}
  </div>
}

export function ProviderQuota({ account }: { account: Account }) {
  const fetch = useFetchQuota()
  const data = account.quota
  const busy = fetch.isPending
  return <div className="flex w-50 max-w-[75vw] items-center gap-2 whitespace-normal" aria-label={`${account.name} 额度`}>
    <div className="max-h-64 min-w-0 flex-1 space-y-1 overflow-y-auto" aria-busy={busy}>
      <ErrorMessage error={fetch.error} />
      {fetch.error && data && <p className="text-sm text-muted-foreground">刷新失败，以下保留上次结果。</p>}
      {data && <QuotaDetails data={data} />}
    </div>
    <Button type="button" variant="ghost" size="icon" className="shrink-0" aria-label={`刷新 ${account.name} 额度`} title="刷新额度" disabled={busy} onClick={() => fetch.mutate(account.id)}><RefreshCw className={busy ? 'animate-spin' : ''} /></Button>
  </div>
}
