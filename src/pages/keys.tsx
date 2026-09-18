import { DetailTableRow } from '@/components/detail-table-row'
import { TableCell } from '@/components/ui/table'
import { AppSelect } from '@/components/app-select'
import { SelectItem } from '@/components/ui/select'
import { useId, useState, type MouseEvent } from 'react'
import { Check, Copy, Eye, Plus, Share2, Trash2 } from 'lucide-react'
import { actions, useProviderOptions, useAction, useKeys } from '@/data/store'
import type { APIKey } from '@/data/types'
import { Badge, Confirm, Empty, ErrorMessage, Field, Modal, PageHeader, QueryState, Submit, Table } from '@/components/shared'
import { Button } from '@/components/ui/button'
import { Field as UIField, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Card } from '@/components/ui/card'
import { date } from '@/lib/utils'
import { ccSwitchEndpoint, ccSwitchImport, publicBaseURL } from '@/lib/cc-switch'

function CopyIconButton({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false)
  async function copy(event?: MouseEvent) {
    event?.preventDefault()
    event?.stopPropagation()
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
    <Button type="button" variant="ghost" size="icon" className="shrink-0 text-muted-foreground" aria-label={copied ? `已复制${label}` : `复制${label}到剪贴板`} disabled={!text} onClick={copy}>
      {copied ? <Check className="text-emerald-300" /> : <Copy />}
    </Button>
  )
}

function CopyField({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  const id = useId()
  return (
    <UIField>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <div className="flex items-center gap-1">
        <Input id={id} readOnly value={value} className={mono ? 'min-w-0 flex-1 font-mono' : 'min-w-0 flex-1'} onFocus={e => e.target.select()} />
        <CopyIconButton text={value} label={label} />
      </div>
    </UIField>
  )
}

function KeyActions({ value, aiProvider }: { value: APIKey; aiProvider: string }) {
  const imported = ccSwitchImport({ pageURL: location.href, aiProvider, name: value.name, apiKey: value.key })
  return (
    <div className="flex gap-1">
      {imported ? (
        <Button
          variant="ghost"
          size="icon"
          className="text-muted-foreground"
          aria-label={`导入 ${value.name} 到 CC Switch`}
          title="导入 CC Switch"
          nativeButton={false}
          render={<a href={imported.href} />}
        >
          <Share2 />
        </Button>
      ) : null}
      <CopyIconButton text={value.key} label={`${value.name} API Key`} />
    </div>
  )
}

export function Keys() {
  const query = useKeys(), accounts = useProviderOptions(), [adding, setAdding] = useState(false), [revealed, setRevealed] = useState<APIKey | null>(null), [removing, setRemoving] = useState<APIKey | null>(null)
  const remove = useAction(actions.deleteKey, ['keys'])
  const rows = query.data?.items || []
  function providerOf(key: APIKey) {
    return accounts.data?.items.find(a => a.id === key.account_id)
  }
  return <>
    <PageHeader title="API Key" description="为客户端签发访问密钥，每把 Key 绑定一个 AI Provider（订阅、API 或组）。" action={<Button onClick={() => setAdding(true)}><Plus />签发 Key</Button>} />
    <Card>
      <QueryState query={query}>
        {rows.length ? (
          <Table headers={['名称', '绑定 AI Provider', '密钥', '有效期', '操作']}>
            {rows.map(k => {
              const provider = providerOf(k)
              return (
                <DetailTableRow key={k.id} aria-label={`查看 ${k.name} 详情`} onOpen={() => setRevealed(k)}>
                  <TableCell className="font-medium">{k.name}</TableCell>
                  <TableCell>{provider?.name || k.account_id}</TableCell>
                  <TableCell className="font-mono text-muted-foreground">••••••••{k.key.slice(-4)}</TableCell>
                  <TableCell>
                    {k.expires_at ? (
                      <>
                        <Badge enabled={Date.parse(k.expires_at) > Date.now()}>{Date.parse(k.expires_at) > Date.now() ? '有效' : '已过期'}</Badge>
                        <div className="mt-1 text-xs text-muted-foreground">{date(k.expires_at)}</div>
                      </>
                    ) : <Badge enabled>长期有效</Badge>}
                  </TableCell>
                  <TableCell>
                    <div className="flex gap-1">
                      <KeyActions value={k} aiProvider={provider?.provider || ''} />
                      <Button variant="ghost" size="icon" aria-label={`查看 ${k.name}`} onClick={() => setRevealed(k)}><Eye /></Button>
                      <Button variant="ghost" size="icon" aria-label={`删除 ${k.name}`} onClick={() => { remove.reset(); setRemoving(k) }}><Trash2 /></Button>
                    </div>
                  </TableCell>
                </DetailTableRow>
              )
            })}
          </Table>
        ) : <Empty>暂无 API Key</Empty>}
      </QueryState>
    </Card>
    {adding && <KeyForm onClose={() => setAdding(false)} onCreated={k => { setAdding(false); setRevealed(k) }} />}
    {revealed && <KeyDetails value={revealed} aiProvider={providerOf(revealed)?.provider || ''} onClose={() => setRevealed(null)} />}
    {removing && <Confirm title={`删除密钥「${removing.name}」？`} error={remove.error} pending={remove.isPending} onClose={() => setRemoving(null)} onConfirm={() => remove.mutate(removing.id, { onSuccess: () => setRemoving(null) })} />}
  </>
}

function KeyForm({ onClose, onCreated }: { onClose: () => void; onCreated: (key: APIKey) => void }) {
  const accounts = useProviderOptions(), [name, setName] = useState(''), [account, setAccount] = useState(''), [days, setDays] = useState(0), create = useAction(actions.createKey, ['keys'])
  return (
    <Modal title="签发 API Key" onClose={onClose}>
      <QueryState query={accounts}>
        <form className="space-y-5" onSubmit={e => { e.preventDefault(); create.mutate({ name, account_id: Number(account), valid_seconds: days * 86400 }, { onSuccess: onCreated }) }}>
          <Field label="名称"><Input required maxLength={64} value={name} onChange={e => setName(e.target.value)} placeholder="例如 Claude Code" /></Field>
          <Field label="绑定 AI Provider">
            <AppSelect required value={account} onValueChange={value => setAccount(value)}>
              <SelectItem value="">请选择 AI Provider</SelectItem>
              {accounts.data?.items.filter(a => a.enabled).map(a => <SelectItem key={a.id} value={String(a.id)}>{a.name} · {a.provider}</SelectItem>)}
            </AppSelect>
          </Field>
          <Field label="有效期（天）" hint="0 表示长期有效"><Input required type="number" min={0} max={36500} step={1} value={days} onChange={e => setDays(Number(e.target.value))} /></Field>
          <ErrorMessage error={create.error} />
          <div className="flex justify-end"><Submit pending={create.isPending}>签发 Key</Submit></div>
        </form>
      </QueryState>
    </Modal>
  )
}

export function KeyDetails({ value, aiProvider, onClose }: { value: APIKey; aiProvider: string; onClose: () => void }) {
  const imported = ccSwitchImport({ pageURL: location.href, aiProvider, name: value.name, apiKey: value.key })
  const endpoint = imported?.endpoint || ccSwitchEndpoint(publicBaseURL(location.href), aiProvider)
  return (
    <Modal title={value.name} description="请妥善保存密钥，仅与需要调用此 AI Provider的客户端共享。" onClose={onClose}>
      <div className="space-y-4">
        <CopyField label="API Key" value={value.key} mono />
        <CopyField label="Base URL" value={endpoint} />
      </div>
    </Modal>
  )
}
