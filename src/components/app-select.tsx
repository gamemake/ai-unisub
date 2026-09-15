import { Children, isValidElement, useEffect, useRef, type ReactNode, type ComponentProps } from 'react'
import { Select, SelectContent, SelectTrigger, SelectValue, SelectItem } from './ui/select'
import { cn } from '@/lib/utils'

/** Domain adapter over the shadcn Base UI Select, preserving labels before opening. */
export function AppSelect({ value, onValueChange, children, required, disabled, name, className, valueLabel, contentClassName, ...triggerProps }: {
  value: string
  onValueChange: (value: string) => void
  children: ReactNode
  required?: boolean
  disabled?: boolean
  name?: string
  valueLabel?: ReactNode
  contentClassName?: string
} & Pick<ComponentProps<typeof SelectTrigger>, 'id' | 'aria-label' | 'aria-describedby' | 'className'>) {
  const options = Children.toArray(children).filter(isValidElement<ComponentProps<typeof SelectItem>>)
  const items = options.map(option => ({ value: option.props.value as string, label: option.props.children }))
  const placeholder = items.find(item => item.value === '')?.label
  const inputRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    // After an empty submission, Base UI can retain its native required message
    // on the hidden control even once the controlled selection has a value.
    // This adapter has no custom validators; let native required validation
    // evaluate the new value instead of retaining the old custom message.
    if (value && inputRef.current?.value && inputRef.current.validity.customError) inputRef.current.setCustomValidity('')
  }, [value])
  return <Select inputRef={inputRef} value={required && !value ? null : value} onValueChange={next => onValueChange(next ?? '')} items={items} required={required} disabled={disabled} name={name}>
    <SelectTrigger {...triggerProps} className={cn('w-full min-w-0', className)}><SelectValue placeholder={placeholder}>{valueLabel}</SelectValue></SelectTrigger>
    <SelectContent alignItemWithTrigger={false} className={contentClassName}>
      {options.filter(option => !required || option.props.value !== '')}
    </SelectContent>
  </Select>
}
