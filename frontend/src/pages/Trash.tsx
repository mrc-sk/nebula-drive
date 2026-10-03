import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import {
  AlertCircle,
  File as FileIcon,
  FileArchive,
  FileCode,
  FileText,
  Film,
  Folder,
  Image as ImageIcon,
  Info,
  Loader2,
  Music,
  RotateCcw,
  Trash2,
  X,
} from 'lucide-react'
import { api, type FileItem } from '../api/client'
import MainLayout from '../layouts/MainLayout'
import { Modal } from '../components/Modal'
import { formatSize } from '../utils/format'

function getFileKind(f: FileItem): string {
  const s = (f.mimeType || '').toLowerCase()
  if (s.startsWith('image/')) return 'image'
  if (s.startsWith('video/')) return 'video'
  if (s.startsWith('audio/')) return 'audio'
  if (s === 'application/pdf') return 'pdf'
  const e = (f.extension || f.name.split('.').pop() || '').toLowerCase()
  if (['zip', 'rar', '7z', 'tar', 'gz', 'bz2', 'xz'].includes(e)) return 'archive'
  if (['mp4', 'mkv', 'mov', 'avi', 'webm', 'flv', 'm4v'].includes(e)) return 'video'
  if (['mp3', 'flac', 'wav', 'aac', 'ogg', 'm4a'].includes(e)) return 'audio'
  if (e === 'pdf') return 'pdf'
  if (['txt', 'md', 'log'].includes(e)) return 'text'
  if (['js', 'ts', 'tsx', 'jsx', 'go', 'py', 'java', 'c', 'cpp', 'h', 'rs', 'json', 'yml', 'yaml', 'html', 'css'].includes(e))
    return 'code'
  return 'other'
}

function fileIcon(f: FileItem): { Icon: any; cls: string } {
  if (f.isDir) return { Icon: Folder, cls: 'text-nebula-300' }
  switch (getFileKind(f)) {
    case 'image': return { Icon: ImageIcon, cls: 'text-emerald-300' }
    case 'video': return { Icon: Film, cls: 'text-rose-300' }
    case 'audio': return { Icon: Music, cls: 'text-amber-300' }
    case 'pdf': return { Icon: FileText, cls: 'text-orange-300' }
    case 'archive': return { Icon: FileArchive, cls: 'text-yellow-300' }
    case 'text': return { Icon: FileText, cls: 'text-sky-300' }
    case 'code': return { Icon: FileCode, cls: 'text-cyan-glow' }
    default: return { Icon: FileIcon, cls: 'text-slate-300' }
  }
}

/**
 * 回收站（/trash）
 *
 * 侧边栏入口从未注册过路由，点击后被 App.tsx 的 catch-all 重定向回文件页。
 *
 * 后端没有独立的 /api/trash 分组，全部复用 /api/files：
 *   GET  /api/files?trash=1     列出该用户全部软删除文件
 *   POST /api/files/:id/restore 还原
 *   POST /api/files/:id/purge   彻底删除（释放配额 + 删物理文件）
 *
 * 两个必须让用户知道的行为（后端既有限制，不在 UI 上假装没有）：
 *  1. trash=1 时 parent 参数被忽略，返回的是**跨目录扁平列表**，不是某个目录的内容；
 *  2. Restore 每次都会新建一个「恢复的文件-YYYYMMDD-HHMMSS」目录把文件塞进去 ——
 *     批量还原 10 个会得到 10 个时间戳目录。
 *
 * 另外 File.DeletedAt 的 tag 是 json:"-"，所以列表拿不到删除时间，UI 不显示时间列。
 */
