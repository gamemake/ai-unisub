import { DetailTableRow } from '@/components/detail-table-row'
import { AppSelect } from '@/components/app-select'
import { useEffect, useMemo, useState } from 'react'
import { actions, useAction, useAICatalog, useAIProviders } from '@/data/store'
import type { Account, ClientType, ModelMapping, Supplier } from '@/data/types'
import { ErrorMessage, PageHeader, QueryState, Table } from '@/components/shared'
import { TableCell } from '@/components/ui/table'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog'
import { Textarea } from '@/components/ui/textarea'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { SelectItem } from '@/components/ui/select'
import { Plus, RefreshCw, RotateCcw, Trash2 } from 'lucide-react'
import { clientTypeLabel } from '@/lib/utils'
import { samePlanWeights, subscriptionPlanLabel, subscriptionPlansForSupplier } from '@/lib/subscription-plan'

function accountSupplier(account: Account) {
  return account.config.supplier || ''
}

function providerKind(account: Account) {
  return account.config.kind || (account.provider === 'group' ? 'group' : account.auth_type === 'api_key' ? 'api' : 'subscription')
}

function supplierFromHash() { return location.hash.startsWith('#ai-catalog/') ? location.hash.slice('#ai-catalog/'.length) : '' }

function sameModels(a: string[] = [], b: string[] = []) {
  return a.length === b.length && a.every((value, i) => value === b[i])
}

function sameMappings(a: ModelMapping[] = [], b: ModelMapping[] = []) {
  return a.length === b.length && a.every((row, i) => row.from === b[i]?.from && row.to === b[i]?.to)
}

function parseModels(text: string) {
  return text.split(/\r?\n/).map(line => line.trim()).filter(Boolean)
}

function normalizeMappings(rows: ModelMapping[]) {
  return rows
    .map(row => ({ from: row.from.trim(), to: row.to.trim() }))
    .filter(row => row.from || row.to)
}

function clientLabel(clients: ClientType[] = []) {
  return clients.length ? clients.map(clientTypeLabel).join(' · ') : '—'
}

