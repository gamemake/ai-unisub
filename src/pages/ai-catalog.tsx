import { DetailTableRow } from '@/components/detail-table-row'
import { useEffect, useId, useState } from 'react'
import { actions, useAction, useAICatalog } from '@/data/store'
import type { ModelMapping, Supplier } from '@/data/types'
import { ErrorMessage, PageHeader, QueryState, Submit, Table } from '@/components/shared'
import { Button } from '@/components/ui/button'
import { TableCell } from '@/components/ui/table'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogTitle, DialogDescription } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'

const clients = [['Anthropic', 'Claude'], ['OpenAI', 'Codex'], ['Grok', 'Grok']] as const
const mappingKey = (m: ModelMapping) => `${m.client}/${m.model}`

function supplierFromHash() { return location.hash.startsWith('#ai-catalog/') ? location.hash.slice('#ai-catalog/'.length) : '' }
export function AICatalogPage() {
  const query = useAICatalog()
  const [supplierID, setSupplierID] = useState(supplierFromHash)
  useEffect(() => { const update = () => { setSupplierID(supplierFromHash()); window.scrollTo(0, 0) }; addEventListener('hashchange', update); return () => removeEventListener('hashchange', update) }, [])
  const supplier = query.data?.catalog.suppliers.find(s => s.id === supplierID)
  const defaults = query.data?.builtin_suppliers.find(s => s.id === supplierID)
  return <div><PageHeader title="模型供应商" description="选择供应商查看详情，查看内置服务地址，独立维护模型映射。" />
    <QueryState query={query}>{query.data && <>
      <Card><Table headers={['供应商', '默认 URL', '模型映射', '配置']}>{query.data.catalog.suppliers.map(s => {
        const builtin = query.data!.builtin_suppliers.find(b => b.id === s.id)!
        const count = new Set([...builtin.mappings, ...s.mappings].map(mappingKey)).size
        return <DetailTableRow key={s.id} aria-label={`查看 ${s.name} 详情`} onOpen={() => { location.hash = `ai-catalog/${s.id}` }}><TableCell className="font-medium text-primary">{s.name}</TableCell><TableCell className="max-w-96 break-all font-mono text-xs"><div>Claude：{s.claude_url || "未提供"}</div><div className="mt-1">Codex / Grok：{s.codex_url || "未提供"}</div></TableCell><TableCell>{count} 条<div className="mt-1 text-xs text-muted-foreground">Claude / Codex / Grok</div></TableCell><TableCell>{s.mappings.length ? `${s.mappings.length} 条自定义映射` : '内置映射'}<div className="mt-1 text-xs text-muted-foreground">内置地址 · 只读</div></TableCell></DetailTableRow>
      })}</Table></Card>
      <Dialog open={!!supplierID} onOpenChange={open => { if (!open) location.hash = 'ai-catalog' }}>
        <DialogContent className="flex h-[min(52rem,calc(100dvh-2rem))] max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 transition-none sm:max-w-4xl">
          <div className="shrink-0 border-b px-5 py-4 pr-12 sm:px-6">
            <DialogTitle>{supplier ? `${supplier.name} 配置` : '模型供应商详情'}</DialogTitle>
            <DialogDescription className="sr-only">查看当前客户端的内置服务地址，编辑模型映射。</DialogDescription>
          </div>
          {supplier && defaults ? <div className="min-h-0 flex-1"><CatalogForm key={supplierID} initial={supplier} defaults={defaults} /></div> : <p role="alert" className="p-6">未找到该供应商，请关闭窗口后重新选择。</p>}
        </DialogContent>
      </Dialog>
    </>}</QueryState>
  </div>
}
function CatalogForm({ initial, defaults }: { initial: Supplier; defaults: Supplier }) {
  const urlID = useId()
  const [supplier, setSupplier] = useState<Supplier>(() => structuredClone(initial))
  const [savedSupplier, setSavedSupplier] = useState<Supplier>(() => structuredClone(initial))
  const [client, setClient] = useState<ModelMapping['client']>('Anthropic')
  const [validation, setValidation] = useState<Error | null>(null)
  const save = useAction(actions.saveAISupplier, ['ai-catalog'])
  const defaultMappings = defaults.mappings.filter(m => m.client === client)
  const customMappings = supplier.mappings.map((m, index) => ({ ...m, index })).filter(m => m.client === client && !defaultMappings.some(b => mappingKey(b) === mappingKey(m)))
  function updateSupplier(patch: Partial<Supplier>) {
    setSupplier(current => ({ ...current, ...patch }))
    setValidation(null)
    save.reset()
  }
  function override(mapping: ModelMapping, target: string) {
    const rest = supplier.mappings.filter(m => mappingKey(m) !== mappingKey(mapping))
    updateSupplier({ mappings: target === mapping.target ? rest : [...rest, { ...mapping, target }] })
  }
  function updateCustom(index: number, patch: Partial<ModelMapping>) {
    updateSupplier({ mappings: supplier.mappings.map((m, i) => i === index ? { ...m, ...patch } : m) })
  }
  function submit(e: React.FormEvent) {
    e.preventDefault()
    for (const s of [supplier]) {
      const keys = new Set<string>()
      for (const m of s.mappings) {
        if (!m.model.trim() || !m.target.trim() || m.model !== m.model.trim() || m.target !== m.target.trim() || keys.has(mappingKey(m))) {
          setClient(m.client)
          setValidation(new Error(`${s.name} 的模型映射名称不能为空、带首尾空格或重复，请检查后保存。`))
          return
        }
        keys.add(mappingKey(m))
      }
    }
    save.mutate(supplier, { onSuccess: result => { const saved = result.catalog.suppliers.find(s => s.id === supplier.id)!; setSupplier(structuredClone(saved)); setSavedSupplier(structuredClone(saved)) } })
  }
  return <form aria-label="供应商配置" onSubmit={submit} className="h-full min-h-0"><fieldset disabled={save.isPending} className="flex h-full min-h-0 min-w-0 flex-col"><div className="flex min-h-0 flex-1 flex-col gap-4 overflow-hidden p-4 sm:px-6">

    <div className="flex shrink-0 flex-wrap items-center justify-between gap-3"><h3 className="font-medium">模型映射</h3><div className="flex gap-2" role="group" aria-label="选择映射客户端">{clients.map(([value, label]) => <Button key={value} type="button" size="sm" variant={client === value ? 'default' : 'outline'} aria-pressed={client === value} onClick={() => setClient(value)}>{label}</Button>)}</div></div>
    <section className="shrink-0 space-y-1.5" aria-label={`${supplier.name} 服务地址`}>
      <div className="flex items-center gap-3">
        <label htmlFor={urlID} className="shrink-0 text-sm font-medium">{client === 'Anthropic' ? 'Claude URL' : 'Codex / Grok URL'}</label>
        <Input id={urlID} aria-label={`${supplier.name} ${client === 'Anthropic' ? 'Claude' : 'Codex / Grok'} URL`} aria-describedby={urlID + '-hint'} readOnly value={client === 'Anthropic' ? supplier.claude_url : supplier.codex_url} placeholder="未提供该协议的内置地址" />
      </div>
      <p id={urlID + '-hint'} className="text-xs text-muted-foreground">系统内置地址，不可修改。{client !== 'Anthropic' && 'Grok 与 Codex 共用此地址。'}</p>
    </section>
    <p className="shrink-0 text-xs text-muted-foreground">客户端模型 → {supplier.name} 模型，仅映射名称，不转换协议。</p>
    <div role="region" className="min-h-0 flex-1 space-y-2 overflow-y-auto overscroll-contain rounded-lg border p-3" aria-label="模型映射列表">
      <div className="grid grid-cols-2 gap-3 text-xs text-muted-foreground"><span>客户端模型</span><span>供应商模型</span></div>
      {!defaultMappings.length && !customMappings.length && <p className="py-6 text-center text-sm text-muted-foreground">当前客户端的原厂模型直接透传，无需默认映射。</p>}
      {defaultMappings.map(m => { const custom = supplier.mappings.find(v => mappingKey(v) === mappingKey(m)); return <div key={mappingKey(m)} className="grid gap-2 rounded-md bg-muted/30 p-2 sm:grid-cols-[1fr_1fr_auto]">
        <span className="self-center break-all font-mono text-xs">{m.model}</span><Input aria-label={`${m.model} 目标模型`} required value={custom?.target ?? m.target} onChange={e => override(m, e.target.value)} /><Button type="button" size="sm" variant="ghost" disabled={!custom} onClick={() => override(m, m.target)}>恢复默认</Button>
      </div> })}
      {customMappings.map(m => <div key={m.index} className="grid gap-2 rounded-md border p-2 sm:grid-cols-[1fr_1fr_auto]"><Input aria-label="请求模型" required value={m.model} placeholder="客户端模型名称" onChange={e => updateCustom(m.index, { model: e.target.value })} /><Input aria-label="目标模型" required value={m.target} placeholder="供应商模型名称" onChange={e => updateCustom(m.index, { target: e.target.value })} /><Button type="button" size="sm" variant="ghost" onClick={() => updateSupplier({ mappings: supplier.mappings.filter((_, i) => i !== m.index) })}>删除映射</Button></div>)}
    </div>
    <Button type="button" variant="outline" className="shrink-0 self-start" onClick={() => updateSupplier({ mappings: [...supplier.mappings, { client, model: '', target: '' }] })}>添加映射</Button>
    </div><div className="shrink-0 space-y-3 border-t bg-card p-4 sm:px-6"><ErrorMessage error={validation || save.error} />{save.isSuccess && <p role="status" className="text-sm text-primary">供应商配置已保存</p>}<div className="flex flex-wrap items-center justify-between gap-3"><p className="hidden text-xs text-muted-foreground sm:block">只保存当前供应商的自定义映射。</p><div className="flex gap-3"><Button type="button" variant="outline" onClick={() => { setSupplier(structuredClone(savedSupplier)); setValidation(null); save.reset() }}>撤销未保存修改</Button><Submit pending={save.isPending} /></div></div>
  </div></fieldset></form>
}
