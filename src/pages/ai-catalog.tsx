import { DetailTableRow } from '@/components/detail-table-row'
import { useEffect, useState } from 'react'
import { useAICatalog } from '@/data/store'
import { Field, PageHeader, QueryState, Table } from '@/components/shared'
import { TableCell } from '@/components/ui/table'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'

function supplierFromHash() { return location.hash.startsWith('#ai-catalog/') ? location.hash.slice('#ai-catalog/'.length) : '' }
export function AICatalogPage() {
  const query = useAICatalog()
  const [supplierID, setSupplierID] = useState(supplierFromHash)
  useEffect(() => { const update = () => { setSupplierID(supplierFromHash()); window.scrollTo(0, 0) }; addEventListener('hashchange', update); return () => removeEventListener('hashchange', update) }, [])
  const supplier = query.data?.catalog.suppliers.find(s => s.id === supplierID)
  return <div><PageHeader title="模型供应商" description="选择供应商查看详情及内置服务地址。" />
    <QueryState query={query}>{query.data && <>
      <Card><Table headers={['供应商', '默认 URL']}>{query.data.catalog.suppliers.map(s =>
        <DetailTableRow key={s.id} aria-label={`查看 ${s.name} 详情`} onOpen={() => { location.hash = `ai-catalog/${s.id}` }}><TableCell className="font-medium text-primary">{s.name}</TableCell><TableCell className="max-w-96 break-all font-mono text-xs"><div>Claude：{s.claude_url || '未提供'}</div><div className="mt-1">Codex / Grok：{s.codex_url || '未提供'}</div></TableCell></DetailTableRow>
      )}</Table></Card>
      <Dialog open={!!supplierID} onOpenChange={open => { if (!open) location.hash = 'ai-catalog' }}>
        <DialogContent className="gap-6 p-6 sm:max-w-2xl">
          <DialogHeader className="pr-8">
            <DialogTitle className="text-lg font-semibold leading-snug">{supplier ? `${supplier.name} 详情` : '模型供应商详情'}</DialogTitle>
            <DialogDescription className="leading-relaxed">内置服务地址只读，Grok 与 Codex 共用地址。</DialogDescription>
          </DialogHeader>
          {supplier ? <div className="space-y-4">
            <Field label={`${supplier.name} Claude URL`}><Input readOnly value={supplier.claude_url} placeholder="未提供该协议的内置地址" /></Field>
            <Field label={`${supplier.name} Codex / Grok URL`}><Input readOnly value={supplier.codex_url} placeholder="未提供该协议的内置地址" /></Field>
          </div> : <p role="alert">未找到该供应商，请关闭窗口后重新选择。</p>}
        </DialogContent>
      </Dialog>
    </>}</QueryState>
  </div>
}
