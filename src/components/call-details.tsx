import { useEffect } from 'react'
import { actions, useAction } from '@/data/store'
import type { Call } from '@/data/types'
import { ErrorMessage, Modal } from '@/components/shared'

function decodeBody(value: unknown) {
  if (typeof value !== 'string') return JSON.stringify(value, null, 2)
  try { return new TextDecoder().decode(Uint8Array.from(atob(value), c => c.charCodeAt(0))) } catch { return value }
}
export function CallDetails({ call, onClose }: { call: Call; onClose: () => void }) {
  const { mutate, ...detail } = useAction(actions.callDetail)
  useEffect(() => { mutate(call) }, [call, mutate])
  return <Modal wide title="调用详情" onClose={onClose}><ErrorMessage error={detail.error} />{detail.isPending && <p role="status">正在加载详情…</p>}{detail.data && <div className="space-y-5">{Object.entries(detail.data).map(([key, value]) => <section key={key}><h3 className="mb-2 text-sm font-medium text-muted-foreground">{key}</h3><pre className="max-h-80 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-background p-4 text-xs">{key.endsWith('_body') ? decodeBody(value) : typeof value === 'object' ? JSON.stringify(value, null, 2) : String(value ?? '—')}</pre></section>)}</div>}</Modal>
}
