import { QueryClient } from '@tanstack/react-query'
export const queryClient = new QueryClient({ defaultOptions: { queries: { staleTime: 15_000, retry: false, refetchOnWindowFocus: false } } })
export class APIError extends Error { constructor(message: string, public status: number) { super(message) } }
const messages: Record<string, string> = {
  'invalid username or password': '用户名或密码错误', 'username and password are required': '请输入用户名和密码',
  'unauthorized': '登录已过期，请重新登录', 'forbidden': '没有权限执行此操作',
  'old password is incorrect': '当前密码不正确', 'user name already exists': '用户名已存在',
}
export async function request<T>(url: string, options: RequestInit = {}): Promise<T> {
  let response: Response
  try { response = await fetch(url, { ...options, credentials: 'same-origin', headers: { ...(options.body ? { 'Content-Type': 'application/json' } : {}), ...options.headers } }) }
  catch (error) { if ((error as Error).name === 'AbortError') throw error; throw new APIError('网络请求失败，请检查连接后重试', 0) }
  if (response.status === 401 && url !== '/api/login') {
    await queryClient.cancelQueries()
    queryClient.clear()
    if (location.pathname !== '/login') location.assign('/login')
  }
  if (response.status === 204) return undefined as T
  let data
  try { data = await response.json() } catch { throw new APIError('服务器返回了无法解析的响应', response.status) }
  if (!response.ok) {
    const raw = typeof data.error === 'string' ? data.error : data.error?.message
    throw new APIError(messages[raw] || raw || `请求失败 (${response.status})`, response.status)
  }
  return data as T
}
export const json = (method: string, body?: unknown): RequestInit => ({ method, ...(body === undefined ? {} : { body: JSON.stringify(body) }) })
