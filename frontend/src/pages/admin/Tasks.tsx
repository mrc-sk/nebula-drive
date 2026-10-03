import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Plus, Loader2, XCircle, RotateCcw, Link as LinkIcon, Magnet } from 'lucide-react'
import { api } from '../../api/client'
import { Modal } from './Users'

type TaskType = 'http' | 'bt'
type TaskStatus = 'pending' | 'running' | 'success' | 'failed'

// 后端 Task.Status 是 int（0 等待 / 1 进行 / 2 完成 / 3 失败），
// 必须显式映射成展示用的字符串枚举。
//
// 之前写的是 `(tk.status || tk.state || 'pending') as TaskStatus` —— 那相当于把
// int 硬转成字符串枚举：status=1 会渲染成字面量 "task.1"，且徽章配色回落到 pending。
// status=0 恰好因为 falsy 落到 'pending' 才显得"正常"，纯属巧合。
const STATUS_BY_CODE: Record<number, TaskStatus> = {
  0: 'pending',
  1: 'running',
  2: 'success',
  3: 'failed',
}

function toStatus(raw: unknown): TaskStatus {
  if (typeof raw === 'number') return STATUS_BY_CODE[raw] || 'pending'
  // 兼容未来后端改成字符串枚举
  if (typeof raw === 'string' && raw in { pending: 1, running: 1, success: 1, failed: 1 }) {
    return raw as TaskStatus
  }
  return 'pending'
}

