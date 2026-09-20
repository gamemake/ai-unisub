import { Sheet, SheetContent, SheetTitle, SheetDescription } from './components/ui/sheet'
import { Separator } from './components/ui/separator'
import { lazy, Suspense, useEffect, useState } from 'react'
import { Activity, SlidersHorizontal, ChevronRight, Home, KeyRound, Layers3, LayoutDashboard, LogOut, Menu, Network, RefreshCw, Shield, Users as UsersIcon } from 'lucide-react'
import { actions, useAction, useMe } from './data/store'
import { queryClient } from './data/client'
import { Button } from './components/ui/button'
import { ErrorMessage, QueryState } from './components/shared'
import { Login } from './pages/login'
const Overview = lazy(() => import('./pages/overview').then(module => ({ default: module.Overview })))
const Personal = lazy(() => import('./pages/overview').then(module => ({ default: module.Personal })))
const AIProviders = lazy(() => import('./pages/ai-providers').then(module => ({ default: module.AIProviders })))
const AICatalog = lazy(() => import('./pages/ai-catalog').then(module => ({ default: module.AICatalogPage })))
const Keys = lazy(() => import('./pages/keys').then(module => ({ default: module.Keys })))
const Security = lazy(() => import('./pages/users').then(module => ({ default: module.Security })))
const Users = lazy(() => import('./pages/users').then(module => ({ default: module.Users })))
const Logs = lazy(() => import('./pages/logs').then(module => ({ default: module.Logs })))
const Proxies = lazy(() => import('./pages/proxies').then(module => ({ default: module.Proxies })))
import { cn } from './lib/utils'
const navigation = [
  { id: 'personal', label: '个人总览', icon: Home }, { id: 'keys', label: 'API Key', icon: KeyRound },
  { id: 'logs', label: '调用记录', icon: Activity }, { id: 'security', label: '安全设置', icon: Shield },
  { id: 'overview', label: '系统总览', icon: LayoutDashboard, admin: true }, { id: 'ai-providers', label: '账号管理', icon: Layers3, admin: true },
  { id: 'ai-catalog', label: '模型供应商', icon: SlidersHorizontal, admin: true },
  { id: 'proxies', label: '代理管理', icon: Network, admin: true }, { id: 'users', label: '用户管理', icon: UsersIcon, admin: true },
]
function pageFromHash() { const raw = location.hash.slice(1); if (raw.startsWith('ai-catalog/')) return 'ai-catalog'; return (raw === 'accounts' || raw === 'providers') ? 'ai-providers' : raw || 'personal' }
export default function App() {
  const me = useMe()
  if (me.data === null) return <Login />
  return <QueryState query={me}>{me.data && <Dashboard />}</QueryState>
}
function Dashboard() {
  const me = useMe(), [page, setPage] = useState(pageFromHash), [mobileOpen, setMobileOpen] = useState(false), logout = useAction(actions.logout)
  useEffect(() => { const update = () => setPage(pageFromHash()); addEventListener('hashchange', update); return () => removeEventListener('hashchange', update) }, [])
  const admin = me.data?.role === 'admin', items = navigation.filter(n => !n.admin || admin), active = items.find(n => n.id === page) || items[0]
  function navigate(id: string) { location.hash = id; setPage(id); setMobileOpen(false) }
  const sidebar = <>      <div className="flex h-20 items-center gap-3 px-6"><span className="rounded-xl bg-primary p-2 text-primary-foreground"><Layers3 className="size-5" /></span><span className="font-semibold tracking-[.16em]">UNISUB</span></div>
      <p className="px-6 pb-3 pt-5 text-[10px] tracking-[.18em] text-muted-foreground">工作空间</p><nav className="space-y-1 px-3" aria-label="主导航">{items.map((n, i) => <div key={n.id}>{n.admin && !items[i - 1]?.admin && <p className="px-3 pb-3 pt-7 text-[10px] tracking-[.18em] text-muted-foreground">管理中心</p>}<Button variant="ghost" aria-current={active.id === n.id ? 'page' : undefined} onClick={() => navigate(n.id)} className={cn('h-auto w-full justify-start gap-3 rounded-lg px-3 py-3 text-sm transition-colors', active.id === n.id ? 'bg-primary/10 font-medium text-primary' : 'text-muted-foreground hover:bg-accent hover:text-foreground')}><n.icon className="size-[18px]" />{n.label}{active.id === n.id && <ChevronRight className="ml-auto size-4" />}</Button></div>)}</nav>
      <div className="mt-auto space-y-2 px-6 py-6 text-xs text-muted-foreground"><p>Server {me.data?.server_version || 'dev'}</p><p>© {new Date().getFullYear()} UniSub</p></div>
</>
  return <div className="min-h-dvh lg:pl-60">
    <aside className="fixed inset-y-0 left-0 z-40 hidden w-60 flex-col border-r bg-card lg:flex">{sidebar}</aside>
    <Sheet open={mobileOpen} onOpenChange={setMobileOpen}><SheetContent side="left" className="w-60! gap-0 overflow-y-auto" aria-describedby="navigation-description"><SheetTitle className="sr-only">导航</SheetTitle><SheetDescription id="navigation-description" className="sr-only">选择工作空间页面</SheetDescription>{sidebar}</SheetContent></Sheet>
    <header className="sticky top-0 z-20 flex h-20 items-center justify-between border-b bg-background/95 px-5 backdrop-blur-md lg:px-9"><div className="flex items-center gap-3"><Button variant="ghost" size="icon" className="lg:hidden" aria-label="打开导航" onClick={() => setMobileOpen(true)}><Menu /></Button><span className="text-sm text-muted-foreground">UniSub <span className="mx-2 text-border">/</span> <span className="text-foreground">{active.label}</span></span></div><div className="flex items-center gap-2"><Button variant="ghost" size="icon" aria-label="刷新数据" onClick={() => queryClient.invalidateQueries()}><RefreshCw /></Button><Separator orientation="vertical" className="mx-2 h-5" /><Button variant="ghost" size="icon" aria-label="退出登录" disabled={logout.isPending} onClick={() => logout.mutate()}><LogOut /></Button></div></header>
    <main className="mx-auto max-w-[1600px] p-5 lg:p-9"><ErrorMessage error={logout.error} /><QueryState query={me}><Suspense fallback={<div role="status" className="p-12 text-center text-muted-foreground">正在加载页面…</div>}>{me.data && <div key={active.id}>{active.id === 'personal' && <Personal me={me.data} />}{active.id === 'keys' && <Keys />}{active.id === 'logs' && <Logs me={me.data} />}{active.id === 'security' && <Security />}{admin && active.id === 'overview' && <Overview />}{admin && active.id === 'ai-providers' && <AIProviders />}{admin && active.id === 'ai-catalog' && <AICatalog />}{admin && active.id === 'users' && <Users me={me.data} />}{admin && active.id === 'proxies' && <Proxies />}</div>}</Suspense></QueryState></main>
  </div>
}
