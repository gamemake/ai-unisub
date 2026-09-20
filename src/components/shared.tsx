import { Children, cloneElement, useId, type ReactElement, type ReactNode } from 'react'
import { AlertCircle, RefreshCw } from 'lucide-react'
import { Alert, AlertDescription } from './ui/alert'
import { Badge as UIBadge } from './ui/badge'
import { Empty as UIEmpty, EmptyDescription } from './ui/empty'
import { Field as UIField, FieldLabel, FieldDescription } from './ui/field'
import { Spinner } from './ui/spinner'
import { Table as UITable, TableHeader, TableBody, TableRow, TableHead } from './ui/table'
import { AlertDialog, AlertDialogContent, AlertDialogHeader, AlertDialogTitle, AlertDialogDescription, AlertDialogFooter, AlertDialogCancel, AlertDialogAction } from './ui/alert-dialog'
import { Button } from './ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from './ui/dialog'

export function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
  const id = useId()
  const input = Children.only(children) as ReactElement<Record<string, unknown>>
  return <UIField><FieldLabel htmlFor={id}>{label}</FieldLabel>{cloneElement(input, { id, 'aria-describedby': hint ? id + '-hint' : undefined })}{hint && <FieldDescription id={id + '-hint'}>{hint}</FieldDescription>}</UIField>
}
export function ErrorMessage({ error }: { error?: Error | null }) {
  return error ? <Alert variant="destructive"><AlertCircle /><AlertDescription>{error.message}</AlertDescription></Alert> : null
}
export function QueryState({ query, children }: { query: { isPending: boolean; error: Error | null; refetch: () => unknown }; children: ReactNode }) {
  if (query.isPending) return <div role="status" className="flex items-center justify-center gap-2 p-12 text-muted-foreground"><Spinner aria-hidden="true" className="size-5" />正在加载…</div>
  if (query.error) return <div className="space-y-3 p-5"><ErrorMessage error={query.error} /><Button variant="outline" onClick={() => query.refetch()}><RefreshCw />重试</Button></div>
  return children
}
export function PageHeader({ title, description, action }: { title: string; description: string; action?: ReactNode }) {
  return <div className="mb-7 flex flex-wrap items-center justify-between gap-4"><div><h1 className="text-2xl font-semibold tracking-tight">{title}</h1><p className="mt-2 text-sm text-muted-foreground">{description}</p></div>{action}</div>
}
export function Modal({ title, description, children, onClose, wide, scrollBody }: { title: string; description?: string; children: ReactNode; onClose: () => void; wide?: boolean; scrollBody?: boolean }) {
  const descriptionId = useId()
  return <Dialog open onOpenChange={open => { if (!open) onClose() }}><DialogContent className={`max-h-[calc(100dvh-2rem)] min-w-0 gap-6 p-6 ${scrollBody ? 'flex flex-col overflow-hidden' : 'overflow-x-hidden overflow-y-auto'} ${wide ? 'sm:max-w-4xl' : 'sm:max-w-xl'}`} aria-describedby={description ? descriptionId : undefined}>
    <DialogHeader className="shrink-0 pr-8">
      <DialogTitle className="text-lg font-semibold leading-snug">{title}</DialogTitle>
      {description && <DialogDescription id={descriptionId} className="leading-relaxed">{description}</DialogDescription>}
    </DialogHeader>
    <div className={`min-w-0 ${scrollBody ? 'min-h-0 flex-1 overflow-x-hidden overflow-y-auto' : ''}`}>{children}</div>
  </DialogContent></Dialog>
}
export function Empty({ children = '暂无数据' }: { children?: ReactNode }) { return <UIEmpty><EmptyDescription>{children}</EmptyDescription></UIEmpty> }
export function Badge({ enabled, children }: { enabled?: boolean; children?: ReactNode }) {
  // Nullish only: HTTP status 0 must render as "0", not fall back to 已启用.
  const label = children != null ? children : (enabled ? '已启用' : '已停用')
  return <UIBadge variant="secondary" className={`h-auto gap-1.5 px-2.5 py-1 ${enabled ? 'bg-emerald-400/10 text-emerald-300' : 'bg-slate-400/10 text-slate-400'}`}><span className={`size-1.5 shrink-0 rounded-full ${enabled ? 'bg-emerald-400' : 'bg-slate-400'}`} />{label}</UIBadge>
}
export function Submit({ pending, children = '保存' }: { pending: boolean; children?: ReactNode }) { return <Button type="submit" disabled={pending}>{pending && <Spinner aria-hidden="true" />}{pending ? '处理中…' : children}</Button> }
export function Table({ headers, children, className }: { headers: string[]; children: ReactNode; className?: string }) { return <UITable className={className}><TableHeader className="bg-background/40"><TableRow>{headers.map(h => <TableHead key={h}>{h}</TableHead>)}</TableRow></TableHeader><TableBody>{children}</TableBody></UITable> }
export function Confirm({ title, pending, error, onConfirm, onClose }: { title: string; pending: boolean; error: Error | null; onConfirm: () => void; onClose: () => void }) { return <AlertDialog open onOpenChange={open => { if (!open && !pending) onClose() }}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{title}</AlertDialogTitle><AlertDialogDescription>此操作无法撤销，请确认后继续。</AlertDialogDescription></AlertDialogHeader><ErrorMessage error={error} /><AlertDialogFooter><AlertDialogCancel disabled={pending}>取消</AlertDialogCancel><AlertDialogAction variant="destructive" disabled={pending} onClick={onConfirm}>{pending ? '处理中…' : '确认删除'}</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog> }
