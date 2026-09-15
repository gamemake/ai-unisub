import { useState } from 'react'
import { CalendarIcon } from 'lucide-react'
import { format, parseISO } from 'date-fns'
import { zhCN } from 'react-day-picker/locale'
import { Button } from './ui/button'
import { Calendar } from './ui/calendar'
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover'

export function DatePicker({ value, onChange, min, max, id, 'aria-describedby': description }: {
  value: string
  onChange: (value: string) => void
  min?: string
  max?: string
  id?: string
  'aria-describedby'?: string
}) {
  const [open, setOpen] = useState(false)
  const selected = value ? parseISO(value) : undefined
  return <Popover open={open} onOpenChange={setOpen}>
    <PopoverTrigger render={<Button id={id} type="button" variant="outline" aria-describedby={description} className="w-full min-w-40 justify-start font-normal" />}>
      <CalendarIcon />{value || '选择日期'}
    </PopoverTrigger>
    <PopoverContent align="start" className="w-auto p-0" aria-label="选择日期">
      <Calendar mode="single" locale={zhCN} selected={selected} defaultMonth={selected} autoFocus
        className="[--cell-size:--spacing(9)]"
        disabled={[...(min ? [{ before: parseISO(min) }] : []), ...(max ? [{ after: parseISO(max) }] : [])]}
        onSelect={day => { if (day) { onChange(format(day, 'yyyy-MM-dd')); setOpen(false) } }} />
    </PopoverContent>
  </Popover>
}