export function AICatalogPage() {
  const query = useAICatalog()
  const [supplierID, setSupplierID] = useState(supplierFromHash)
  useEffect(() => { const update = () => { setSupplierID(supplierFromHash()); window.scrollTo(0, 0) }; addEventListener('hashchange', update); return () => removeEventListener('hashchange', update) }, [])
  const supplier = query.data?.catalog.suppliers.find(s => s.id === supplierID)
  const builtin = query.data?.builtin_suppliers.find(s => s.id === supplierID)
  return <div><PageHeader title="模型供应商" description="查看内置服务地址，编辑支持模型、模型映射与订阅套餐用量权重；URL 与支持客户端由代码固定。" />
    <QueryState query={query}>{query.data && <>
      <Card><Table className="table-fixed" headers={['供应商', '支持客户端', '默认 URL', '模型']}>{query.data.catalog.suppliers.map(s =>
        <DetailTableRow key={s.id} aria-label={`查看 ${s.name} 详情`} onOpen={() => { location.hash = `ai-catalog/${s.id}` }}>
          <TableCell className="w-[16%] font-medium text-primary">{s.name}</TableCell>
          <TableCell className="w-[18%] whitespace-normal text-sm">{clientLabel(s.supported_clients)}</TableCell>
          <TableCell className="w-[40%] max-w-0 whitespace-normal break-all font-mono text-xs">
            <div>Claude：{s.claude_url || '未提供'}</div>
            <div className="mt-1">OpenAI：{s.openai_url || '未提供'}</div>
          </TableCell>
          <TableCell className="w-[26%] max-w-0 whitespace-normal break-all text-xs text-muted-foreground">
            <div>{s.models?.length ? `${s.models.slice(0, 2).join('、')}${s.models.length > 2 ? ` 等 ${s.models.length} 个` : ''}` : '—'}</div>
            {!!s.model_mappings?.length && <div className="mt-1 text-muted-foreground/80">映射 {s.model_mappings.length} 条</div>}
            {!!Object.keys(s.subscription_plan_weights || {}).length && <div className="mt-1 text-muted-foreground/80">套餐用量权重 {Object.keys(s.subscription_plan_weights || {}).length} 项</div>}
          </TableCell>
        </DetailTableRow>
      )}</Table></Card>
      <Dialog open={!!supplierID} onOpenChange={open => { if (!open) location.hash = 'ai-catalog' }}>
        <DialogContent className="flex h-[min(40rem,calc(100dvh-2rem))] min-w-0 flex-col gap-4 overflow-hidden p-6 sm:max-w-2xl">
          <DialogHeader className="shrink-0 pr-8">
            <DialogTitle className="text-lg font-semibold leading-snug">{supplier ? `模型供应商 ${supplier.name}` : '模型供应商'}</DialogTitle>
            <DialogDescription className="sr-only">编辑支持模型、模型映射与订阅套餐用量权重；服务地址只读。</DialogDescription>
          </DialogHeader>
          {supplier && builtin ? (
            <SupplierEditor key={supplier.id} supplier={supplier} builtin={builtin} onClose={() => { location.hash = 'ai-catalog' }} />
          ) : <p role="alert" className="min-h-0 flex-1">未找到该供应商，请关闭窗口后重新选择。</p>}
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

type EditorTab = 'models' | 'mappings' | 'weights'

function validateSupplierForm(models: string[], mappings: ModelMapping[], planIDs: string[], weights: Record<string, number>) {
  for (const model of models) {
    if (!model || model !== model.trim()) {
      return '模型名不能为空或含首尾空格'
    }
  }
  const seen = new Set<string>()
  for (const model of models) {
    if (seen.has(model)) return `支持模型存在重复项：${model}`
    seen.add(model)
  }
  for (let i = 0; i < mappings.length; i++) {
    const from = mappings[i].from.trim()
    const to = mappings[i].to.trim()
    if (!from && !to) continue
    if (!from || !to) return `第 ${i + 1} 条映射需同时填写客户端模型与上游模型`
    if ((from.match(/\*/g) || []).length > 1) return `第 ${i + 1} 条映射的客户端模型至多一个 * 通配符`
    if (!models.includes(to)) return `第 ${i + 1} 条映射的上游模型「${to}」不在支持模型列表中`
  }
  for (const id of planIDs) {
    const value = weights[id]
    if (!Number.isInteger(value) || value < 1) return `套餐「${subscriptionPlanLabel(id)}」的用量权重须为不小于 1 的整数`
  }
  return ''
}

function SupplierEditor({ supplier, builtin, onClose }: { supplier: Supplier; builtin: Supplier; onClose: () => void }) {
  const providers = useAIProviders()
  const planOptions = useMemo(() => subscriptionPlansForSupplier(supplier.id), [supplier.id])
  const hasPlans = planOptions.length > 0
  const [tab, setTab] = useState<EditorTab>('models')
  const [modelsText, setModelsText] = useState((supplier.models || []).join('\n'))
  const [mappings, setMappings] = useState<ModelMapping[]>(() => (supplier.model_mappings || []).map(row => ({ ...row })))
  const [weights, setWeights] = useState<Record<string, number>>(() => {
    const next: Record<string, number> = {}
    for (const plan of subscriptionPlansForSupplier(supplier.id)) {
      next[plan.id] = supplier.subscription_plan_weights?.[plan.id] ?? builtin.subscription_plan_weights?.[plan.id] ?? 1
    }
    return next
  })
  const [refreshProviderID, setRefreshProviderID] = useState('')
  const [formError, setFormError] = useState('')
  const save = useAction(actions.saveSupplier, ['ai-catalog'])
  const fetchModels = useAction(actions.fetchModels)
  const models = useMemo(() => parseModels(modelsText), [modelsText])
  const normalizedMappings = useMemo(() => normalizeMappings(mappings), [mappings])
  const modelsDirty = !sameModels(models, builtin.models || [])
  const mappingsDirty = !sameMappings(normalizedMappings, builtin.model_mappings || [])
  const weightsDirty = hasPlans && !samePlanWeights(weights, builtin.subscription_plan_weights || {})
  const matchingProviders = useMemo(
    () => (providers.data?.items || []).filter(account => providerKind(account) !== 'group' && accountSupplier(account) === supplier.id),
    [providers.data?.items, supplier.id],
  )

  function submit(e: React.FormEvent) {
    e.preventDefault()
    const cleaned = normalizeMappings(mappings)
    const error = validateSupplierForm(models, cleaned, planOptions.map(p => p.id), weights)
    if (error) {
      setFormError(error)
      return
    }
    setFormError('')
    const payload: Parameters<typeof actions.saveSupplier>[0] = { id: supplier.id, name: supplier.name, models, model_mappings: cleaned }
    if (hasPlans) payload.subscription_plan_weights = weights
    save.mutate(payload, { onSuccess: onClose })
  }

  function refreshFromProvider() {
    const id = Number(refreshProviderID)
    if (!id) return
    fetchModels.mutate(id, {
      onSuccess: result => {
        setModelsText((result.models || []).map(model => model.id).join('\n'))
        setFormError('')
      },
    })
  }

  function updateMapping(index: number, patch: Partial<ModelMapping>) {
    setFormError('')
    setMappings(rows => rows.map((row, i) => i === index ? { ...row, ...patch } : row))
  }

  return (
    <form aria-label="供应商配置" className="flex min-h-0 flex-1 flex-col gap-4 overflow-hidden" noValidate onSubmit={submit}>
      <div className="grid shrink-0 gap-3 sm:grid-cols-2">
        <div className="min-w-0 space-y-1">
          <div className="text-sm text-muted-foreground">Claude</div>
          <div className="break-all font-mono text-xs" aria-label="Claude URL">{supplier.claude_url || '未提供'}</div>
        </div>
        <div className="min-w-0 space-y-1">
          <div className="text-sm text-muted-foreground">OpenAI</div>
          <div className="break-all font-mono text-xs" aria-label="OpenAI URL">{supplier.openai_url || '未提供'}</div>
        </div>
      </div>

      <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-hidden">
        <div className="flex shrink-0 items-end gap-2 border-b">
          <div role="tablist" aria-label="供应商配置分区" className="flex min-w-0 flex-1 gap-1">
            {([
              ['models', '支持模型'] as const,
              ['mappings', '模型映射'] as const,
              ...(hasPlans ? [['weights', '套餐用量权重'] as const] : []),
            ]).map(([id, label]) => (
              <button
                key={id}
                type="button"
                role="tab"
                id={`supplier-tab-${id}`}
                aria-selected={tab === id}
                aria-controls={`supplier-panel-${id}`}
                className={`-mb-px border-b-2 px-3 py-2 text-sm transition-colors ${tab === id ? 'border-primary font-medium text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}`}
                onClick={() => setTab(id)}
              >
                {label}
              </button>
            ))}
          </div>
          {tab === 'models' ? (
            <FieldReset label="支持模型" disabled={!modelsDirty} onReset={() => { setModelsText((builtin.models || []).join('\n')); setFormError('') }} />
          ) : tab === 'mappings' ? (
            <FieldReset label="模型映射" disabled={!mappingsDirty} onReset={() => { setMappings((builtin.model_mappings || []).map(row => ({ ...row }))); setFormError('') }} />
          ) : (
            <FieldReset label="套餐用量权重" disabled={!weightsDirty} onReset={() => {
              const next: Record<string, number> = {}
              for (const plan of planOptions) next[plan.id] = builtin.subscription_plan_weights?.[plan.id] ?? 1
              setWeights(next)
              setFormError('')
            }} />
          )}
        </div>

        {tab === 'models' && (
          <div role="tabpanel" id="supplier-panel-models" aria-labelledby="supplier-tab-models" className="flex min-h-0 flex-1 flex-col gap-2 overflow-hidden">
            <Textarea
              rows={8}
              value={modelsText}
              onChange={e => { setModelsText(e.target.value); setFormError('') }}
              placeholder="每行一个模型名"
              className="min-h-0 flex-1 resize-none font-mono text-xs"
            />
            <div className="flex shrink-0 flex-col gap-2">
              <span className="text-sm font-medium">从账号刷新</span>
              <div className="flex items-center gap-2">
                <div className="min-w-0 flex-1">
                  <AppSelect
                    aria-label="选择用于刷新模型列表的账号"
                    value={refreshProviderID}
                    onValueChange={setRefreshProviderID}
                    disabled={!matchingProviders.length || fetchModels.isPending}
                  >
                    <SelectItem value="">{matchingProviders.length ? '选择同供应商账号' : '暂无可用账号'}</SelectItem>
                    {matchingProviders.map(account => (
                      <SelectItem key={account.id} value={String(account.id)}>
                        {account.name}{account.enabled ? '' : '（已停用）'}
                      </SelectItem>
                    ))}
                  </AppSelect>
                </div>
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  className="size-10 shrink-0"
                  aria-label={fetchModels.isPending ? '刷新中' : '刷新模型'}
                  disabled={!refreshProviderID || fetchModels.isPending}
                  onClick={refreshFromProvider}
                >
                  <RefreshCw className={fetchModels.isPending ? 'animate-spin' : ''} />
                </Button>
              </div>
              <ErrorMessage error={fetchModels.error} />
              <p className="text-xs text-muted-foreground">每行一个模型名。可手工编辑，或用同供应商账号向上游拉取后写入编辑框；保存后才会生效。仅用于展示、CC Switch 建议与模型映射选项。</p>
            </div>
          </div>
        )}

        {tab === 'mappings' && (
          <div role="tabpanel" id="supplier-panel-mappings" aria-labelledby="supplier-tab-mappings" className="flex min-h-0 flex-1 flex-col gap-2 overflow-hidden">
            <div className="min-h-0 flex-1 space-y-2 overflow-y-auto">
              {mappings.length === 0 && (
                <p className="text-sm text-muted-foreground">未配置映射时，请求中的 model 原样转发。</p>
              )}
              {mappings.map((row, index) => {
                const toOptions = models.includes(row.to) || !row.to.trim() ? models : [row.to, ...models]
                return (
                  <div key={index} className="flex items-center gap-2">
                    <Input
                      aria-label={`映射 ${index + 1} 客户端模型`}
                      className="min-w-0 flex-1 font-mono text-xs"
                      placeholder="客户端模型，如 claude-*"
                      value={row.from}
                      onChange={e => updateMapping(index, { from: e.target.value })}
                    />
                    <span className="shrink-0 text-muted-foreground">→</span>
                    <div className="min-w-0 flex-1">
                      <AppSelect
                        aria-label={`映射 ${index + 1} 上游模型`}
                        className="font-mono text-xs"
                        value={row.to}
                        onValueChange={value => updateMapping(index, { to: value })}
                        disabled={!toOptions.length && !row.to}
                      >
                        <SelectItem value="">{toOptions.length ? '选择上游模型' : '请先填写支持模型'}</SelectItem>
                        {toOptions.map(model => (
                          <SelectItem key={model} value={model}>{model}</SelectItem>
                        ))}
                      </AppSelect>
                    </div>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      className="size-10 shrink-0"
                      aria-label={`删除映射 ${index + 1}`}
                      onClick={() => { setFormError(''); setMappings(rows => rows.filter((_, i) => i !== index)) }}
                    >
                      <Trash2 />
                    </Button>
                  </div>
                )
              })}
            </div>
            <div className="flex shrink-0 items-center gap-3">
              <Button type="button" variant="outline" size="sm" className="shrink-0" onClick={() => { setFormError(''); setMappings(rows => [...rows, { from: '', to: '' }]) }}>
                <Plus />添加映射
              </Button>
              <p className="min-w-0 flex-1 text-right text-xs text-muted-foreground">自上而下首条命中；from 支持一个 *，to 选自支持模型。</p>
            </div>
          </div>
        )}

        {tab === 'weights' && hasPlans && (
          <div role="tabpanel" id="supplier-panel-weights" aria-labelledby="supplier-tab-weights" className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto">
            <p className="text-xs text-muted-foreground">用量权重表示同优先级档内的相对用量，供将来负载均衡使用；不等于组员调度优先级。只使用配置值。</p>
            <div className="space-y-3">
              {planOptions.map(plan => (
                <div key={plan.id} className="flex flex-wrap items-center gap-3">
                  <div className="min-w-36 flex-1">
                    <div className="text-sm font-medium">{plan.label}</div>
                    <div className="font-mono text-xs text-muted-foreground">{plan.id}</div>
                  </div>
                  <Input
                    aria-label={`${plan.label} 用量权重`}
                    className="w-28"
                    type="number"
                    min={1}
                    step={1}
                    required
                    value={weights[plan.id] ?? 1}
                    onChange={e => {
                      setFormError('')
                      const value = Number(e.target.value)
                      setWeights(prev => ({ ...prev, [plan.id]: value }))
                    }}
                  />
                </div>
              ))}
            </div>
          </div>
        )}
      </div>

      <div className="flex shrink-0 flex-col gap-2">
        {(formError || save.error) && (
          <div className="space-y-2">
            {formError ? <p role="alert" className="text-sm text-destructive">{formError}</p> : null}
            <ErrorMessage error={save.error} />
          </div>
        )}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose}>取消</Button>
          <Button type="submit" disabled={save.isPending}>{save.isPending ? '处理中…' : '保存'}</Button>
        </div>
      </div>
    </form>
  )
}
