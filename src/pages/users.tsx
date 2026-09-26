import { Switch } from '@/components/ui/switch'
import { TableRow, TableCell } from '@/components/ui/table'
import { AppSelect } from '@/components/app-select'
import { SelectItem } from '@/components/ui/select'
import { useState } from 'react'
import { KeyRound, Plus, Trash2 } from 'lucide-react'
import { actions, useAction, useUsers } from '@/data/store'
import type { User } from '@/data/types'
import { Badge, Confirm, Empty, ErrorMessage, Field, Modal, PageHeader, QueryState, Submit, Table } from '@/components/shared'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { date } from '@/lib/utils'

export function Users({ me }: { me: User }) {
  const query = useUsers(), [adding, setAdding] = useState(false), [reset, setReset] = useState<User | null>(null), [removing, setRemoving] = useState<User | null>(null)
  const update = useAction(actions.updateUser, ['users']), remove = useAction(actions.deleteUser, ['users'])
  return <><PageHeader title="用户管理" description="管理控制台账号、成员角色与访问状态。" action={<Button onClick={() => setAdding(true)}><Plus />添加用户</Button>} /><div className="mb-4"><ErrorMessage error={update.error} /></div><Card><QueryState query={query}>{query.data?.items.length ? <Table headers={['用户名', '角色', '状态', '创建时间', '操作']}>{query.data.items.map(u => <TableRow key={u.id}><TableCell className="font-medium">{u.name}{u.id === me.id && <span className="ml-2 text-xs text-muted-foreground">当前用户</span>}</TableCell><TableCell><AppSelect aria-label={`${u.name} 的角色`} disabled={u.id === me.id || update.isPending} className="w-28" value={u.role} onValueChange={value => update.mutate({ id: u.id, role: value as User['role'] })}><SelectItem value="user">成员</SelectItem><SelectItem value="admin">管理员</SelectItem></AppSelect></TableCell><TableCell><div className="flex items-center gap-2"><Switch disabled={u.id === me.id || update.isPending} aria-label={`${u.enabled ? '停用' : '启用'} ${u.name}`} checked={u.enabled} onCheckedChange={checked => update.mutate({ id: u.id, enabled: checked })} /><Badge enabled={u.enabled} /></div></TableCell><TableCell className="text-muted-foreground">{date(u.created_at)}</TableCell><TableCell><div className="flex gap-1"><Button variant="ghost" size="icon" disabled={u.id === me.id} aria-label={`重置 ${u.name} 的密码`} onClick={() => setReset(u)}><KeyRound /></Button><Button variant="ghost" size="icon" disabled={u.id === me.id} aria-label={`删除 ${u.name}`} onClick={() => { remove.reset(); setRemoving(u) }}><Trash2 /></Button></div></TableCell></TableRow>)}</Table> : <Empty />}</QueryState></Card>{adding && <UserForm onClose={() => setAdding(false)} />}{reset && <UserForm user={reset} onClose={() => setReset(null)} />}{removing && <Confirm title={`删除用户「${removing.name}」？`} pending={remove.isPending} error={remove.error} onClose={() => setRemoving(null)} onConfirm={() => remove.mutate(removing.id, { onSuccess: () => setRemoving(null) })} />}</>
}
function UserForm({ user, onClose }: { user?: User; onClose: () => void }) {
  const [name, setName] = useState(''), [password, setPassword] = useState(''), [role, setRole] = useState<User['role']>('user')
  const create = useAction(actions.createUser, ['users']), reset = useAction(actions.resetPassword)
  const mutation = user ? reset : create
  return <Modal title={user ? `重置 ${user.name} 的密码` : '添加用户'} onClose={onClose}><form className="space-y-5" onSubmit={e => { e.preventDefault(); if (user) reset.mutate({ id: user.id, password }, { onSuccess: onClose }); else create.mutate({ name, password, role }, { onSuccess: onClose }) }}>{!user && <><Field label="用户名"><Input required minLength={2} maxLength={32} value={name} onChange={e => setName(e.target.value)} /></Field><Field label="角色"><AppSelect value={role} onValueChange={value => setRole(value as User['role'])}><SelectItem value="user">成员</SelectItem><SelectItem value="admin">管理员</SelectItem></AppSelect></Field></>}<Field label="新密码" hint="至少 12 个字符"><Input type="password" required minLength={12} autoComplete="new-password" value={password} onChange={e => setPassword(e.target.value)} /></Field><ErrorMessage error={mutation.error} /><div className="flex justify-end"><Submit pending={mutation.isPending} /></div></form></Modal>
}
export function Security() {
  const [oldPassword, setOldPassword] = useState(''), [password, setPassword] = useState(''), [confirm, setConfirm] = useState(''), [error, setError] = useState<Error | null>(null), change = useAction(actions.password)
  return <><PageHeader title="安全设置" description="维护你的登录密码，保护工作空间。" /><Card className="max-w-xl p-6"><h2 className="mb-5 font-semibold">修改登录密码</h2><form className="space-y-5" onSubmit={e => { e.preventDefault(); setError(null); if (password !== confirm) { setError(new Error('两次输入的新密码不一致')); return } change.mutate({ old_password: oldPassword, new_password: password }, { onSuccess: () => { setOldPassword(''); setPassword(''); setConfirm('') } }) }}><Field label="当前密码"><Input required type="password" autoComplete="current-password" value={oldPassword} onChange={e => setOldPassword(e.target.value)} /></Field><Field label="新密码" hint="至少 12 个字符"><Input required minLength={12} type="password" autoComplete="new-password" value={password} onChange={e => setPassword(e.target.value)} /></Field><Field label="确认新密码"><Input required type="password" autoComplete="new-password" value={confirm} onChange={e => setConfirm(e.target.value)} /></Field><ErrorMessage error={error || change.error} />{change.isSuccess && <p role="status" className="text-sm text-primary">密码已更新。</p>}<Submit pending={change.isPending}>保存新密码</Submit></form></Card></>
}