export default function Tasks() {
  const { t } = useTranslation()
  const [rows, setRows] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [open, setOpen] = useState(false)
  const [type, setType] = useState<TaskType>('http')
  const [form, setForm] = useState({ url: '', parentId: '' })
  const [saving, setSaving] = useState(false)

  const load = async () => {
    try {
      const r = await api.tasks.list()
      if (r?.code === 0) setRows(r.data || [])
    } finally {
      setLoading(false)
    }
  }

  // 轮询：仅当存在「等待中/进行中」任务时才继续，否则空转。
  // 之前是无条件 setInterval(load, 2000)，即使全部任务都已完成也一直在打接口。
  useEffect(() => {
    let alive = true
    let timer: any = null

    const tick = async () => {
      try {
        const r = await api.tasks.list()
        if (!alive) return // 卸载后不再 setState
        if (r?.code === 0) {
          setRows(r.data || [])
          // 全部任务都进了终态就不再轮询
          const hasActive = (r.data || []).some(
            (t: any) => {
              const s = toStatus(t.status ?? t.state)
              return s === 'running' || s === 'pending'
            },
          )
          if (!hasActive && timer) {
            clearInterval(timer)
            timer = null
          }
        }
      } catch {
        // 轮询失败静默重试，不打断用户
      } finally {
        if (alive) setLoading(false)
      }
    }

    tick()
    timer = setInterval(tick, 2000)
    return () => {
      alive = false
      if (timer) clearInterval(timer)
    }
  }, [])

  const submit = async () => {
    if (!form.url) return
    setSaving(true)
    try {
      await api.tasks.create({
        type,
        url: form.url,
        parentId: form.parentId || null,
      })
      setOpen(false)
      setForm({ url: '', parentId: '' })
      load()
    } catch {
      alert('Failed')
    } finally {
      setSaving(false)
    }
  }

  const cancel = async (id: any) => {
    try {
      await api.tasks.cancel(id)
      load()
    } catch {
      alert('Failed')
    }
  }

  const retry = async (id: any) => {
    try {
      await api.tasks.retry(id)
      load()
    } catch {
      alert('Failed')
    }
  }

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{t('ns_admin.tasks')}</h1>
          <p className="text-sm text-slate-400">Background download tasks</p>
        </div>
        <button type="button" onClick={() => setOpen(true)} className="btn-primary">
          <Plus className="h-4 w-4" />
          {t('ns_admin.createTask')}
        </button>
      </div>

      <div className="glass overflow-hidden rounded-2xl">
        <table className="min-w-full text-sm">
          <thead className="border-b border-white/10 bg-white/5 text-left text-xs uppercase text-slate-400">
            <tr>
              <th className="px-4 py-3">ID</th>
              <th className="px-4 py-3">{t('ns_admin.type')}</th>
              <th className="px-4 py-3">URL</th>
              <th className="px-4 py-3 w-64">{t('ns_admin.progress')}</th>
              <th className="px-4 py-3">状态</th>
              <th className="px-4 py-3 text-right">操作</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-white/5">
            {loading && (
              <tr>
                <td colSpan={6} className="py-10 text-center">
                  <Loader2 className="mx-auto h-6 w-6 animate-spin" />
                </td>
              </tr>
            )}
            {!loading &&
              rows.map((tk) => {
                const status = toStatus(tk.status ?? tk.state)
                const pct = Number(tk.progress ?? tk.percent ?? 0)
                return (
                  <tr key={tk.id} className="hover:bg-white/5">
                    <td className="px-4 py-3 text-slate-400">#{tk.id}</td>
                    <td className="px-4 py-3">
                      <span className="inline-flex items-center gap-1 rounded-full bg-sky-500/20 px-2 py-0.5 text-xs text-sky-300">
                        {tk.type === 'bt' ? <Magnet className="h-3 w-3" /> : <LinkIcon className="h-3 w-3" />}
                        {t(`task.${tk.type || 'http'}`)}
                      </span>
                    </td>
                    <td className="px-4 py-3">
                      <div className="max-w-md truncate text-slate-300" title={tk.url}>
                        {tk.url || tk.name}
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-2">
                        <div className="h-2 flex-1 overflow-hidden rounded-full bg-white/10">
                          <div
                            className="h-full rounded-full bg-gradient-to-r from-indigo-500 to-cyan-400 transition-all"
                            style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
                          />
                        </div>
                        <span className="w-12 text-right text-xs tabular-nums text-slate-400">{pct.toFixed(0)}%</span>
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      <StatusBadge status={status} />
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex justify-end gap-1">
                        {(status === 'running' || status === 'pending') && (
                          <button
                            type="button"
                            onClick={() => cancel(tk.id)}
                            title={t('ns_admin.cancel')}
                            className="grid h-8 w-8 place-items-center rounded-lg hover:bg-white/10"
                          >
                            <XCircle className="h-4 w-4" />
                          </button>
                        )}
                        {status === 'failed' && (
                          <button
                            type="button"
                            onClick={() => retry(tk.id)}
                            title={t('ns_admin.retry')}
                            className="grid h-8 w-8 place-items-center rounded-lg hover:bg-white/10"
                          >
                            <RotateCcw className="h-4 w-4" />
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                )
              })}
            {!loading && rows.length === 0 && (
              <tr>
                <td colSpan={6} className="py-10 text-center text-slate-400">
                  -
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {open && (
        <Modal onClose={() => setOpen(false)} title={t('ns_admin.createTask')}>
          <div className="mb-3 flex gap-2">
            <TabBtn active={type === 'http'} onClick={() => setType('http')}>
              <LinkIcon className="h-3.5 w-3.5" />
              {t('ns_admin.httpTask')}
            </TabBtn>
            <TabBtn active={type === 'bt'} onClick={() => setType('bt')}>
              <Magnet className="h-3.5 w-3.5" />
              {t('ns_admin.btTask')}
            </TabBtn>
          </div>
          <div className="space-y-3">
            <Field label={t('task.enterUrl')}>
              <input
                className="glass-input"
                placeholder={type === 'bt' ? 'magnet:?xt=... 或 .torrent URL' : 'https://...'}
                value={form.url}
                onChange={(e) => setForm({ ...form, url: e.target.value })}
              />
            </Field>
            <Field label={t('task.targetDir')}>(可选，留空为根目录)
              <input
                className="glass-input"
                value={form.parentId}
                onChange={(e) => setForm({ ...form, parentId: e.target.value })}
              />
            </Field>
          </div>
          <div className="mt-5 flex justify-end gap-2">
            <button type="button" onClick={() => setOpen(false)} className="btn-ghost">
              {t('common.cancel')}
            </button>
            <button type="button" onClick={submit} disabled={saving || !form.url} className="btn-primary">
              {saving && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('task.create')}
            </button>
          </div>
        </Modal>
      )}
    </div>
  )
}

function StatusBadge({ status }: { status: TaskStatus }) {
  const { t } = useTranslation()
  const map: Record<TaskStatus, string> = {
    pending: 'bg-slate-500/20 text-slate-300',
    running: 'bg-sky-500/20 text-sky-300',
    success: 'bg-emerald-500/20 text-emerald-300',
    failed: 'bg-rose-500/20 text-rose-300',
  }
  return (
    <span className={`rounded-full px-2 py-0.5 text-xs ${map[status] || map.pending}`}>
      {t(`task.${status}`)}
    </span>
  )
}

function TabBtn({
  active,
  onClick,
  children,
}: {
  active: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`inline-flex items-center gap-1.5 rounded-xl px-4 py-2 text-sm transition ${
        active ? 'bg-gradient-to-r from-indigo-500/40 to-cyan-400/30 text-white' : 'text-slate-300 hover:bg-white/10'
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
