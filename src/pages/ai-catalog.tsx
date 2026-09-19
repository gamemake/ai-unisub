import { DetailTableRow } from '@/components/detail-table-row'
import { useEffect, useMemo, useState } from 'react'
import { actions, useAction, useAICatalog } from '@/data/store'
import type { ClientType, Supplier } from '@/data/types'
import { ErrorMessage, Field, PageHeader, QueryState, Table } from '@/components/shared'
import { TableCell } from '@/components/ui/table'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { RotateCcw } from 'lucide-react'

function supplierFromHash() { return location.hash.startsWith('#ai-catalog/') ? location.hash.slice('#ai-catalog/'.length) : '' }

function sameModels(a: string[] = [], b: string[] = []) {
  return a.length === b.length && a.every((value, i) => value === b[i])
}

function parseModels(text: string) {
  return text.split(/\r?\n/).map(line => line.trim()).filter(Boolean)
}

function clientLabel(clients: ClientType[] = []) {
  return clients.length ? clients.join(' · ') : '—'
}

export function AICatalogPage() {
  const query = useAICatalog()
  const [supplierID, setSupplierID] = useState(supplierFromHash)
  useEffect(() => { const update = () => { setSupplierID(supplierFromHash()); window.scrollTo(0, 0) }; addEventListener('hashchange', update); return () => removeEventListener('hashchange', update) }, [])
  const supplier = query.data?.catalog.suppliers.find(s => s.id === supplierID)
  const builtin = query.data?.builtin_suppliers.find(s => s.id === supplierID)
  return <div><PageHeader title="模型供应商" description="查看内置服务地址，编辑显示名与支持模型列表；URL 与支持客户端由代码固定。" />
    <QueryState query={query}>{query.data && <>
      <Card><Table headers={['供应商', '支持客户端', '默认 URL', '模型']}>{query.data.catalog.suppliers.map(s =>
        <DetailTableRow key={s.id} aria-label={`查看 ${s.name} 详情`} onOpen={() => { location.hash = `ai-catalog/${s.id}` }}>
          <TableCell className="font-medium text-primary">{s.name}</TableCell>
          <TableCell className="text-sm">{clientLabel(s.supported_clients)}</TableCell>
          <TableCell className="max-w-96 break-all font-mono text-xs">
            <div>Claude：{s.claude_url || '未提供'}</div>
            <div className="mt-1">OpenAI：{s.openai_url || '未提供'}</div>
          </TableCell>
          <TableCell className="max-w-72 text-xs text-muted-foreground">{s.models?.length ? `${s.models.slice(0, 2).join('、')}${s.models.length > 2 ? ` 等 ${s.models.length} 个` : ''}` : '—'}</TableCell>
        </DetailTableRow>
      )}</Table></Card>
      <Dialog open={!!supplierID} onOpenChange={open => { if (!open) location.hash = 'ai-catalog' }}>
        <DialogContent className="gap-6 p-6 sm:max-w-2xl">
          <DialogHeader className="pr-8">
            <DialogTitle className="text-lg font-semibold leading-snug">{supplier ? `${supplier.name} 详情` : '模型供应商详情'}</DialogTitle>
            <DialogDescription className="leading-relaxed">服务地址与支持客户端只读。显示名和模型列表可改，各字段可恢复为代码缺省。</DialogDescription>
          </DialogHeader>
          {supplier && builtin ? (
            <SupplierEditor key={supplier.id} supplier={supplier} builtin={builtin} onClose={() => { location.hash = 'ai-catalog' }} />
          ) : <p role="alert">未找到该供应商，请关闭窗口后重新选择。</p>}
        </DialogContent>
      </Dialog>
    </>}</QueryState>
  </div>
}

function FieldReset({ label, disabled, onReset }: { label: string; disabled: boolean; onReset: () => void }) {
  return (
    <Button type="button" variant="ghost" size="sm" className="h-7 gap-1 px-2 text-xs text-muted-foreground" disabled={disabled} onClick={onReset} aria-label={`恢复${label}为默认值`}>
      <RotateCcw className="size-3.5" />恢复默认
    </Button>
  )
}

function SupplierEditor({ supplier, builtin, onClose }: { supplier: Supplier; builtin: Supplier; onClose: () => void }) {
  const [name, setName] = useState(supplier.name)
  const [modelsText, setModelsText] = useState((supplier.models || []).join('\n'))
  const save = useAction(actions.saveSupplier, ['ai-catalog'])
  const models = useMemo(() => parseModels(modelsText), [modelsText])
  const nameDirty = name !== builtin.name
  const modelsDirty = !sameModels(models, builtin.models || [])
  const dirty = name !== supplier.name || !sameModels(models, supplier.models || [])

  function submit(e: React.FormEvent) {
    e.preventDefault()
    save.mutate({ id: supplier.id, name: name.trim(), models }, { onSuccess: onClose })
  }

  return (
    <form aria-label="供应商配置" className="space-y-4" onSubmit={submit}>
      <div className="flex flex-wrap gap-2">
        {(supplier.supported_clients || []).map(client => <Badge key={client} variant="secondary">{client}</Badge>)}
        {!supplier.supported_clients?.length && <span className="text-sm text-muted-foreground">无支持客户端</span>}
      </div>
      <Field label={`${supplier.name} Claude URL`}><Input readOnly value={supplier.claude_url} placeholder="未提供该协议的内置地址" /></Field>
      <Field label={`${supplier.name} OpenAI URL`}><Input readOnly value={supplier.openai_url} placeholder="未提供该协议的内置地址" /></Field>
      <div className="space-y-2">
        <div className="flex items-center justify-between gap-2">
          <span className="text-sm font-medium">显示名</span>
          <FieldReset label="显示名" disabled={!nameDirty} onReset={() => setName(builtin.name)} />
        </div>
        <Input required maxLength={64} value={name} onChange={e => setName(e.target.value)} />
      </div>
      <div className="space-y-2">
        <div className="flex items-center justify-between gap-2">
          <span className="text-sm font-medium">支持模型</span>
          <FieldReset label="支持模型" disabled={!modelsDirty} onReset={() => setModelsText((builtin.models || []).join('\n'))} />
        </div>
        <Textarea rows={6} value={modelsText} onChange={e => setModelsText(e.target.value)} placeholder="每行一个模型名" className="font-mono text-xs" />
        <p className="text-xs text-muted-foreground">仅用于展示与 CC Switch 建议；网关仍原样转发请求中的 model。</p>
      </div>
      <ErrorMessage error={save.error} />
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onClose}>取消</Button>
        <Button type="submit" disabled={save.isPending || !dirty}>{save.isPending ? '处理中…' : '保存'}</Button>
      </div>
    </form>
  )
}
