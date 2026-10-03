import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { AlertCircle, Download, Link as LinkIcon, Loader2, Magnet, Plus, RotateCcw, X, XCircle } from 'lucide-react'
import { api, type TaskItem } from '../api/client'
import MainLayout from '../layouts/MainLayout'
import { Modal } from '../components/Modal'
import { formatTime } from '../utils/format'

type TaskStatus = 'pending' | 'running' | 'success' | 'failed'

// 后端 Task.Status 是 int（0 等待 / 1 进行 / 2 完成 / 3 失败），必须显式映射。
// 之前 admin/Tasks.tsx 写的是 `(tk.status || tk.state || 'pending') as TaskStatus` ——
// 那等于把 int 硬转字符串枚举，status=1 会渲染成字面量 "task.1"，徽章配色也回落到 pending。
const STATUS_BY_CODE: Record<number, TaskStatus> = {
  0: 'pending',
  1: 'running',
  2: 'success',
  3: 'failed',
}

function toStatus(raw: unknown): TaskStatus {
  if (typeof raw === 'number') return STATUS_BY_CODE[raw] || 'pending'
  if (typeof raw === 'string' && raw in { pending: 1, running: 1, success: 1, failed: 1 }) {
    return raw as TaskStatus
  }
  return 'pending'
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

/**
 * 离线下载（/tasks）
 *
 * 侧边栏入口从未注册过路由，点击后被 App.tsx 的 catch-all 重定向回文件页。
 *
 * 与 /admin/tasks 的关系：两者调的是**同一个**接口 GET /api/tasks ——
 * 后端 ListTasks 按 u.IsAdmin 决定是否过滤 owner_id（task.go:35-44）。
 * 所以本页与管理员页数据同源，区别只在侧边栏位置与「只管自己的」语义。
 *
 * 后端能力边界：
 *   POST /api/tasks/:id/cancel  取消
 *   POST /api/tasks/:id/retry   重试（本轮新增，此前前端按钮打的是 404）
 *   —— 没有暂停/继续/删除任务。ListTasks 硬编码 LIMIT 100，无分页。
 */
export default function TasksOffline() {
  const { t } = useTranslation()
  const nav = useNavigate()

  const [rows, setRows] = useState<TaskItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [open, setOpen] = useState(false)
  const [type, setType] = useState<'http' | 'bt'>('http')
  const [form, setForm] = useState({ url: '', parentId: '' })
  const [saving, setSaving] = useState(false)
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const r = await api.tasks.list()
      if (r?.code === 0) setRows(r.data || [])
      else setError(r?.message || t('ns_mine.loadFailed'))
    } catch (e: any) {
      setError(e?.message || t('ns_mine.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  // 轮询：仅当存在「等待中/进行中」任务时继续，否则空转。
  // 用 alive 守卫避免卸载后 setState。
  const tickRef = useRef<() => void>(() => {})
  useEffect(() => {
    let alive = true
    let timer: any = null

    const tick = async () => {
      try {
        const r = await api.tasks.list()
        if (!alive) return
        if (r?.code === 0) {
          const next = r.data || []
          setRows(next)
          const hasActive = next.some((x) => {
            const s = toStatus(x.status)
            return s === 'running' || s === 'pending'
          })
          // 全部进入终态就停止轮询
          if (!hasActive && timer) {
            clearInterval(timer)
            timer = null
          }
        }
      } catch {
        // 轮询失败静默重试
      } finally {
        if (alive) setLoading(false)
      }
    }
    tickRef.current = tick

    tick()
    timer = setInterval(tick, 2000)
    return () => {
      alive = false
      if (timer) clearInterval(timer)
    }
  }, [])

  // 手动刷新（操作完成后调用），复用轮询的 tick 以保持一致的数据来源
  const refresh = useCallback(() => {
    tickRef.current()
  }, [])

  const act = async (id: number, fn: (id: number) => Promise<any>, errKey: string) => {
    setBusyId(id)
    setError('')
    try {
      const r = await fn(id)
      if (r?.code !== 0) setError(r?.message || t(errKey))
      refresh()
    } catch (e: any) {
      setError(e?.message || t(errKey))
    } finally {
      setBusyId(null)
    }
  }

  const submit = async () => {
    if (!form.url.trim()) return
    setSaving(true)
    setError('')
    try {
      const r = await api.tasks.create({
        type,
        url: form.url.trim(),
        parentId: form.parentId ? Number(form.parentId) : null,
      })
      if (r?.code !== 0) {
        setError(r?.message || t('ns_mine.loadFailed'))
        return
      }
      setOpen(false)
      setForm({ url: '', parentId: '' })
      refresh()
    } catch (e: any) {
      setError(e?.message || t('ns_mine.loadFailed'))
    } finally {
      setSaving(false)
    }
  }

  const toolbar = useMemo(
    () => (
      <button type="button" onClick={() => setOpen(true)} className="btn-primary mr-2">
        <Plus className="h-4 w-4" />
        <span className="hidden sm:inline">{t('ns_admin.createTask')}</span>
      </button>
    ),
    [t],
  )

  return (
    <MainLayout
      breadcrumb={[{ name: t('files'), onClick: () => nav('/files') }, { name: t('tasksOffline') }]}
      toolbar={toolbar}
    >
      <div className="flex h-full min-h-[60vh] flex-col gap-3">
        {error && (
          <div className="flex items-center gap-2 rounded-xl border border-rose-400/30 bg-rose-500/10 px-4 py-2 text-sm text-rose-300">
            <AlertCircle className="h-4 w-4 shrink-0" />
            <span className="flex-1">{error}</span>
            <button type="button" onClick={() => setError('')}>
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
        )}

        <div className="glass flex-1 overflow-auto rounded-2xl">
          {loading && rows.length === 0 ? (
            <div className="flex h-full min-h-[40vh] items-center justify-center">
              <Loader2 className="h-7 w-7 animate-spin text-nebula-300" />
            </div>
          ) : rows.length === 0 ? (
            <div className="flex h-full min-h-[40vh] flex-col items-center justify-center gap-3 text-slate-400">
              <div className="grid h-16 w-16 place-items-center rounded-2xl bg-white/5">
                <Download className="h-8 w-8" />
              </div>
              <p className="text-sm">{t('ns_mine.emptyTasks')}</p>
            </div>
          ) : (
            <table className="min-w-full text-sm">
              <thead className="sticky top-0 z-10 border-b border-white/10 bg-black/30 text-left text-xs uppercase text-slate-400 backdrop-blur">
                <tr>
                  <th className="px-4 py-3">ID</th>
                  <th className="px-4 py-3">{t('ns_admin.type')}</th>
                  <th className="px-4 py-3">URL</th>
                  <th className="w-56 px-4 py-3">{t('ns_admin.progress')}</th>
                  <th className="px-4 py-3">{t('task.status')}</th>
                  <th className="px-4 py-3">{t('ns_files.modified')}</th>
                  <th className="px-4 py-3 text-right">{t('ns_mine.actions')}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-white/5">
                {rows.map((tk) => {
                  const status = toStatus(tk.status)
                  const pct = Math.min(100, Math.max(0, Number(tk.progress ?? 0)))
                  const terminal = status === 'success' || status === 'failed'
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
                          {tk.url}
                        </div>
                        {status === 'failed' && tk.error && (
                          <div className="mt-0.5 max-w-md truncate text-xs text-rose-300/80" title={tk.error}>
                            {tk.error}
                          </div>
                        )}
                      </td>
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-2">
                          <div className="h-2 flex-1 overflow-hidden rounded-full bg-white/10">
                            <div
                              className="h-full rounded-full bg-gradient-to-r from-indigo-500 to-cyan-400 transition-all"
                              style={{ width: `${pct}%` }}
                            />
                          </div>
                          <span className="w-12 text-right text-xs tabular-nums text-slate-400">
                            {pct.toFixed(0)}%
                          </span>
                        </div>
                      </td>
                      <td className="px-4 py-3">
                        <StatusBadge status={status} />
                      </td>
                      <td className="px-4 py-3 text-xs text-slate-400">{formatTime(tk.updatedAt || tk.createdAt)}</td>
                      <td className="px-4 py-3">
                        <div className="flex items-center justify-end gap-1">
                          {!terminal && (
                            <button
                              type="button"
                              onClick={() => act(tk.id, api.tasks.cancel, 'ns_mine.loadFailed')}
                              disabled={busyId === tk.id}
                              title={t('ns_admin.cancel')}
                              className="grid h-8 w-8 place-items-center rounded-lg text-slate-400 transition hover:bg-white/10 disabled:opacity-40"
                            >
                              <XCircle className="h-4 w-4" />
                            </button>
                          )}
                          {terminal && (
                            <button
                              type="button"
                              onClick={() => act(tk.id, api.tasks.retry, 'ns_mine.loadFailed')}
                              disabled={busyId === tk.id}
                              title={t('common.retry')}
                              className="grid h-8 w-8 place-items-center rounded-lg text-slate-400 transition hover:bg-white/10 disabled:opacity-40"
                            >
                              <RotateCcw className="h-4 w-4" />
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          )}
        </div>
      </div>

      {open && (
        <Modal title={t('ns_admin.createTask')} onClose={() => setOpen(false)}>
          <div className="space-y-4">
            <div className="flex gap-2">
              {(['http', 'bt'] as const).map((x) => (
                <button
                  key={x}
                  type="button"
                  onClick={() => setType(x)}
                  className={`flex flex-1 items-center justify-center gap-1.5 rounded-xl px-3 py-2 text-sm transition ${
                    type === x ? 'bg-nebula-500/30 text-white' : 'bg-white/5 text-slate-300 hover:bg-white/10'
                  }`}
                >
                  {x === 'bt' ? <Magnet className="h-4 w-4" /> : <LinkIcon className="h-4 w-4" />}
                  {x === 'bt' ? t('ns_admin.btTask') : t('ns_admin.httpTask')}
                </button>
              ))}
            </div>

            <label className="block">
              <span className="mb-1.5 block text-sm text-slate-300">{t('task.enterUrl')}</span>
              <input
                className="glass-input"
                value={form.url}
                onChange={(e) => setForm({ ...form, url: e.target.value })}
                placeholder={type === 'bt' ? 'magnet:?xt=... or .torrent URL' : 'https://'}
              />
            </label>

            <label className="block">
              <span className="mb-1.5 block text-sm text-slate-300">{t('task.targetDir')}</span>
              <input
                className="glass-input"
                value={form.parentId}
                onChange={(e) => setForm({ ...form, parentId: e.target.value })}
                placeholder={t('ns_mine.empty')}
              />
            </label>

            <div className="flex justify-end gap-2 pt-1">
              <button type="button" onClick={() => setOpen(false)} className="btn-ghost">
                {t('common.cancel')}
              </button>
              <button
                type="button"
                onClick={submit}
                disabled={saving || !form.url.trim()}
                className="btn-primary"
              >
                {saving && <Loader2 className="h-4 w-4 animate-spin" />}
                {t('common.save')}
              </button>
            </div>
          </div>
        </Modal>
      )}
    </MainLayout>
  )
}
