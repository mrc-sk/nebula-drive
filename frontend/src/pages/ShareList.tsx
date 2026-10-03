import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { AlertCircle, Check, Copy, FolderOpen, Link2, Loader2, Share2, Trash2, X } from 'lucide-react'
import { api, type ShareItem } from '../api/client'
import MainLayout from '../layouts/MainLayout'
import { Modal } from '../components/Modal'
import { formatTime } from '../utils/format'

/**
 * 分享列表（/share-list）
 *
 * 侧边栏一直有这个入口，但路由从未注册过 —— 点击后落到 App.tsx 的 catch-all
 * `<Route path="*" element={<Navigate to="/" replace />} />`，被弹回文件页，
 * 表现为"点了没反应"。本页面补上这个缺失路由。
 *
 * 后端能力边界（不要为不存在的接口画按钮）：
 *   GET    /api/shares        列出「我创建的」分享（owner_id 过滤，无分页）
 *   DELETE /api/shares/:id    取消分享
 *   —— 没有「分享给我的」、没有编辑、没有统计图、没有批量删除。
 *
 * 注意：models.Share.Password 的 tag 是 json:"-"，后端永不返回密码，
 * 所以列表里无法显示密码；ExtractCode 是 json:"extractCode"，会返回，可以显示。
 */
