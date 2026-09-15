import { Children, isValidElement, type ReactNode, type ComponentProps } from 'react'
import { Select, SelectContent, SelectTrigger, SelectValue, SelectItem } from './ui/select'
import { cn } from '@/lib/utils'

/** Domain adapter over the shadcn Base UI Select, preserving labels before opening. */
export function AppSelect({ value, onValueChange, children, required, disabled, name, className, ...triggerProps }: {
  value: string
  onValueChange: (value: string) => void
  children: ReactNode
  required?: boolean
  disabled?: boolean
  name?: string
} & Pick<ComponentProps<typeof SelectTrigger>, 'id' | 'aria-label' | 'aria-describedby' | 'className'>) {
  const options = Children.toArray(children).filter(isValidElement<ComponentProps<typeof SelectItem>>)
  const items = options.map(option => ({ value: option.props.value as string, label: option.props.children }))
  const placeholder = items.find(item => item.value === '')?.label
  return <Select value={required && !value ? null : value} onValueChange={next => onValueChange(next ?? '')} items={items} required={required} disabled={disabled} name={name}>
    <SelectTrigger {...triggerProps} className={cn('h-10 w-full min-w-0', className)}><SelectValue placeholder={placeholder} /></SelectTrigger>
    <SelectContent alignItemWithTrigger={false}>
      {options.filter(option => !required || option.props.value !== '')}
    </SelectContent>
  </Select>
}
