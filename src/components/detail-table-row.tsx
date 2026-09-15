import type { ComponentProps } from 'react'
import { TableRow } from '@/components/ui/table'
import { cn } from '@/lib/utils'

export function DetailTableRow({ onOpen, className, ...props }: Omit<ComponentProps<'tr'>, 'onClick' | 'onKeyDown'> & { onOpen?: () => void }) {
  if (!onOpen) return <TableRow {...props} className={className} />
  return <TableRow {...props} tabIndex={0} aria-haspopup="dialog" className={cn('cursor-pointer focus-visible:bg-muted/50 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring', className)} onClick={event => {
    const target = event.target as Element
    if (target.closest('button, a, input, select, textarea, label, [role="button"], [role="combobox"], [role="switch"], [role="checkbox"], [contenteditable="true"]')) return
    if (window.getSelection()?.toString()) return
    onOpen()
  }} onKeyDown={event => {
    if (event.target === event.currentTarget && (event.key === 'Enter' || event.key === ' ')) {
      event.preventDefault()
      onOpen()
    }
  }} />
}
