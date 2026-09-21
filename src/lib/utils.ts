import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'
import type { ClientType } from '@/data/types'

export function cn(...inputs: ClassValue[]) { return twMerge(clsx(inputs)) }
export function date(value?: string) { return value ? new Date(value).toLocaleString('zh-CN') : '—' }
export function number(value?: number) { return new Intl.NumberFormat('zh-CN').format(value || 0) }

/** UI labels for the API client type values. */
const clientTypeLabels: Record<ClientType, string> = {
  claude: 'Claude',
  codex: 'Codex',
  grok: 'Grok',
}

export function clientTypeLabel(client: ClientType | string) {
  return clientTypeLabels[client as ClientType] || client
}
