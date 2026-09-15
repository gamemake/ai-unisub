import { useState } from 'react'
import { ChevronDown, Plus } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTitle, PopoverTrigger } from '@/components/ui/popover'

export type ProviderKind = 'subscription' | 'api' | 'group'

export function AddAIProviderButton({ onSelect }: { onSelect: (kind: ProviderKind) => void }) {
  const [open, setOpen] = useState(false)
  return <Popover open={open} onOpenChange={setOpen}>
    <PopoverTrigger render={<Button />}><Plus />添加 AI Provider<ChevronDown /></PopoverTrigger>
    <PopoverContent align="end" className="w-64 gap-1">
      <PopoverTitle className="px-3 py-2 text-sm font-medium">选择类型</PopoverTitle>
      {([
        ['subscription', '订阅', '通过 OAuth 连接订阅账户'],
        ['api', 'API', '通过 API Key 连接模型供应商'],
        ['group', '组', '组合已有 AI Provider 并配置权重'],
      ] as const).map(([kind, label, description]) => <Button key={kind} variant="ghost" aria-label={`添加${label} AI Provider`} className="h-auto flex-col items-start gap-1 px-3 py-2" onClick={() => { setOpen(false); onSelect(kind) }}>
        <span>{label}</span><span className="text-xs font-normal text-muted-foreground">{description}</span>
      </Button>)}
    </PopoverContent>
  </Popover>
}
