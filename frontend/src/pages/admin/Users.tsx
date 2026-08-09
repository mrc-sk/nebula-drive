import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Search, Plus, Pencil, Ban, Key, Trash2, Loader2, X } from 'lucide-react'
import { api } from '../../api/client'
import { useAuthStore } from '../../store/auth'

interface UserRow {
  id: number | string
  userName?: string
  email?: string
  nickname?: string
  groupId?: number | string
  groupName?: string
  storage?: number
  maxStorage?: number
  status?: number | string
  banned?: boolean
  isAdmin?: boolean
  [k: string]: any
}

export default function Users() {
  const { t } = useTranslation()
  const me = useAuthStore((s) => s.user)
  const [loading, setLoading] = useState(true)
  const [rows, setRows] = useState<UserRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [size] = useState(20)
  const [keyword, setKeyword] = useState('')
  const [groups, setGroups] = useState<any[]>([])

  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<UserRow | null>(null)
  const [form, setForm] = useState<any>({})
  const [saving, setSaving] = useState(false)

  const load = async () => {
    setLoading(true)
    try {
      const [u, g] = await Promise.all([api.admin.users(page, size), api.admin.groups()])
      if (u?.code === 0 && u.data) {
        setRows((u.data.items || []).filter((r: UserRow) => !keyword || match(r, keyword)))
        setTotal(u.data.total || 0)
      }
      if (g?.code === 0) setGroups(g.data || [])
    } catch {
      // ignore
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
  }, [page])

  useEffect(() => {
    setRows((rs) => rs.filter((r) => !keyword || match(r, keyword)))
  }, [keyword])

  const openCreate = () => {
    setEditing(null)
    setForm({
      userName: '',
      password: '',
      email: '',
      nickname: '',
      groupId: '',
      isAdmin: false,
    })
    setOpen(true)
  }

  const openEdit = (u: UserRow) => {
    setEditing(u)
    setForm({
      userName: u.userName || '',
      email: u.email || '',
      nickname: u.nickname || '',
      groupId: u.groupId || '',
      isAdmin: !!u.isAdmin,
    })
    setOpen(true)
  }

  const submit = async () => {
    setSaving(true)
    try {
      if (editing) {
        await api.admin.updateUser(editing.id, form)
      } else {
        await api.admin.createUser(form)
      }
      setOpen(false)
      load()
    } catch {
      alert('Failed')
    } finally {
      setSaving(false)
    }
  }

  const del = async (u: UserRow) => {
    if (!confirm(`Delete user ${u.userName}?`)) return
    try {
      await api.admin.deleteUser(u.id)
      load()
    } catch {
      alert('Failed')
    }
  }

  const banToggle = async (u: UserRow) => {
    try {
      await api.admin.updateUser(u.id, { banned: !u.banned, status: u.banned ? 1 : 0 })
      load()
    } catch {
      alert('Failed')
    }
  }

  const resetPwd = async (u: UserRow) => {
    const p = prompt(`New password for ${u.userName}:`, '')
    if (!p) return
    try {
      await api.admin.updateUser(u.id, { password: p })
      alert('OK')
    } catch {
      alert('Failed')
    }
  }

  const filtered = rows.filter((r) => !keyword || match(r, keyword))
  const pages = Math.max(1, Math.ceil(total / size))
  const meId = me?.id

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold">{t('ns_admin.users')}</h1>
          <p className="text-sm text-slate-400">Total: {total}</p>
        </div>
        <div className="flex items-center gap-2">
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
            <input
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              placeholder={t('ns_admin.search')}
              className="glass-input pl-9 sm:w-64"
            />
          </div>
          <button type="button" onClick={openCreate} className="btn-primary">
            <Plus className="h-4 w-4" />
            {t('ns_admin.newUser')}
          </button>
        </div>
      </div>

      <div className="glass overflow-hidden rounded-2xl">
        <div className="overflow-x-auto">
          <table className="min-w-full text-sm">
            <thead className="border-b border-white/10 bg-white/5 text-left text-xs uppercase text-slate-400">
              <tr>
                <th className="px-4 py-3">ID</th>
                <th className="px-4 py-3"></th>
                <th className="px-4 py-3">用户名</th>
                <th className="px-4 py-3">邮箱</th>
                <th className="px-4 py-3">昵称</th>
                <th className="px-4 py-3">用户组</th>
                <th className="px-4 py-3">存储</th>
                <th className="px-4 py-3">状态</th>
                <th className="px-4 py-3">管理员</th>
                <th className="px-4 py-3 text-right">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-white/5">
              {loading && (
                <tr>
                  <td colSpan={10} className="py-10 text-center">
                    <Loader2 className="mx-auto h-6 w-6 animate-spin" />
                  </td>
                </tr>
              )}
              {!loading &&
                filtered.map((u) => {
                  const initial = (u.userName || u.nickname || 'A').charAt(0).toUpperCase()
                  const isSelf = String(meId) === String(u.id)
                  return (
                    <tr key={u.id} className="hover:bg-white/5">
                      <td className="px-4 py-3 text-slate-400">#{u.id}</td>
                      <td className="px-4 py-3">
                        <span className="grid h-8 w-8 place-items-center rounded-full bg-gradient-to-br from-indigo-500 to-cyan-400 text-xs font-semibold text-white">
                          {initial}
                        </span>
                      </td>
                      <td className="px-4 py-3 font-medium">{u.userName}</td>
                      <td className="px-4 py-3 text-slate-400">{u.email || '-'}</td>
                      <td className="px-4 py-3">{u.nickname || '-'}</td>
                      <td className="px-4 py-3 text-slate-400">{u.groupName || (groups.find((g) => String(g.id) === String(u.groupId))?.name) || '-'}</td>
                      <td className="px-4 py-3 text-slate-400">
                        {fmt(u.storage)} / {fmt(u.maxStorage) || '∞'}
                      </td>
                      <td className="px-4 py-3">
                        <span
                          className={`rounded-full px-2 py-0.5 text-xs ${
                            u.banned || String(u.status) === '0' ? 'bg-rose-500/20 text-rose-300' : 'bg-emerald-500/20 text-emerald-300'
                          }`}
                        >
                          {u.banned || String(u.status) === '0' ? t('ns_admin.ban') : 'OK'}
                        </span>
                      </td>
                      <td className="px-4 py-3">
                        {u.isAdmin ? (
                          <span className="rounded-full bg-amber-500/20 px-2 py-0.5 text-xs text-amber-300">Admin</span>
                        ) : (
                          <span className="text-slate-400">-</span>
                        )}
                      </td>
                      <td className="px-4 py-3">
                        <div className="flex justify-end gap-1">
                          <IconBtn onClick={() => openEdit(u)} title="Edit">
                            <Pencil className="h-4 w-4" />
                          </IconBtn>
                          <IconBtn onClick={() => resetPwd(u)} title="Reset">
                            <Key className="h-4 w-4" />
                          </IconBtn>
                          <IconBtn
                            onClick={() => banToggle(u)}
                            title={u.banned ? 'Unban' : 'Ban'}
                            disabled={isSelf}
                          >
                            <Ban className="h-4 w-4" />
                          </IconBtn>
                          <IconBtn onClick={() => del(u)} danger disabled={isSelf} title="Delete">
                            <Trash2 className="h-4 w-4" />
                          </IconBtn>
                        </div>
                      </td>
                    </tr>
                  )
                })}
              {!loading && filtered.length === 0 && (
                <tr>
                  <td colSpan={10} className="py-10 text-center text-slate-400">
                    -
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
        <div className="flex items-center justify-between border-t border-white/10 px-4 py-3 text-sm">
          <div className="text-slate-400">
            {page} / {pages}
          </div>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page <= 1}
              className="btn-ghost px-3 py-1.5 text-xs disabled:opacity-50"
            >
              Prev
            </button>
            <button
              type="button"
              onClick={() => setPage((p) => Math.min(pages, p + 1))}
              disabled={page >= pages}
              className="btn-ghost px-3 py-1.5 text-xs disabled:opacity-50"
            >
              Next
            </button>
          </div>
        </div>
      </div>

      {open && (
        <Modal onClose={() => setOpen(false)} title={editing ? 'Edit User' : t('ns_admin.newUser')}>
          <div className="space-y-3">
            <Field label={t('auth.username')}>
              <input
                className="glass-input"
                value={form.userName}
                onChange={(e) => setForm({ ...form, userName: e.target.value })}
              />
            </Field>
            {!editing && (
              <Field label={t('auth.password')}>
                <input
                  type="password"
                  className="glass-input"
                  value={form.password}
                  onChange={(e) => setForm({ ...form, password: e.target.value })}
                />
              </Field>
            )}
            <Field label="邮箱">
              <input
                className="glass-input"
                value={form.email}
                onChange={(e) => setForm({ ...form, email: e.target.value })}
              />
            </Field>
            <Field label="昵称">
              <input
                className="glass-input"
                value={form.nickname}
                onChange={(e) => setForm({ ...form, nickname: e.target.value })}
              />
            </Field>
            <Field label="用户组">
              <select
                className="glass-input"
                value={form.groupId}
                onChange={(e) => setForm({ ...form, groupId: e.target.value })}
              >
                <option value="">-</option>
                {groups.map((g) => (
                  <option key={g.id} value={g.id}>
                    {g.name}
                  </option>
                ))}
              </select>
            </Field>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={!!form.isAdmin}
                onChange={(e) => setForm({ ...form, isAdmin: e.target.checked })}
                className="h-4 w-4 rounded border-white/20 bg-white/10 accent-indigo-500"
              />
              管理员
            </label>
          </div>
          <div className="mt-5 flex justify-end gap-2">
            <button type="button" onClick={() => setOpen(false)} className="btn-ghost">
              {t('common.cancel')}
            </button>
            <button type="button" onClick={submit} disabled={saving} className="btn-primary">
              {saving && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('common.save')}
            </button>
          </div>
        </Modal>
      )}
    </div>
  )
}