export default function ShareList() {
  const { t } = useTranslation()
  const nav = useNavigate()

  const [rows, setRows] = useState<ShareItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [copiedId, setCopiedId] = useState<number | null>(null)
  const [pendingDelete, setPendingDelete] = useState<ShareItem | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const r = await api.shares.list()
      if (r?.code === 0) setRows(r.data || [])
      else setError(r?.message || t('ns_mine.loadFailed'))
    } catch (e: any) {
      setError(e?.message || t('ns_mine.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    load()
  }, [load])

  const shareLink = (id: number) => `${window.location.origin}/#/share/${id}`

  const copy = async (s: ShareItem) => {
    try {
      await navigator.clipboard.writeText(shareLink(s.id))
      setCopiedId(s.id)
      setTimeout(() => setCopiedId((cur) => (cur === s.id ? null : cur)), 2000)
    } catch (e: any) {
      setError(e?.message || t('ns_mine.loadFailed'))
    }
  }

  const doDelete = async () => {
    if (!pendingDelete) return
    const id = pendingDelete.id
    setBusyId(id)
    setError('')
    try {
      const r = await api.shares.remove(id)
      if (r?.code !== 0) {
        setError(r?.message || t('ns_mine.loadFailed'))
        return
      }
      setRows((cur) => cur.filter((x) => x.id !== id))
      setPendingDelete(null)
    } catch (e: any) {
      setError(e?.message || t('ns_mine.loadFailed'))
    } finally {
      setBusyId(null)
    }
  }

  // 已过期的分享单独标出来：后端 ListShares 不会过滤，只能前端判断
  const isExpired = (s: ShareItem) => !!s.expireAt && new Date(s.expireAt).getTime() < Date.now()

  const toolbar = useMemo(
    () => (
      <button type="button" onClick={load} className="btn-ghost mr-2" title={t('common.retry')}>
        <Loader2 className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} />
        <span className="hidden sm:inline">{t('common.retry')}</span>
      </button>
    ),
    [load, loading, t],
  )

  return (
    <MainLayout
      breadcrumb={[{ name: t('files'), onClick: () => nav('/files') }, { name: t('shares') }]}
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
          {loading ? (
            <div className="flex h-full min-h-[40vh] items-center justify-center">
              <Loader2 className="h-7 w-7 animate-spin text-nebula-300" />
            </div>
          ) : rows.length === 0 ? (
            <div className="flex h-full min-h-[40vh] flex-col items-center justify-center gap-3 text-slate-400">
              <div className="grid h-16 w-16 place-items-center rounded-2xl bg-white/5">
                <Share2 className="h-8 w-8" />
              </div>
              <p className="text-sm">{t('ns_mine.emptyShares')}</p>
            </div>
          ) : (
            <table className="min-w-full text-sm">
              <thead className="sticky top-0 z-10 border-b border-white/10 bg-black/30 text-left text-xs uppercase text-slate-400 backdrop-blur">
                <tr>
                  <th className="px-4 py-3">ID</th>
                  <th className="px-4 py-3">{t('ns_mine.fileRef')}</th>
                  <th className="px-4 py-3">{t('publicShare')}</th>
                  <th className="px-4 py-3">{t('expireTime')}</th>
                  <th className="px-4 py-3 text-right">{t('viewTimes')}</th>
                  <th className="px-4 py-3 text-right">{t('downloadTimes')}</th>
                  <th className="px-4 py-3 text-right">{t('ns_mine.actions')}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-white/5">
                {rows.map((s) => {
                  const expired = isExpired(s)
                  return (
                    <tr key={s.id} className="hover:bg-white/5">
                      <td className="px-4 py-3 text-slate-400">#{s.id}</td>
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-2 text-slate-200">
                          {s.isDir ? (
                            <FolderOpen className="h-4 w-4 shrink-0 text-amber-300" />
                          ) : (
                            <Link2 className="h-4 w-4 shrink-0 text-nebula-300" />
                          )}
                          {/* 后端只回 fileId 不回文件名，只能显示占位 —— 真实文件名需另查文件接口 */}
                          <span className="truncate">
                            {t('ns_mine.fileRef')} #{s.fileId}
                            {s.isDir ? ' /' : ''}
                          </span>
                        </div>
                      </td>
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-2">
                          <code className="max-w-[16rem] truncate rounded bg-white/5 px-2 py-1 text-xs text-slate-300">
                            {shareLink(s.id)}
                          </code>
                          <button
                            type="button"
                            onClick={() => copy(s)}
                            title={t('copyLink')}
                            className="grid h-7 w-7 shrink-0 place-items-center rounded-lg text-slate-400 transition hover:bg-white/10 hover:text-slate-100"
                          >
                            {copiedId === s.id ? <Check className="h-3.5 w-3.5 text-emerald-400" /> : <Copy className="h-3.5 w-3.5" />}
                          </button>
                        </div>
                        {s.extractCode && (
                          <div className="mt-1 text-xs text-cyan-glow/80">
                            {t('ns_mine.extractCode')}: {s.extractCode}
                          </div>
                        )}
                      </td>
                      <td className="px-4 py-3">
                        {s.expireAt ? (
                          <span className={expired ? 'text-rose-300' : 'text-slate-300'}>
                            {formatTime(s.expireAt)}
                            {expired ? ` · ${t('ns_mine.expired')}` : ''}
                          </span>
                        ) : (
                          <span className="text-slate-500">{t('ns_mine.neverExpires')}</span>
                        )}
                      </td>
                      <td className="px-4 py-3 text-right tabular-nums text-slate-300">{s.views ?? 0}</td>
                      <td className="px-4 py-3 text-right tabular-nums text-slate-300">{s.downloads ?? 0}</td>
                      <td className="px-4 py-3">
                        <div className="flex items-center justify-end gap-1">
                          <button
                            type="button"
                            onClick={() => setPendingDelete(s)}
                            disabled={busyId === s.id}
                            title={t('ns_admin.cancel')}
                            className="grid h-8 w-8 place-items-center rounded-lg text-slate-400 transition hover:bg-rose-500/20 hover:text-rose-300 disabled:opacity-40"
                          >
                            <Trash2 className="h-4 w-4" />
                          </button>
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

      {pendingDelete && (
        <Modal title={t('ns_mine.removeShareConfirm')} onClose={() => setPendingDelete(null)}>
          <p className="text-sm text-slate-300">
            {t('ns_mine.removeShareConfirm')} #{pendingDelete.id}
          </p>
          <div className="mt-5 flex justify-end gap-2">
            <button type="button" onClick={() => setPendingDelete(null)} className="btn-ghost">
              {t('common.cancel')}
            </button>
            <button
              type="button"
              onClick={doDelete}
              disabled={busyId === pendingDelete.id}
              className="btn-primary"
            >
              {busyId === pendingDelete.id && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('common.save')}
            </button>
          </div>
        </Modal>
      )}
    </MainLayout>
  )
}