export default function Trash() {
  const { t } = useTranslation()
  const nav = useNavigate()

  const [rows, setRows] = useState<FileItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [busy, setBusy] = useState(false)
  const [confirm, setConfirm] = useState<{ mode: 'purge'; ids: number[] } | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const r = await api.trash.list()
      if (r?.code === 0) {
        setRows(r.data || [])
        setSelected(new Set())
      } else {
        setError(r?.message || t('ns_mine.loadFailed'))
      }
    } catch (e: any) {
      setError(e?.message || t('ns_mine.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    load()
  }, [load])

  const restore = async (ids: number[]) => {
    if (!ids.length) return
    setBusy(true)
    setError('')
    try {
      // 后端只有单条还原，没有批量接口 —— 只能串行循环。
      // 串行而非 Promise.all：每个 restore 都会建目录，并发会放大 SQLite 写竞争。
      for (const id of ids) {
        const r = await api.trash.restore(id)
        if (r?.code !== 0) {
          setError(r?.message || t('ns_mine.loadFailed'))
          break
        }
      }
      await load()
    } catch (e: any) {
      setError(e?.message || t('ns_mine.loadFailed'))
    } finally {
      setBusy(false)
    }
  }

  const purge = async (ids: number[]) => {
    if (!ids.length) return
    setBusy(true)
    setError('')
    try {
      for (const id of ids) {
        const r = await api.trash.purge(id)
        if (r?.code !== 0) {
          setError(r?.message || t('ns_mine.loadFailed'))
          break
        }
      }
      setConfirm(null)
      await load()
    } catch (e: any) {
      setError(e?.message || t('ns_mine.loadFailed'))
    } finally {
      setBusy(false)
    }
  }

  const toggle = (id: number) =>
    setSelected((cur) => {
      const next = new Set(cur)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  const allIds = rows.map((r) => r.id)
  const allSelected = allIds.length > 0 && allIds.every((id) => selected.has(id))

  const toolbar = useMemo(
    () => (
      <div className="mr-2 flex items-center gap-2">
        {selected.size > 0 && (
          <>
            <button
              type="button"
              onClick={() => restore(Array.from(selected))}
              disabled={busy}
              className="btn-ghost"
            >
              <RotateCcw className="h-4 w-4" />
              <span className="hidden sm:inline">{t('ns_mine.bulkRestore')}</span>
            </button>
            <button
              type="button"
              onClick={() => setConfirm({ mode: 'purge', ids: Array.from(selected) })}
              disabled={busy}
              className="btn-ghost"
            >
              <Trash2 className="h-4 w-4 text-rose-300" />
              <span className="hidden sm:inline text-rose-300">{t('ns_mine.bulkPurge')}</span>
            </button>
          </>
        )}
      </div>
    ),
    // restore 依赖 t 变化不大，busy/selected 直接读最新值即可
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [selected, busy, t],
  )

  return (
    <MainLayout
      breadcrumb={[{ name: t('files'), onClick: () => nav('/files') }, { name: t('recycle') }]}
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

        <div className="flex items-center gap-2 rounded-xl border border-amber-400/25 bg-amber-400/10 px-4 py-2 text-xs text-amber-200/90">
          <Info className="h-3.5 w-3.5 shrink-0" />
          <span>{t('ns_files.trashHint')}</span>
          <span className="hidden sm:inline">· {t('ns_mine.restoreHint')}</span>
        </div>

        <div className="glass flex-1 overflow-auto rounded-2xl">
          {loading ? (
            <div className="flex h-full min-h-[40vh] items-center justify-center">
              <Loader2 className="h-7 w-7 animate-spin text-nebula-300" />
            </div>
          ) : rows.length === 0 ? (
            <div className="flex h-full min-h-[40vh] flex-col items-center justify-center gap-3 text-slate-400">
              <div className="grid h-16 w-16 place-items-center rounded-2xl bg-white/5">
                <Trash2 className="h-8 w-8" />
              </div>
              <p className="text-sm">{t('ns_mine.emptyTrash')}</p>
            </div>
          ) : (
            <>
              <div className="sticky top-0 z-10 flex items-center gap-3 border-b border-white/10 bg-black/30 px-4 py-2.5 text-xs text-slate-400 backdrop-blur">
                <input
                  type="checkbox"
                  checked={allSelected}
                  onChange={() => setSelected(allSelected ? new Set() : new Set(allIds))}
                  className="h-4 w-4 accent-nebula-500"
                />
                <span>
                  {selected.size} / {rows.length}
                </span>
              </div>
              <div className="divide-y divide-white/5">
                {rows.map((f) => {
                  const { Icon, cls } = fileIcon(f)
                  const on = selected.has(f.id)
                  return (
                    <div
                      key={f.id}
                      className={`flex items-center gap-3 px-4 py-2.5 transition ${on ? 'bg-white/5' : 'hover:bg-white/5'}`}
                    >
                      <input
                        type="checkbox"
                        checked={on}
                        onChange={() => toggle(f.id)}
                        className="h-4 w-4 shrink-0 accent-nebula-500"
                      />
                      <Icon className={`h-4 w-4 shrink-0 ${cls}`} />
                      <span className="min-w-0 flex-1 truncate text-slate-200">{f.name}</span>
                      <span className="shrink-0 text-xs tabular-nums text-slate-400">
                        {f.isDir ? '/' : formatSize(f.size)}
                      </span>
                      <div className="flex shrink-0 items-center gap-1">
                        <button
                          type="button"
                          onClick={() => restore([f.id])}
                          disabled={busy}
                          title={t('restore')}
                          className="grid h-8 w-8 place-items-center rounded-lg text-slate-400 transition hover:bg-white/10 hover:text-slate-100 disabled:opacity-40"
                        >
                          <RotateCcw className="h-4 w-4" />
                        </button>
                        <button
                          type="button"
                          onClick={() => setConfirm({ mode: 'purge', ids: [f.id] })}
                          disabled={busy}
                          title={t('purge')}
                          className="grid h-8 w-8 place-items-center rounded-lg text-slate-400 transition hover:bg-rose-500/20 hover:text-rose-300 disabled:opacity-40"
                        >
                          <Trash2 className="h-4 w-4" />
                        </button>
                      </div>
                    </div>
                  )
                })}
              </div>
            </>
          )}
        </div>
      </div>

      {confirm && (
        <Modal title={t('ns_mine.purgeConfirm')} onClose={() => setConfirm(null)}>
          <p className="text-sm text-slate-300">
            {t('ns_mine.purgeConfirm')} ({confirm.ids.length})
          </p>
          <div className="mt-5 flex justify-end gap-2">
            <button type="button" onClick={() => setConfirm(null)} className="btn-ghost">
              {t('common.cancel')}
            </button>
            <button
              type="button"
              onClick={() => purge(confirm.ids)}
              disabled={busy}
              className="btn-primary"
            >
              {busy && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('purge')}
            </button>
          </div>
        </Modal>
      )}
    </MainLayout>
  )
}