function match(r: UserRow, k: string) {
  const s = k.toLowerCase()
  return (
    String(r.userName || '').toLowerCase().includes(s) ||
    String(r.email || '').toLowerCase().includes(s) ||
    String(r.nickname || '').toLowerCase().includes(s)
  )
}

function fmt(b: any) {
  if (!b) return '0 B'
  let n = Number(b) || 0
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (n >= 1024 && i < u.length - 1) {
    n /= 1024
    i++
  }
  return `${n.toFixed(n >= 10 ? 0 : 1)} ${u[i]}`
}

function IconBtn({
  children,
  onClick,
  title,
  disabled,
  danger,
}: {
  children: React.ReactNode
  onClick?: () => void
  title?: string
  disabled?: boolean
  danger?: boolean
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      disabled={disabled}
      className={`grid h-8 w-8 place-items-center rounded-lg transition disabled:opacity-30 ${
        danger ? 'hover:bg-rose-500/20 hover:text-rose-300' : 'hover:bg-white/10'
      }`}
    >
      {children}
    </button>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <div className="mb-1 text-xs text-slate-400">{label}</div>
      {children}
    </label>
  )
}

export function Modal({
  children,
  onClose,
  title,
}: {
  children: React.ReactNode
  onClose: () => void
  title: string
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4" onClick={onClose}>
      <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" />
      <div
        className="glass-strong relative w-full max-w-lg rounded-2xl p-6"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-4 flex items-center justify-between">
          <h3 className="text-lg font-semibold">{title}</h3>
          <button
            type="button"
            onClick={onClose}
            className="grid h-8 w-8 place-items-center rounded-lg hover:bg-white/10"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        {children}
      </div>
    </div>
  )
}
