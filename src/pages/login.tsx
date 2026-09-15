import { useState, type FormEvent } from 'react'
import { ArrowRight, Layers3, ShieldCheck } from 'lucide-react'
import { actions, useAction } from '@/data/store'
import { Input } from '@/components/ui/input'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { ErrorMessage, Field, Submit } from '@/components/shared'

export function Login() {
  const [username, setUsername] = useState(''), [password, setPassword] = useState('')
  const login = useAction(actions.login)
  function submit(e: FormEvent) { e.preventDefault(); login.mutate({ username: username.trim(), password }) }
  return <main className="grid min-h-dvh items-center gap-12 p-6 lg:grid-cols-2 lg:px-[10vw]">
    <section className="hidden lg:block"><div className="mb-12 flex items-center gap-3 text-xl font-semibold"><span className="rounded-xl bg-primary p-2.5 text-primary-foreground"><Layers3 /></span>UNISUB</div><p className="mb-5 text-sm tracking-[.2em] text-primary">ONE GATEWAY. ALL YOUR AI.</p><h1 className="max-w-lg text-5xl font-semibold leading-tight">让每一次 AI 调用，<br /><span className="text-primary">简单而有序。</span></h1><p className="mt-6 max-w-md leading-7 text-muted-foreground">统一管理 OpenAI、Anthropic 与 Grok。连接订阅、分配密钥，在一个工作台掌握所有调用。</p><div className="mt-10 flex gap-3">{['OpenAI', 'Anthropic', 'Grok'].map(n => <span key={n} className="rounded-full border px-4 py-2 text-sm text-muted-foreground">{n}</span>)}</div></section>
    <Card className="mx-auto w-full max-w-md p-3 sm:p-5"><CardHeader><span className="mb-5 text-sm font-semibold text-primary lg:hidden">UNISUB</span><CardTitle className="text-2xl">欢迎回来</CardTitle><p className="text-sm text-muted-foreground">登录 UniSub，继续管理你的 AI 工作空间。</p></CardHeader><CardContent><form onSubmit={submit} className="space-y-5"><Field label="用户名"><Input autoFocus autoComplete="username" required value={username} onChange={e => setUsername(e.target.value)} placeholder="输入用户名" /></Field><Field label="密码"><Input type="password" autoComplete="current-password" required value={password} onChange={e => setPassword(e.target.value)} placeholder="输入密码" /></Field><ErrorMessage error={login.error} /><div className="[&>button]:w-full"><Submit pending={login.isPending}>登录控制台<ArrowRight /></Submit></div></form><p className="mt-7 flex items-center justify-center gap-2 text-xs text-muted-foreground"><ShieldCheck className="size-4" />使用你的管理员或成员账号登录</p></CardContent></Card>
  </main>
}
