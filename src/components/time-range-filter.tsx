import { useState } from 'react'
import { Search } from 'lucide-react'
import { Button } from './ui/button'
import { ToggleGroup, ToggleGroupItem } from './ui/toggle-group'
import { Field } from './shared'
import { DatePicker } from './date-picker'

export type TimeFilter = { range: string; from?: string; to?: string }

export function TimeRangeFilter({ onChange }: { onChange: (value: TimeFilter) => void }) {
  const [range, setRange] = useState('1d'), [from, setFrom] = useState(''), [to, setTo] = useState('')
  return <div className="flex flex-wrap items-end gap-3">
    <ToggleGroup aria-label="统计时间范围" value={[range]} onValueChange={values => {
      if (!values.length) return
      const next = values[0]
      setRange(next)
      if (next !== 'custom') onChange({ range: next })
      else if (from && to && from <= to) onChange({ range: next, from, to })
    }} className="h-10 rounded-lg border p-1">
      {[['1d', '24 小时'], ['1w', '7 天'], ['1m', '30 天'], ['custom', '自定义']].map(([value, label]) => <ToggleGroupItem key={value} value={value} className="h-full data-pressed:bg-primary data-pressed:text-primary-foreground">{label}</ToggleGroupItem>)}
    </ToggleGroup>
    <form aria-hidden={range !== 'custom'} inert={range !== 'custom'} className={`grid w-full items-end gap-2 sm:w-auto sm:grid-cols-[1fr_1fr_auto] ${range !== 'custom' ? 'invisible' : ''}`} onSubmit={event => {
      event.preventDefault()
      if (from && to && from <= to) onChange({ range: 'custom', from, to })
    }}>
      <Field label="开始日期"><DatePicker value={from} max={to || undefined} onChange={setFrom} /></Field>
      <Field label="结束日期"><DatePicker value={to} min={from || undefined} onChange={setTo} /></Field>
      <Button size="icon" className="size-10" type="submit" aria-label="查询" disabled={!from || !to || from > to}><Search /></Button>
    </form>
  </div>
}
