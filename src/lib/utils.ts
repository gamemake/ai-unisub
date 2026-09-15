import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'
export function cn(...inputs: ClassValue[]) { return twMerge(clsx(inputs)) }
export function date(value?: string) { return value ? new Date(value).toLocaleString('zh-CN') : '—' }
export function number(value?: number) { return new Intl.NumberFormat('zh-CN').format(value || 0) }
