import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  FolderPlus, Upload, Trash2, List, LayoutGrid, Folder, FileText, Image as ImageIcon, Film, Music,
  FileArchive, FileCode, File, Download, Eye, Pencil, FolderInput, Share2, RotateCcw,
  X, Loader2, AlertCircle, Check, Copy, ArrowLeft, ChevronDown, ChevronRight, ArrowUpDown,
  CheckSquare, Square, MoreHorizontal, Lock, KeyRound,
  Tag as TagIcon, History, Layers, FolderSync, Crown, UploadCloud, Filter,
} from 'lucide-react'
import {
  api, computeMD5, fetchBlob, uploadWithProgress, type FileItem, type FileTag, type FileVersion,
} from '../api/client'
import MainLayout from '../layouts/MainLayout'
import { useUserPrefsStore, type SelectMode } from '../store/user'
import { useAuthStore } from '../store/auth'
import VideoPlayer from '../components/preview/VideoPlayer'
import AudioPlayer from '../components/preview/AudioPlayer'
import OfficePreview from '../components/preview/OfficePreview'

/* ============================== 标签预设色 ============================== */
const TAG_COLORS = [
  { name: 'blue', value: '#3b82f6' },
  { name: 'green', value: '#10b981' },
  { name: 'amber', value: '#f59e0b' },
  { name: 'rose', value: '#f43f5e' },
  { name: 'purple', value: '#a855f7' },
  { name: 'cyan', value: '#22d3ee' },
]

/* ============================== 工具函数 ============================== */

function formatSize(bytes: number): string {
  if (!bytes || bytes < 0) return '0 B'
  const k = 1024
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(k)))
  return `${(bytes / Math.pow(k, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

function formatTime(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

type FileKind = 'image' | 'video' | 'audio' | 'pdf' | 'office' | 'archive' | 'text' | 'code' | 'file'
type PreviewKind = 'image' | 'video' | 'audio' | 'pdf' | 'office' | 'text' | 'none'

function getFileKind(f: FileItem): FileKind {
  if (f.isDir) return 'file'
  const ext = (f.extension || '').toLowerCase()
  const mt = (f.mimeType || '').toLowerCase()
  if (mt.startsWith('image/') || ['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp', 'ico', 'avif'].includes(ext)) return 'image'
  if (mt.startsWith('video/') || ['mp4', 'mkv', 'mov', 'avi', 'webm', 'flv', 'wmv', 'm4v'].includes(ext)) return 'video'
  if (mt.startsWith('audio/') || ['mp3', 'wav', 'flac', 'aac', 'ogg', 'm4a', 'opus'].includes(ext)) return 'audio'
  if (ext === 'pdf' || mt === 'application/pdf') return 'pdf'
  if (['docx', 'xlsx', 'xls', 'pptx'].includes(ext)) return 'office'
  if (['zip', 'rar', '7z', 'tar', 'gz', 'bz2', 'xz', 'iso'].includes(ext)) return 'archive'
  if (['txt', 'md', 'log', 'csv', 'json', 'xml', 'yml', 'yaml', 'ini', 'conf'].includes(ext)) return 'text'
  if (['js', 'ts', 'jsx', 'tsx', 'go', 'py', 'java', 'c', 'cpp', 'h', 'rs', 'rb', 'php', 'sh', 'html', 'css', 'vue'].includes(ext)) return 'code'
  return 'file'
}

function getPreviewKind(f: FileItem): PreviewKind {
  const k = getFileKind(f)
  if (k === 'image') return 'image'
  if (k === 'video') return 'video'
  if (k === 'audio') return 'audio'
  if (k === 'pdf') return 'pdf'
  if (k === 'office') return 'office'
  if (k === 'text' || k === 'code') return 'text'
  return 'none'
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
    default: return { Icon: File, cls: 'text-slate-300' }
  }
}

interface Row { file: FileItem; prefix?: string }
type ModalState =
  | { type: 'none' }
  | { type: 'mkdir' }
  | { type: 'rename'; file: FileItem }
  | { type: 'move'; file: FileItem }
  | { type: 'share'; file: FileItem }
  | { type: 'preview'; file: FileItem }
  | { type: 'delete'; file: FileItem }
  | { type: 'purge'; file: FileItem }
  | { type: 'addTag'; file: FileItem }
  | { type: 'batchMove'; ids: number[] }
  | { type: 'batchCopy'; ids: number[] }
  | { type: 'batchPolicy'; ids: number[] }
  | { type: 'batchDelete'; ids: number[] }

interface UploadTask {
  id: string
  name: string
  size: number
  status: 'hashing' | 'uploading' | 'rapid' | 'done' | 'error'
  progress: number
  error?: string
}
interface DownloadTask {
  id: string
  name: string
  size: number
  status: 'downloading' | 'done' | 'error'
  progress: number
  error?: string
}

function Modal({ title, onClose, children, wide }: { title: string; onClose: () => void; children: React.ReactNode; wide?: boolean }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm animate-fade-in" onClick={onClose}>
      <div className={`glass-strong animate-slide-up w-full overflow-hidden rounded-2xl ${wide ? 'max-w-4xl' : 'max-w-md'}`} onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between border-b border-white/10 px-5 py-3.5">
          <h3 className="text-sm font-semibold">{title}</h3>
          <button type="button" onClick={onClose} className="grid h-7 w-7 place-items-center rounded-lg text-slate-400 transition hover:bg-white/10 hover:text-slate-100">
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="p-5">{children}</div>
      </div>
    </div>
  )
}

function NamePromptModal({ title, label, initial, onSubmit, onClose }: any) {
  const { t } = useTranslation()
  const [name, setName] = useState(initial || '')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const submit = async () => {
    if (!name.trim()) { setErr(t('ns_files.enterName')); return }
    setBusy(true); setErr('')
    try { await onSubmit(name.trim()); onClose() } catch (e: any) { setErr(e?.message || 'error'); setBusy(false) }
  }
  return (
    <Modal title={title} onClose={onClose}>
      <label className="block">
        <span className="mb-1.5 block text-xs text-slate-300">{label}</span>
        <input className="glass-input" value={name} autoFocus onChange={(e) => setName(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && submit()} />
      </label>
      {err && (
        <div className="mt-3 flex items-center gap-2 rounded-xl border border-rose-400/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
          <AlertCircle className="h-3.5 w-3.5 shrink-0" /><span>{err}</span>
        </div>
      )}
      <div className="mt-4 flex justify-end gap-2">
        <button type="button" className="btn-ghost" onClick={onClose}>{t('cancel')}</button>
        <button type="button" className="btn-primary" onClick={submit} disabled={busy}>
          {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Check className="h-4 w-4" />}{t('save')}
        </button>
      </div>
    </Modal>
  )
}

function DirRow({ dir, depth, selected, onSelect, excludeId, allOpen }: any) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(depth < 1 || !!allOpen)
  const [children, setChildren] = useState<FileItem[]>([])
  const [loaded, setLoaded] = useState(false)
  const id = dir ? dir.id : null
  const name = dir ? dir.name : t('ns_files.root')
  const excluded = excludeId != null && id === excludeId

  useEffect(() => { if (allOpen != null) setOpen(allOpen) }, [allOpen])

  useEffect(() => {
    if (!open || loaded) return
    let alive = true
    api.files.list(id).then((r) => {
      if (!alive) return
      setChildren((r.data || []).filter((f: any) => f.isDir))
      setLoaded(true)
    }).catch(() => { if (alive) setLoaded(true) })
    return () => { alive = false }
  }, [open, loaded, id])

  return (
    <div>
      <div className="flex items-center gap-1 rounded-lg py-0.5" style={{ paddingLeft: depth * 14 }}>
        <button type="button" onClick={() => setOpen((o: boolean) => !o)} className="grid h-5 w-5 shrink-0 place-items-center rounded text-slate-400 hover:bg-white/10">
          {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
        </button>
        <button type="button" disabled={excluded} onClick={() => onSelect(id)} className={`flex flex-1 items-center gap-2 rounded-lg px-2 py-1 text-sm transition disabled:cursor-not-allowed disabled:opacity-40 ${selected === id ? 'bg-nebula-500/30 text-white' : 'text-slate-200 hover:bg-white/10'}`}>
          <Folder className="h-4 w-4 shrink-0 text-nebula-300" /><span className="truncate">{name}</span>
        </button>
      </div>
      {open && loaded && children.map((c: any) => <DirRow key={c.id} dir={c} depth={depth + 1} selected={selected} onSelect={onSelect} excludeId={excludeId} allOpen={allOpen} />)}
    </div>
  )
}

function MoveModal({ file, onMoved, onClose }: any) {
  const { t } = useTranslation()
  const [target, setTarget] = useState<number | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const submit = async () => {
    setBusy(true); setErr('')
    try { await api.files.move(file.id, target); onMoved() } catch (e: any) { setErr(e?.message || 'error'); setBusy(false) }
  }
  return (
    <Modal title={t('ns_files.moveTitle')} onClose={onClose}>
      <div className="max-h-72 overflow-auto rounded-xl bg-white/5 p-2">
        <DirRow dir={null} depth={0} selected={target} onSelect={setTarget} excludeId={file.isDir ? file.id : undefined} />
      </div>
      {err && <div className="mt-3 flex items-center gap-2 rounded-xl border border-rose-400/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300"><AlertCircle className="h-3.5 w-3.5" /><span>{err}</span></div>}
      <div className="mt-4 flex justify-end gap-2">
        <button type="button" className="btn-ghost" onClick={onClose}>{t('cancel')}</button>
        <button type="button" className="btn-primary" onClick={submit} disabled={busy}>
          {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <FolderInput className="h-4 w-4" />}{t('move')}
        </button>
      </div>
    </Modal>
  )
}

/* ----- 添加标签弹窗（毛玻璃） ----- */
function AddTagModal({ file, onDone, onClose }: { file: FileItem; onDone: () => void; onClose: () => void }) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [color, setColor] = useState(TAG_COLORS[0].value)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const submit = async () => {
    if (!name.trim()) { setErr(t('tagName')); return }
    setBusy(true); setErr('')
    try {
      await api.files.addTag(file.id, name.trim(), color)
      onDone()
    } catch (e: any) {
      setErr(e?.message || 'error'); setBusy(false)
    }
  }
  return (
    <Modal title={t('addTag')} onClose={onClose}>
      <label className="block">
        <span className="mb-1.5 block text-xs text-slate-300">{t('tagName')}</span>
        <input className="glass-input" value={name} autoFocus onChange={(e) => setName(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && submit()} />
      </label>
      <div className="mt-3">
        <span className="mb-1.5 block text-xs text-slate-300">{t('tagColor')}</span>
        <div className="flex flex-wrap gap-2">
          {TAG_COLORS.map((c) => (
            <button
              key={c.name}
              type="button"
              onClick={() => setColor(c.value)}
              className={`h-8 w-8 rounded-full transition ${color === c.value ? 'ring-2 ring-white ring-offset-2 ring-offset-black/50' : 'hover:scale-110'}`}
              style={{ backgroundColor: c.value }}
              title={c.name}
            />
          ))}
        </div>
      </div>
      {err && (
        <div className="mt-3 flex items-center gap-2 rounded-xl border border-rose-400/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
          <AlertCircle className="h-3.5 w-3.5 shrink-0" /><span>{err}</span>
        </div>
      )}
      <div className="mt-4 flex justify-end gap-2">
        <button type="button" className="btn-ghost" onClick={onClose}>{t('cancel')}</button>
        <button type="button" className="btn-primary" onClick={submit} disabled={busy}>
          {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <TagIcon className="h-4 w-4" />}{t('save')}
        </button>
      </div>
    </Modal>
  )
}

/* ----- 批量移动/复制目录选择器 ----- */
function DirPickerModal({ title, confirmText, ids, mode, onDone, onClose }: {
  title: string
  confirmText: string
  ids: number[]
  mode: 'move' | 'copy'
  onDone: () => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [target, setTarget] = useState<number | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const submit = async () => {
    setBusy(true); setErr('')
    try {
      if (mode === 'move') await api.files.batchMove(ids, target)
      else await api.files.batchCopy(ids, target)
      onDone()
    } catch (e: any) {
      setErr(e?.message || 'error'); setBusy(false)
    }
  }
  return (
    <Modal title={`${title} (${ids.length})`} onClose={onClose}>
      <div className="mb-2 text-xs text-slate-400">{t('selectTargetDir')}</div>
      <div className="max-h-72 overflow-auto rounded-xl bg-white/5 p-2">
        <DirRow dir={null} depth={0} selected={target} onSelect={setTarget} />
      </div>
      {err && <div className="mt-3 flex items-center gap-2 rounded-xl border border-rose-400/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300"><AlertCircle className="h-3.5 w-3.5" /><span>{err}</span></div>}
      <div className="mt-4 flex justify-end gap-2">
        <button type="button" className="btn-ghost" onClick={onClose}>{t('cancel')}</button>
        <button type="button" className="btn-primary" onClick={submit} disabled={busy}>
          {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : mode === 'move' ? <FolderInput className="h-4 w-4" /> : <FolderSync className="h-4 w-4" />}{confirmText}
        </button>
      </div>
    </Modal>
  )
}

/* ----- 批量改存储策略 ----- */
function BatchPolicyModal({ ids, onDone, onClose }: { ids: number[]; onDone: () => void; onClose: () => void }) {
  const { t } = useTranslation()
  const [policies, setPolicies] = useState<any[]>([])
  const [policyId, setPolicyId] = useState<number | ''>('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  useEffect(() => {
    let alive = true
    api.admin.policies().then((r) => {
      if (alive && r?.code === 0 && Array.isArray(r.data)) setPolicies(r.data)
    }).catch(() => { if (alive) setPolicies([]) })
    return () => { alive = false }
  }, [])
  const submit = async () => {
    if (policyId === '') { setErr(t('selectPolicy')); return }
    setBusy(true); setErr('')
    try {
      await api.files.batchPolicy(ids, Number(policyId))
      onDone()
    } catch (e: any) {
      setErr(e?.message || 'error'); setBusy(false)
    }
  }
  return (
    <Modal title={`${t('batchPolicy')} (${ids.length})`} onClose={onClose}>
      <div className="mb-2 text-xs text-slate-400">{t('selectPolicy')}</div>
      <select className="glass-input" value={policyId} onChange={(e) => setPolicyId(e.target.value ? Number(e.target.value) : '')}>
        <option value="">--</option>
        {policies.map((p: any) => (
          <option key={p.id} value={p.id}>{p.name || p.type || `#${p.id}`}</option>
        ))}
      </select>
      {policies.length === 0 && <div className="mt-2 text-[11px] text-slate-500">—</div>}
      {err && <div className="mt-3 flex items-center gap-2 rounded-xl border border-rose-400/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300"><AlertCircle className="h-3.5 w-3.5" /><span>{err}</span></div>}
      <div className="mt-4 flex justify-end gap-2">
        <button type="button" className="btn-ghost" onClick={onClose}>{t('cancel')}</button>
        <button type="button" className="btn-primary" onClick={submit} disabled={busy}>
          {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Layers className="h-4 w-4" />}{t('save')}
        </button>
      </div>
    </Modal>
  )
}

/* ----- 批量删除（输入密码确认） ----- */
function BatchDeleteModal({ ids, onDone, onClose }: { ids: number[]; onDone: () => void; onClose: () => void }) {
  const { t } = useTranslation()
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const submit = async () => {
    if (!password) { setErr(t('confirmDeletePassword')); return }
    setBusy(true); setErr('')
    try {
      await api.files.batchDelete(ids, password)
      onDone()
    } catch (e: any) {
      setErr(e?.message || 'error'); setBusy(false)
    }
  }
  return (
    <Modal title={`${t('batchDelete')} (${ids.length})`} onClose={onClose}>
      <p className="text-sm text-slate-200">{t('confirmDeletePassword')}</p>
      <input
        type="password"
        className="glass-input mt-3"
        placeholder={t('password')}
        value={password}
        autoFocus
        onChange={(e) => setPassword(e.target.value)}
        onKeyDown={(e) => e.key === 'Enter' && submit()}
      />
      {err && <div className="mt-3 flex items-center gap-2 rounded-xl border border-rose-400/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300"><AlertCircle className="h-3.5 w-3.5" /><span>{err}</span></div>}
      <div className="mt-4 flex justify-end gap-2">
        <button type="button" className="btn-ghost" onClick={onClose}>{t('cancel')}</button>
        <button type="button" className="btn-primary !bg-gradient-to-r !from-rose-500 !to-rose-600" onClick={submit} disabled={busy}>
          {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Trash2 className="h-4 w-4" />}{t('delete')}
        </button>
      </div>
    </Modal>
  )
}

function ShareModal({ file, onClose }: any) {
  const { t } = useTranslation()
  const [usePwd, setUsePwd] = useState(false)
  const [useExtract, setUseExtract] = useState(true)
  const [password, setPassword] = useState('')
  const [extractCode, setExtractCode] = useState('')
  const [expireAt, setExpireAt] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [link, setLink] = useState('')
  const [copied, setCopied] = useState(false)

  const genRandomPwd = () => {
    const chars = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%'
    let s = ''
    for (let i = 0; i < 8; i++) s += chars[Math.floor(Math.random() * chars.length)]
    setPassword(s)
  }
  const genRandomExtract = () => {
    const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZ23456789'
    let s = ''
    for (let i = 0; i < 4; i++) s += chars[Math.floor(Math.random() * chars.length)]
    setExtractCode(s)
  }

  const submit = async () => {
    setBusy(true); setErr('')
    try {
      const r = await api.shares.create(file.id, usePwd ? password : undefined, expireAt, useExtract ? extractCode : undefined)
      if (r.code === 0 && r.data) setLink(`${location.origin}/#/share/${r.data.id}`)
      else setErr(r.message || 'error')
    } catch (e: any) { setErr(e?.message || 'error') } finally { setBusy(false) }
  }
  const copy = () => { navigator.clipboard?.writeText(link); setCopied(true); setTimeout(() => setCopied(false), 1500) }
  if (link) {
    return (
      <Modal title={t('share')} onClose={onClose}>
        <p className="text-sm text-slate-300">{t('share')}</p>
        <div className="mt-2 flex gap-2">
          <input className="glass-input" value={link} readOnly onFocus={(e) => e.target.select()} />
          <button type="button" className="btn-ghost shrink-0" onClick={copy}>
            {copied ? <Check className="h-4 w-4 text-emerald-300" /> : <Copy className="h-4 w-4" />}{t('copyLink')}
          </button>
        </div>
        <div className="mt-4 flex justify-end"><button type="button" className="btn-primary" onClick={onClose}>{t('ok')}</button></div>
      </Modal>
    )
  }
  return (
    <Modal title={t('share')} onClose={onClose}>
      <label className="flex items-center justify-between rounded-xl bg-white/5 px-3 py-2.5">
        <span className="flex items-center gap-2 text-sm">
          <Lock className="h-4 w-4 text-amber-300" />
          启用访问密码
        </span>
        <input type="checkbox" className="h-4 w-4 accent-nebula-500" checked={usePwd} onChange={(e) => setUsePwd(e.target.checked)} />
      </label>
      {usePwd && (
        <div className="mt-2 space-y-2">
          <div className="flex gap-2">
            <input
              className="glass-input flex-1"
              type="password"
              placeholder="请输入访问密码"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <button type="button" onClick={genRandomPwd} className="btn-ghost shrink-0 !px-3 text-xs">
              随机生成
            </button>
          </div>
          {password && (
            <div className="text-[11px] text-slate-400 break-all">密码: <span className="text-slate-200">{password}</span></div>
          )}
        </div>
      )}

      <label className="mt-3 flex items-center justify-between rounded-xl bg-white/5 px-3 py-2.5">
        <span className="flex items-center gap-2 text-sm">
          <KeyRound className="h-4 w-4 text-cyan-300" />
          启用提取码
        </span>
        <input type="checkbox" className="h-4 w-4 accent-nebula-500" checked={useExtract} onChange={(e) => setUseExtract(e.target.checked)} />
      </label>
      {useExtract && (
        <div className="mt-2 space-y-2">
          <div className="flex gap-2">
            <input
              className="glass-input flex-1 text-center text-lg font-bold tracking-[0.6em] uppercase"
              maxLength={4}
              placeholder="0000"
              value={extractCode}
              onChange={(e) => setExtractCode(e.target.value.replace(/[^0-9a-zA-Z]/g, '').slice(0, 4))}
            />
            <button type="button" onClick={genRandomExtract} className="btn-ghost shrink-0 !px-3 text-xs">
              随机生成
            </button>
          </div>
          {extractCode && (
            <div className="text-[11px] text-slate-400 break-all">提取码: <span className="font-bold tracking-widest text-cyan-300">{extractCode}</span></div>
          )}
        </div>
      )}

      <label className="mt-3 block">
        <span className="mb-1.5 block text-xs text-slate-300">{t('expireTime')}（可选）</span>
        <input className="glass-input" type="datetime-local" value={expireAt} onChange={(e) => setExpireAt(e.target.value)} />
      </label>
      <div className="mt-2 truncate text-xs text-slate-400">{file.name}</div>
      {err && <div className="mt-3 flex items-center gap-2 rounded-xl border border-rose-400/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300"><AlertCircle className="h-3.5 w-3.5" /><span>{err}</span></div>}
      <div className="mt-4 flex justify-end gap-2">
        <button type="button" className="btn-ghost" onClick={onClose}>{t('cancel')}</button>
        <button type="button" className="btn-primary" onClick={submit} disabled={busy}>
          {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Share2 className="h-4 w-4" />}{t('share')}
        </button>
      </div>
    </Modal>
  )
}

function PreviewModal({ file, onClose, onDownload }: { file: FileItem; onClose: () => void; onDownload?: () => void }) {
  const { t } = useTranslation()
  const kind = getPreviewKind(file)
  const [url, setUrl] = useState<string | null>(null)
  const [text, setText] = useState<string | null>(null)
  const [err, setErr] = useState('')
  const [tab, setTab] = useState<'preview' | 'versions'>('preview')
  useEffect(() => {
    let obj: string | null = null
    let alive = true
    setUrl(null); setText(null); setErr('')
    if (kind === 'none') return
    const sourceURL = kind === 'office' ? api.files.downloadURL(file.id) : api.files.previewURL(file.id)
    fetchBlob(sourceURL).then(async (b) => {
      if (!alive) return
      if (kind === 'text') setText(await b.text())
      else { obj = URL.createObjectURL(b); setUrl(obj) }
    }).catch((e: any) => { if (alive) setErr(e?.message || 'error') })
    return () => { alive = false; if (obj) URL.revokeObjectURL(obj) }
  }, [file.id, kind])
  return (
    <Modal title={`${t('preview')} · ${file.name}`} onClose={onClose} wide>
      <div className="mb-3 flex items-center gap-1 rounded-xl bg-white/5 p-1">
        <button
          type="button"
          onClick={() => setTab('preview')}
          className={`flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs transition ${tab === 'preview' ? 'bg-nebula-500/30 text-white' : 'text-slate-300 hover:bg-white/10'}`}
        >
          <Eye className="h-3.5 w-3.5" />{t('preview')}
        </button>
        {!file.isDir && (
          <button
            type="button"
            onClick={() => setTab('versions')}
            className={`flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs transition ${tab === 'versions' ? 'bg-nebula-500/30 text-white' : 'text-slate-300 hover:bg-white/10'}`}
          >
            <History className="h-3.5 w-3.5" />{t('versionHistory')}
            {typeof file.versionCount === 'number' && file.versionCount > 0 && (
              <span className="grid h-4 min-w-4 place-items-center rounded-full bg-cyan-glow/30 px-1 text-[10px] text-white">{file.versionCount}</span>
            )}
          </button>
        )}
      </div>
      {tab === 'versions' ? (
        <VersionHistoryPanel file={file} />
      ) : (
        <div className="flex max-h-[70vh] min-h-[200px] items-center justify-center overflow-auto">
          {kind === 'none' ? (
            <div className="flex flex-col items-center gap-3 py-10 text-slate-400">
              <File className="h-10 w-10" />
              <p className="text-sm">{t('ns_files.unsupportedPreview')}</p>
              {onDownload && (
                <button type="button" className="btn-primary" onClick={onDownload}>
                  <Download className="h-4 w-4" />{t('download')}
                </button>
              )}
            </div>
          ) : err ? <div className="flex items-center gap-2 text-sm text-rose-300"><AlertCircle className="h-4 w-4" />{err}</div>
            : (kind === 'image' || kind === 'video' || kind === 'audio' || kind === 'pdf' || kind === 'office') && !url ? <Loader2 className="h-7 w-7 animate-spin text-nebula-300" />
            : kind === 'text' && text === null ? <Loader2 className="h-7 w-7 animate-spin text-nebula-300" />
            : kind === 'image' ? <img src={url || undefined} alt={file.name} className="max-h-[70vh] max-w-full rounded-xl" />
            : kind === 'video' ? <VideoPlayer src={url || ''} mimeType={file.mimeType} />
            : kind === 'audio' ? <AudioPlayer src={url || ''} mimeType={file.mimeType} />
            : kind === 'office' ? <OfficePreview fileUrl={url || ''} fileExtension={file.extension} fileName={file.name} />
            : kind === 'pdf' ? <iframe src={url || undefined} title={file.name} className="h-[70vh] w-full rounded-xl" />
            : <pre className="max-h-[70vh] w-full overflow-auto whitespace-pre-wrap break-words rounded-xl bg-black/20 p-4 font-mono text-xs leading-relaxed text-cyan-100">{text}</pre>}
        </div>
      )}
    </Modal>
  )
}

/* ----- 版本历史面板 ----- */
function VersionHistoryPanel({ file }: { file: FileItem }) {
  const { t } = useTranslation()
  const [versions, setVersions] = useState<FileVersion[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [busyId, setBusyId] = useState<number | null>(null)
  const [msg, setMsg] = useState<{ type: 'ok' | 'err'; text: string } | null>(null)
  const versionInputRef = useRef<HTMLInputElement>(null)

  const load = useCallback(async () => {
    setLoading(true); setErr('')
    try {
      const r = await api.files.listVersions(file.id)
      if (r?.code === 0 && Array.isArray(r.data)) setVersions(r.data)
      else setErr(r?.message || 'error')
    } catch (e: any) {
      setErr(e?.message || 'error')
    } finally {
      setLoading(false)
    }
  }, [file.id])

  useEffect(() => { load() }, [load])

  const restore = async (v: FileVersion) => {
    setBusyId(v.id); setMsg(null)
    try {
      await api.files.restoreVersion(file.id, v.id)
      setMsg({ type: 'ok', text: `${t('restoreVersion')} v${v.version} OK` })
      await load()
    } catch (e: any) {
      setMsg({ type: 'err', text: e?.message || 'error' })
    } finally {
      setBusyId(null)
    }
  }

  const remove = async (v: FileVersion) => {
    setBusyId(v.id); setMsg(null)
    try {
      await api.files.deleteVersion(file.id, v.id)
      setMsg({ type: 'ok', text: `${t('deleteVersion')} v${v.version} OK` })
      await load()
    } catch (e: any) {
      setMsg({ type: 'err', text: e?.message || 'error' })
    } finally {
      setBusyId(null)
    }
  }

  const downloadVersion = async (v: FileVersion) => {
    try {
      const blob = await fetchBlob(api.files.downloadVersionURL(file.id, v.id))
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `${file.name}.v${v.version}`
      document.body.appendChild(a); a.click(); a.remove()
      setTimeout(() => URL.revokeObjectURL(url), 1500)
    } catch (e: any) {
      setMsg({ type: 'err', text: e?.message || 'error' })
    }
  }

  const onUploadVersion = (f?: File) => {
    if (!f) return
    setBusyId(-1); setMsg(null)
    const xhr = new XMLHttpRequest()
    const fd = new FormData()
    fd.append('file', f)
    xhr.open('POST', api.files.uploadVersionURL(file.id))
    const token = localStorage.getItem('nebula_token')
    if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`)
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        setMsg({ type: 'ok', text: `${t('uploadNewVersion')} OK` })
        load().catch(() => {})
      } else {
        setMsg({ type: 'err', text: xhr.statusText || `upload failed (${xhr.status})` })
      }
      setBusyId(null)
      if (versionInputRef.current) versionInputRef.current.value = ''
    }
    xhr.onerror = () => {
      setMsg({ type: 'err', text: 'upload network error' })
      setBusyId(null)
      if (versionInputRef.current) versionInputRef.current.value = ''
    }
    xhr.send(fd)
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <div className="text-xs text-slate-400">{t('versionHistory')}</div>
        <button
          type="button"
          onClick={() => versionInputRef.current?.click()}
          disabled={busyId !== null}
          className="btn-ghost py-1.5 text-xs"
        >
          {busyId === -1 ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <UploadCloud className="h-3.5 w-3.5" />}
          {t('uploadNewVersion')}
        </button>
        <input
          ref={versionInputRef}
          type="file"
          className="hidden"
          onChange={(e) => onUploadVersion(e.target.files?.[0])}
        />
      </div>

      {msg && (
        <div className={`flex items-center gap-2 rounded-lg px-3 py-2 text-xs ${msg.type === 'ok' ? 'border border-emerald-400/30 bg-emerald-500/10 text-emerald-300' : 'border border-rose-400/30 bg-rose-500/10 text-rose-300'}`}>
          {msg.type === 'ok' ? <Check className="h-3.5 w-3.5" /> : <AlertCircle className="h-3.5 w-3.5" />}
          <span>{msg.text}</span>
        </div>
      )}

      {loading ? (
        <div className="grid place-items-center py-8"><Loader2 className="h-6 w-6 animate-spin text-nebula-300" /></div>
      ) : err ? (
        <div className="flex items-center gap-2 rounded-xl border border-rose-400/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
          <AlertCircle className="h-3.5 w-3.5" /><span>{err}</span>
        </div>
      ) : versions.length === 0 ? (
        <div className="py-8 text-center text-sm text-slate-400">—</div>
      ) : (
        <div className="max-h-[55vh] space-y-2 overflow-auto">
          {versions.map((v) => (
            <div key={v.id} className="flex flex-wrap items-center gap-3 rounded-xl border border-white/10 bg-white/5 px-3 py-2.5">
              <div className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-gradient-to-br from-nebula-500/40 to-cyan-glow/30 text-xs font-bold text-white">
                v{v.version}
              </div>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-x-3 text-sm text-slate-100">
                  <span className="font-medium">{t('version')} {v.version}</span>
                  <span className="text-xs text-slate-400">{formatSize(v.size)}</span>
                  {v.uploaderName && <span className="text-xs text-slate-400">· {v.uploaderName}</span>}
                </div>
                <div className="mt-0.5 text-[11px] text-slate-500">{t('uploadTime')}: {formatTime(v.createdAt)}</div>
              </div>
              <div className="flex items-center gap-1">
                <button type="button" onClick={() => downloadVersion(v)} title={t('downloadVersion')} className="grid h-8 w-8 place-items-center rounded-lg text-slate-300 hover:bg-white/10">
                  {busyId === v.id ? <Loader2 className="h-4 w-4 animate-spin" /> : <Download className="h-4 w-4" />}
                </button>
                <button type="button" onClick={() => restore(v)} title={t('restoreVersion')} disabled={busyId !== null} className="grid h-8 w-8 place-items-center rounded-lg text-emerald-300 hover:bg-emerald-500/15 disabled:opacity-40">
                  <RotateCcw className="h-4 w-4" />
                </button>
                <button type="button" onClick={() => remove(v)} title={t('deleteVersion')} disabled={busyId !== null} className="grid h-8 w-8 place-items-center rounded-lg text-rose-300 hover:bg-rose-500/15 disabled:opacity-40">
                  <Trash2 className="h-4 w-4" />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function ConfirmModal({ title, message, confirmText, danger, onConfirm, onClose }: any) {
  const { t } = useTranslation()
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const run = async () => {
    setBusy(true); setErr('')
    try { await onConfirm(); onClose() } catch (e: any) { setErr(e?.message || 'error'); setBusy(false) }
  }
  return (
    <Modal title={title} onClose={onClose}>
      <p className="text-sm text-slate-200">{message}</p>
      {err && <div className="mt-3 flex items-center gap-2 rounded-xl border border-rose-400/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300"><AlertCircle className="h-3.5 w-3.5" /><span>{err}</span></div>}
      <div className="mt-4 flex justify-end gap-2">
        <button type="button" className="btn-ghost" onClick={onClose}>{t('cancel')}</button>
        <button type="button" className={danger ? 'btn-primary !bg-gradient-to-r !from-rose-500 !to-rose-600' : 'btn-primary'} onClick={run} disabled={busy}>
          {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : null}{confirmText}
        </button>
      </div>
    </Modal>
  )
}

function TransferDrawer({ uploads, downloads }: { uploads: UploadTask[]; downloads: DownloadTask[] }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(true)
  const [tab, setTab] = useState<'upload' | 'download'>('upload')
  const list = tab === 'upload' ? uploads : downloads
  const active = list.filter((u) => u.status === 'uploading' || u.status === 'hashing' || u.status === 'downloading').length
  return (
    <div className="transfer-drawer w-[calc(100vw-32px)] max-w-sm md:w-96">
      <div className="glass-strong rounded-2xl overflow-hidden">
        <button type="button" onClick={() => setOpen((v) => !v)} className="flex w-full items-center justify-between gap-2 border-b border-white/10 px-4 py-2.5">
          <div className="flex items-center gap-2">
            <ArrowUpDown className="h-4 w-4 text-nebula-300" />
            <span className="text-sm font-semibold">{t('transferQueue')}</span>
            {active > 0 && <span className="grid h-5 min-w-5 place-items-center rounded-full bg-cyan-glow/30 px-1.5 text-[10px] text-white">{active}</span>}
          </div>
          {open ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
        </button>
        {open && (
          <>
            <div className="flex gap-1 border-b border-white/10 p-1.5">
              <button type="button" onClick={() => setTab('upload')} className={`flex-1 rounded-lg px-2 py-1.5 text-xs transition ${tab === 'upload' ? 'bg-nebula-500/30 text-white' : 'text-slate-300 hover:bg-white/10'}`}>
                {t('upload')} ({uploads.length})
              </button>
              <button type="button" onClick={() => setTab('download')} className={`flex-1 rounded-lg px-2 py-1.5 text-xs transition ${tab === 'download' ? 'bg-nebula-500/30 text-white' : 'text-slate-300 hover:bg-white/10'}`}>
                {t('download')} ({downloads.length})
              </button>
            </div>
            <div className="max-h-[260px] space-y-2 overflow-auto p-2">
              {list.length === 0 ? (
                <div className="py-6 text-center text-xs text-slate-400">—</div>
              ) : (
                list.map((u) => (
                  <div key={u.id} className="rounded-xl bg-white/5 p-2.5">
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate text-xs" title={u.name}>{u.name}</span>
                      <span className="shrink-0 text-[10px]">
                        {(u.status === 'hashing' || u.status === 'uploading' || u.status === 'downloading') && (
                          <span className="text-cyan-glow">{u.progress}%</span>
                        )}
                        {(u.status === 'rapid' || u.status === 'done') && (
                          <span className="flex items-center gap-0.5 text-emerald-300"><Check className="h-3 w-3" />{t('ns_files.uploadDone')}</span>
                        )}
                        {u.status === 'error' && <span className="text-rose-300">{t('ns_files.uploadFailed')}</span>}
                      </span>
                    </div>
                    <div className="glass-progress mt-1.5"><div style={{ width: `${u.progress}%` }} /></div>
                  </div>
                ))
              )}
            </div>
          </>
        )}
      </div>
    </div>
  )
}

export default function Files() {
  const { t } = useTranslation()
  const selectMode = useUserPrefsStore((s) => s.selectMode)
  const [files, setFiles] = useState<FileItem[]>([])
  const [expandedRows, setExpandedRows] = useState<Row[]>([])
  const [breadcrumbFiles, setBreadcrumbFiles] = useState<FileItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [currentParent, setCurrentParent] = useState<number | null>(null)
  const [trashMode, setTrashMode] = useState(false)
  const [view, setView] = useState<'list' | 'grid'>('list')
  const [allExpanded, setAllExpanded] = useState(false)
  const [selectedIds, setSelectedIds] = useState<Set<number>>(new Set())
  const [modal, setModal] = useState<ModalState>({ type: 'none' })
  const [tags, setTags] = useState<FileTag[]>([])
  const [filterTagId, setFilterTagId] = useState<number | null>(null)
  const [uploads, setUploads] = useState<UploadTask[]>([])
  const [downloads, setDownloads] = useState<DownloadTask[]>([])
  const [dragOver, setDragOver] = useState(false)
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number; ids: Set<number> } | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const dragCounterRef = useRef(0)

  const rows: Row[] = useMemo(() => {
    const base: Row[] = allExpanded && !trashMode ? expandedRows : files.map((f) => ({ file: f }))
    if (filterTagId == null) return base
    return base.filter((r) => (r.file.tags || []).some((tg) => tg.id === filterTagId))
  }, [allExpanded, expandedRows, files, trashMode, filterTagId])

  const loadTags = useCallback(async () => {
    try {
      const r = await api.files.listTags()
      if (r?.code === 0 && Array.isArray(r.data)) setTags(r.data)
    } catch { /* ignore */ }
  }, [])

  useEffect(() => { loadTags() }, [loadTags])

  const removeTagFromFile = async (fileId: number, tagId: number) => {
    try {
      await api.files.removeTag(fileId, tagId)
      await loadAll()
      await loadTags()
    } catch (e: any) { setError(e?.message || 'error') }
  }

  const crumbs = useMemo(() => {
    const items: { name: string; onClick?: () => void }[] = []
    items.push({ name: t('files'), onClick: () => { setTrashMode(false); enterDir(null) } })
    if (trashMode) {
      items.push({ name: t('recycle') })
    } else {
      breadcrumbFiles.forEach((b) => items.push({ name: b.name, onClick: () => enterDir(b.id) }))
    }
    return items
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [breadcrumbFiles, trashMode])

  const loadAll = useCallback(async () => {
    setLoading(true); setError('')
    try {
      if (trashMode) {
        const r = await api.files.list(currentParent, true)
        setFiles(r.data || []); setBreadcrumbFiles([])
      } else if (allExpanded) {
        const out: Row[] = []
        const queue: { id: number | null; prefix: string }[] = [{ id: currentParent, prefix: '' }]
        while (queue.length) {
          const { id, prefix } = queue.shift()!
          const r = await api.files.list(id)
          for (const f of r.data || []) {
            out.push({ file: f, prefix })
            if (f.isDir) queue.push({ id: f.id, prefix: prefix + f.name + '/' })
          }
        }
        setExpandedRows(out)
      } else {
        const r = await api.files.list(currentParent, trashMode)
        setFiles(r.data || [])
        if (currentParent == null) setBreadcrumbFiles([])
        else {
          const bc = await api.files.breadcrumb(currentParent)
          setBreadcrumbFiles(bc.data || [])
        }
      }
    } catch (e: any) { setError(e?.message || t('ns_files.loadFailed')) }
    finally { setLoading(false) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentParent, trashMode, allExpanded])

  useEffect(() => { loadAll() }, [loadAll])
  useEffect(() => {
    try {
      useUserPrefsStore.getState().load()
    } catch { /* ignore */ }
  }, [])

  const enterDir = (id: number | null) => { setSelectedIds(new Set()); setCurrentParent(id); setContextMenu(null) }

  const updateTask = (id: string, patch: Partial<UploadTask>) =>
    setUploads((u) => u.map((t2) => (t2.id === id ? { ...t2, ...patch } : t2)))

  const onPickFiles = (list: FileList | File[]) => {
    const arr = Array.from(list)
    if (!arr.length) return
    const tasks: UploadTask[] = arr.map((f) => ({
      id: `${f.name}-${f.size}-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
      name: f.name, size: f.size, status: 'hashing', progress: 0,
    }))
    setUploads((u) => [...u, ...tasks])
    void (async () => {
      for (let i = 0; i < arr.length; i++) {
        const file = arr[i]
        const taskId = tasks[i].id
        try {
          const hash = await computeMD5(file)
          const rapid = await api.files.rapid(hash, file.name, currentParent, file.size)
          if (rapid.code === 0) { updateTask(taskId, { status: 'rapid', progress: 100 }); continue }
          updateTask(taskId, { status: 'uploading', progress: 0 })
          const res = await uploadWithProgress(file, currentParent, hash, (p) => updateTask(taskId, { progress: p }))
          if (res.code === 0) updateTask(taskId, { status: res.rapid ? 'rapid' : 'done', progress: 100 })
          else updateTask(taskId, { status: 'error', error: res.message })
        } catch (e: any) { updateTask(taskId, { status: 'error', error: e?.message || 'error' }) }
      }
      await loadAll()
    })()
  }

  const downloadFile = async (f: FileItem) => {
    const id = `${f.id}-${Date.now()}`
    setDownloads((d) => [...d, { id, name: f.name, size: f.size, status: 'downloading', progress: 2 }])
    try {
      const blob = await fetchBlob(api.files.downloadURL(f.id))
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url; a.download = f.name; document.body.appendChild(a); a.click(); a.remove()
      setTimeout(() => URL.revokeObjectURL(url), 1500)
      setDownloads((d) => d.map((x) => x.id === id ? { ...x, status: 'done', progress: 100 } : x))
    } catch (e: any) {
      setDownloads((d) => d.map((x) => x.id === id ? { ...x, status: 'error', error: e?.message, progress: 0 } : x))
      setError(e?.message || 'download failed')
    }
  }

  const handleMkdir = async (name: string) => { await api.files.mkdir(name, currentParent) }
  const handleRename = async (name: string) => { if (modal.type !== 'rename') return; await api.files.rename(modal.file.id, name) }
  const handleMoved = async () => {}
  const handleDelete = async () => { if (modal.type !== 'delete') return; await api.files.remove(modal.file.id) }
  const handlePurge = async () => { if (modal.type !== 'purge') return; await api.files.purge(modal.file.id) }
  const handleRestore = async (f: FileItem) => { try { await api.files.restore(f.id); await loadAll() } catch (e: any) { setError(e?.message) } }

  // 拖拽
  useEffect(() => {
    const onDragEnter = (e: DragEvent) => {
      if (!e.dataTransfer?.types.includes('Files')) return
      e.preventDefault()
      dragCounterRef.current += 1
      if (dragCounterRef.current === 1) setDragOver(true)
    }
    const onDragLeave = (e: DragEvent) => {
      if (!e.dataTransfer?.types.includes('Files')) return
      e.preventDefault()
      dragCounterRef.current -= 1
      if (dragCounterRef.current <= 0) { dragCounterRef.current = 0; setDragOver(false) }
    }
    const onDragOver = (e: DragEvent) => { if (e.dataTransfer?.types.includes('Files')) e.preventDefault() }
    const onDrop = (e: DragEvent) => {
      if (!e.dataTransfer?.types.includes('Files')) return
      e.preventDefault()
      dragCounterRef.current = 0; setDragOver(false)
      if (e.dataTransfer.files && e.dataTransfer.files.length) onPickFiles(e.dataTransfer.files)
    }
    window.addEventListener('dragenter', onDragEnter)
    window.addEventListener('dragleave', onDragLeave)
    window.addEventListener('dragover', onDragOver)
    window.addEventListener('drop', onDrop)
    return () => {
      window.removeEventListener('dragenter', onDragEnter)
      window.removeEventListener('dragleave', onDragLeave)
      window.removeEventListener('dragover', onDragOver)
      window.removeEventListener('drop', onDrop)
    }
  }, [currentParent])

  const toggleSelect = (id: number, multi: boolean) => {
    setSelectedIds((prev) => {
      const n = new Set(multi ? prev : [])
      if (n.has(id)) n.delete(id); else n.add(id)
      setContextMenu(null)
      return n
    })
  }

  const onRowContextMenu = (e: React.MouseEvent, id: number) => {
    e.preventDefault()
    const ids = selectedIds.has(id) && selectedIds.size > 1 ? selectedIds : new Set([id])
    if (!selectedIds.has(id)) setSelectedIds(new Set([id]))
    setContextMenu({ x: e.clientX, y: e.clientY, ids })
  }

  const onGlobalMouseDown = (e: React.MouseEvent) => {
    if (contextMenu) setContextMenu(null)
  }

  const toolbar = (
    <div className="mr-2 flex items-center gap-2">
      {!trashMode ? (
        <>
          <button type="button" className="btn-ghost" onClick={() => setModal({ type: 'mkdir' })}>
            <FolderPlus className="h-4 w-4" /><span className="hidden sm:inline">{t('mkdir')}</span>
          </button>
          <button type="button" className="btn-ghost" onClick={() => fileInputRef.current?.click()}>
            <Upload className="h-4 w-4" /><span className="hidden sm:inline">{t('uploadFile')}</span>
          </button>
          <button type="button" className="btn-ghost" onClick={() => { setAllExpanded(false); setTrashMode(true) }}>
            <Trash2 className="h-4 w-4" /><span className="hidden sm:inline">{t('recycle')}</span>
          </button>
          <div className="mx-1 hidden h-5 w-px bg-white/10 sm:block" />
          <div className="hidden items-center gap-1 rounded-xl bg-white/5 p-1 sm:flex">
            <button type="button" onClick={() => setView('list')} className={`flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-xs transition ${view === 'list' ? 'bg-nebula-500/40 text-white' : 'text-slate-300 hover:bg-white/10'}`}>
              <List className="h-3.5 w-3.5" /><span>{t('listView')}</span>
            </button>
            <button type="button" onClick={() => setView('grid')} className={`flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-xs transition ${view === 'grid' ? 'bg-nebula-500/40 text-white' : 'text-slate-300 hover:bg-white/10'}`}>
              <LayoutGrid className="h-3.5 w-3.5" /><span>{t('gridView')}</span>
            </button>
          </div>
        </>
      ) : (
        <button type="button" className="btn-ghost" onClick={() => setTrashMode(false)}>
          <ArrowLeft className="h-4 w-4" /><span>{t('ns_files.backToFiles')}</span>
        </button>
      )}
    </div>
  )

  return (
    <MainLayout breadcrumb={crumbs} toolbar={toolbar} allExpanded={allExpanded} onToggleAllExpand={setAllExpanded}>
      <div className="flex h-full min-h-[60vh] flex-col gap-3" onMouseDown={onGlobalMouseDown} onClick={() => setContextMenu(null)}>
        {dragOver && <div className="drag-overlay" />}
        {error && (
          <div className="flex items-center gap-2 rounded-xl border border-rose-400/30 bg-rose-500/10 px-4 py-2 text-sm text-rose-300">
            <AlertCircle className="h-4 w-4 shrink-0" />
            <span className="flex-1">{error}</span>
            <button type="button" onClick={() => setError('')}><X className="h-3.5 w-3.5" /></button>
          </div>
        )}
        {/* 标签筛选条 */}
        {!trashMode && tags.length > 0 && (
          <div className="glass flex flex-wrap items-center gap-1.5 rounded-2xl px-3 py-2">
            <span className="mr-1 flex items-center gap-1 text-xs text-slate-400"><Filter className="h-3 w-3" />{t('filterByTag')}</span>
            <button
              type="button"
              onClick={() => setFilterTagId(null)}
              className={`rounded-full px-2.5 py-1 text-[11px] transition ${filterTagId == null ? 'bg-nebula-500/40 text-white' : 'bg-white/5 text-slate-300 hover:bg-white/10'}`}
            >
              {t('allTags')}
            </button>
            {tags.map((tg) => (
              <button
                key={tg.id}
                type="button"
                onClick={() => setFilterTagId(filterTagId === tg.id ? null : tg.id)}
                className={`flex items-center gap-1 rounded-full px-2.5 py-1 text-[11px] transition ${filterTagId === tg.id ? 'bg-nebula-500/40 text-white' : 'bg-white/5 text-slate-300 hover:bg-white/10'}`}
                title={tg.name}
              >
                <span className="h-2 w-2 rounded-full" style={{ backgroundColor: tg.color || '#888' }} />
                <span className="max-w-[8rem] truncate">{tg.name}</span>
                {typeof tg.fileCount === 'number' && <span className="text-slate-500">({tg.fileCount})</span>}
              </button>
            ))}
          </div>
        )}

        <div className="glass flex-1 overflow-auto rounded-2xl">
          {loading ? <div className="flex h-full items-center justify-center"><Loader2 className="h-7 w-7 animate-spin text-nebula-300" /></div>
            : rows.length === 0 ? (
              <div className="flex h-full flex-col items-center justify-center gap-3 text-slate-400">
                <div className="grid h-16 w-16 place-items-center rounded-2xl bg-white/5"><Folder className="h-8 w-8" /></div>
                <p className="text-sm">{t('ns_files.empty')}</p>
              </div>
            ) : view === 'list' ? (
              <div>
                <div className="sticky top-0 z-10 grid grid-cols-[auto_1fr_90px_130px_auto] gap-2 border-b border-white/10 bg-black/20 px-4 py-2 text-xs text-slate-400 backdrop-blur">
                  <button type="button" onClick={() => setSelectedIds(selectedIds.size === rows.length ? new Set() : new Set(rows.map((r) => r.file.id)))} className="text-slate-300 hover:text-white">
                    {selectedIds.size === rows.length ? <CheckSquare className="h-4 w-4 text-cyan-glow" /> : <Square className="h-4 w-4" />}
                  </button>
                  <span>{t('ns_files.name')}</span>
                  <span>{t('ns_files.size')}</span>
                  <span className="hidden sm:block">{t('ns_files.modified')}</span>
                  <span className="text-right">{t('type')}</span>
                </div>
                {rows.map(({ file: f, prefix }) => {
                  const { Icon, cls } = fileIcon(f)
                  const sel = selectedIds.has(f.id)
                  return (
                    <div
                      key={`${f.id}-${prefix || ''}`}
                      className={`grid grid-cols-[auto_1fr_90px_130px_auto] items-center gap-2 px-4 py-2.5 text-sm transition ${sel ? 'bg-nebula-500/20' : 'hover:bg-white/5'}`}
                      onClick={(e) => {
                        e.stopPropagation()
                        if (e.shiftKey || e.ctrlKey || e.metaKey) toggleSelect(f.id, true)
                        else { setSelectedIds(sel ? new Set() : new Set([f.id])); setContextMenu(null) }
                      }}
                      onDoubleClick={() => { if (f.isDir && !allExpanded && !trashMode) enterDir(f.id) }}
                      onContextMenu={(e) => onRowContextMenu(e, f.id)}
                    >
                      <button type="button" onClick={(e) => { e.stopPropagation(); toggleSelect(f.id, true) }}>
                        {sel ? <CheckSquare className="h-4 w-4 text-cyan-glow" /> : <Square className="h-4 w-4 text-slate-400" />}
                      </button>
                      <div className="flex min-w-0 items-center gap-2.5">
                        <Icon className={`h-4 w-4 shrink-0 ${cls}`} />
                        <span className="truncate">{prefix && <span className="text-slate-400">{prefix}</span>}{f.name}</span>
                        {(f.tags || []).length > 0 && (
                          <span className="hidden items-center gap-1 sm:flex">
                            {f.tags!.slice(0, 3).map((tg) => (
                              <span
                                key={tg.id}
                                className="inline-flex items-center gap-1 rounded-full px-1.5 py-0.5 text-[10px] text-white/90"
                                style={{ backgroundColor: (tg.color || '#6366f1') + 'cc' }}
                                title={tg.name}
                              >
                                <span className="max-w-[5rem] truncate">{tg.name}</span>
                                {!trashMode && (
                                  <button
                                    type="button"
                                    onClick={(e) => { e.stopPropagation(); removeTagFromFile(f.id, tg.id) }}
                                    className="text-white/70 hover:text-white"
                                  >
                                    <X className="h-2.5 w-2.5" />
                                  </button>
                                )}
                              </span>
                            ))}
                            {f.tags!.length > 3 && <span className="text-[10px] text-slate-400">+{f.tags!.length - 3}</span>}
                          </span>
                        )}
                      </div>
                      <span className="text-xs text-slate-400">{f.isDir ? '—' : formatSize(f.size)}</span>
                      <span className="hidden truncate text-xs text-slate-400 sm:block">{formatTime(f.updatedAt)}</span>
                      <div className="flex items-center justify-end gap-0.5" onClick={(e) => e.stopPropagation()}>
                        <RowActions
                          f={f} trash={trashMode}
                          onPreview={() => setModal({ type: 'preview', file: f })}
                          onDownload={() => downloadFile(f)}
                          onRename={() => setModal({ type: 'rename', file: f })}
                          onMove={() => setModal({ type: 'move', file: f })}
                          onShare={() => setModal({ type: 'share', file: f })}
                          onDelete={() => setModal({ type: 'delete', file: f })}
                          onPurge={() => setModal({ type: 'purge', file: f })}
                          onRestore={() => handleRestore(f)}
                          onAddTag={() => setModal({ type: 'addTag', file: f })}
                        />
                      </div>
                    </div>
                  )
                })}
              </div>
            ) : (
              <div className="grid grid-cols-2 gap-3 p-3 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
                {rows.map(({ file: f, prefix }) => {
                  const { Icon, cls } = fileIcon(f)
                  const sel = selectedIds.has(f.id)
                  return (
                    <div
                      key={`${f.id}-${prefix || ''}`}
                      className={`group flex cursor-pointer flex-col rounded-2xl bg-white/5 p-3 transition hover:bg-white/10 ${sel ? 'ring-2 ring-nebula-400' : ''}`}
                      onClick={(e) => { e.stopPropagation(); toggleSelect(f.id, e.shiftKey || e.ctrlKey || e.metaKey ? true : false) }}
                      onDoubleClick={() => { if (f.isDir && !allExpanded && !trashMode) enterDir(f.id) }}
                      onContextMenu={(e) => onRowContextMenu(e, f.id)}
                    >
                      <div className="relative mb-2 grid aspect-square place-items-center overflow-hidden rounded-xl bg-black/20">
                        {f.isDir ? <Folder className="h-10 w-10 text-nebula-300" /> : <Icon className={`h-10 w-10 ${cls}`} />}
                        {!trashMode && !f.isDir && (
                          <button
                            type="button"
                            onClick={(e) => { e.stopPropagation(); setModal({ type: 'addTag', file: f }) }}
                            className="absolute right-1.5 top-1.5 grid h-7 w-7 place-items-center rounded-lg bg-black/40 text-slate-300 opacity-0 transition hover:bg-black/60 hover:text-white group-hover:opacity-100"
                            title={t('addTag')}
                          >
                            <TagIcon className="h-3.5 w-3.5" />
                          </button>
                        )}
                      </div>
                      <div className="truncate text-sm" title={f.name}>{prefix && <span className="text-slate-400">{prefix}</span>}{f.name}</div>
                      <div className="mt-0.5 text-xs text-slate-400">{f.isDir ? '—' : formatSize(f.size)}</div>
                      {(f.tags || []).length > 0 && (
                        <div className="mt-1.5 flex flex-wrap gap-1">
                          {f.tags!.slice(0, 3).map((tg) => (
                            <span
                              key={tg.id}
                              className="inline-flex items-center gap-0.5 rounded-full px-1.5 py-0.5 text-[10px] text-white/90"
                              style={{ backgroundColor: (tg.color || '#6366f1') + 'cc' }}
                              title={tg.name}
                            >
                              <span className="h-1.5 w-1.5 rounded-full bg-white/80" />
                              <span className="max-w-[4rem] truncate">{tg.name}</span>
                            </span>
                          ))}
                          {f.tags!.length > 3 && <span className="text-[10px] text-slate-400">+{f.tags!.length - 3}</span>}
                        </div>
                      )}
                    </div>
                  )
                })}
              </div>
            )}
        </div>

        {/* 上传 input */}
        <input ref={fileInputRef} type="file" multiple className="hidden" onChange={(e) => { if (e.target.files) onPickFiles(e.target.files); e.target.value = '' }} />

        {/* 传输队列 */}
        {(uploads.length > 0 || downloads.length > 0) && <TransferDrawer uploads={uploads} downloads={downloads} />}

        {/* 上下文跟随模式 / 底部浮动条 / 侧边抽屉 */}
        <SelectUIPanel mode={selectMode} contextMenu={contextMenu} selectedIds={selectedIds}
          onClear={() => { setSelectedIds(new Set()); setContextMenu(null) }}
          onDelete={() => {
            const ids = Array.from(selectedIds)
            if (ids.length === 1) {
              const f = rows.find((r) => r.file.id === ids[0])?.file
              if (f) setModal({ type: 'delete', file: f })
            } else if (ids.length > 1) {
              setModal({ type: 'batchDelete', ids })
            }
          }}
          onBatchMove={() => setModal({ type: 'batchMove', ids: Array.from(selectedIds) })}
          onBatchCopy={() => setModal({ type: 'batchCopy', ids: Array.from(selectedIds) })}
          onBatchPolicy={() => setModal({ type: 'batchPolicy', ids: Array.from(selectedIds) })}
          onDownload={() => {
            const ids = Array.from(selectedIds)
            rows.filter((r) => ids.includes(r.file.id) && !r.file.isDir).forEach((r) => downloadFile(r.file))
          }}
        />
      </div>

      {/* 弹窗 */}
      {modal.type === 'mkdir' && <NamePromptModal title={t('mkdir')} label={t('ns_files.folderName')} onSubmit={handleMkdir} onClose={() => { setModal({ type: 'none' }); loadAll() }} />}
      {modal.type === 'rename' && <NamePromptModal title={t('rename')} label={t('ns_files.name')} initial={modal.file.name} onSubmit={handleRename} onClose={() => { setModal({ type: 'none' }); loadAll() }} />}
      {modal.type === 'move' && <MoveModal file={modal.file} onMoved={async () => { setModal({ type: 'none' }); await loadAll() }} onClose={() => setModal({ type: 'none' })} />}
      {modal.type === 'share' && <ShareModal file={modal.file} onClose={() => setModal({ type: 'none' })} />}
      {modal.type === 'preview' && <PreviewModal file={modal.file} onClose={() => setModal({ type: 'none' })} onDownload={() => downloadFile(modal.file)} />}
      {modal.type === 'delete' && (
        <ConfirmModal title={t('deleteFile')} message={t('ns_files.confirmDelete')} confirmText={t('delete')} danger onConfirm={handleDelete} onClose={() => { setModal({ type: 'none' }); loadAll() }} />
      )}
      {modal.type === 'purge' && (
        <ConfirmModal title={t('purge')} message={t('ns_files.confirmPurge')} confirmText={t('purge')} danger onConfirm={handlePurge} onClose={() => { setModal({ type: 'none' }); loadAll() }} />
      )}
      {modal.type === 'addTag' && (
        <AddTagModal file={modal.file} onDone={() => { setModal({ type: 'none' }); loadAll(); loadTags() }} onClose={() => setModal({ type: 'none' })} />
      )}
      {modal.type === 'batchMove' && (
        <DirPickerModal title={t('batchMove')} confirmText={t('move')} ids={modal.ids} mode="move" onDone={async () => { setModal({ type: 'none' }); setSelectedIds(new Set()); await loadAll() }} onClose={() => setModal({ type: 'none' })} />
      )}
      {modal.type === 'batchCopy' && (
        <DirPickerModal title={t('batchCopy')} confirmText={t('create')} ids={modal.ids} mode="copy" onDone={async () => { setModal({ type: 'none' }); setSelectedIds(new Set()); await loadAll() }} onClose={() => setModal({ type: 'none' })} />
      )}
      {modal.type === 'batchPolicy' && (
        <BatchPolicyModal ids={modal.ids} onDone={async () => { setModal({ type: 'none' }); setSelectedIds(new Set()); await loadAll() }} onClose={() => setModal({ type: 'none' })} />
      )}
      {modal.type === 'batchDelete' && (
        <BatchDeleteModal ids={modal.ids} onDone={async () => { setModal({ type: 'none' }); setSelectedIds(new Set()); await loadAll() }} onClose={() => setModal({ type: 'none' })} />
      )}
    </MainLayout>
  )
}

function RowActions({ f, trash, onPreview, onDownload, onRename, onMove, onShare, onDelete, onPurge, onRestore, onAddTag }: any) {
  if (trash) {
    return (
      <>
        <button type="button" onClick={onRestore} title="restore" className="grid h-8 w-8 place-items-center rounded-lg text-slate-300 hover:bg-white/10"><RotateCcw className="h-4 w-4" /></button>
        <button type="button" onClick={onPurge} title="purge" className="grid h-8 w-8 place-items-center rounded-lg text-rose-300 hover:bg-rose-500/15"><Trash2 className="h-4 w-4" /></button>
      </>
    )
  }
  const previewKind = getPreviewKind(f)
  return (
    <div className="hidden items-center gap-0.5 sm:flex">
      {!f.isDir && <button type="button" onClick={onDownload} title="download" className="grid h-8 w-8 place-items-center rounded-lg text-slate-300 hover:bg-white/10"><Download className="h-4 w-4" /></button>}
      {!f.isDir && previewKind !== 'none' && <button type="button" onClick={onPreview} title="preview" className="grid h-8 w-8 place-items-center rounded-lg text-slate-300 hover:bg-white/10"><Eye className="h-4 w-4" /></button>}
      {!f.isDir && onAddTag && <button type="button" onClick={onAddTag} title="tag" className="grid h-8 w-8 place-items-center rounded-lg text-slate-300 hover:bg-white/10"><TagIcon className="h-4 w-4" /></button>}
      <button type="button" onClick={onRename} title="rename" className="grid h-8 w-8 place-items-center rounded-lg text-slate-300 hover:bg-white/10"><Pencil className="h-4 w-4" /></button>
      <button type="button" onClick={onMove} title="move" className="grid h-8 w-8 place-items-center rounded-lg text-slate-300 hover:bg-white/10"><FolderInput className="h-4 w-4" /></button>
      <button type="button" onClick={onShare} title="share" className="grid h-8 w-8 place-items-center rounded-lg text-slate-300 hover:bg-white/10"><Share2 className="h-4 w-4" /></button>
      <button type="button" onClick={onDelete} title="delete" className="grid h-8 w-8 place-items-center rounded-lg text-rose-300 hover:bg-rose-500/15"><Trash2 className="h-4 w-4" /></button>
    </div>
  )
}

function SelectUIPanel({ mode, contextMenu, selectedIds, onClear, onDelete, onDownload, onBatchMove, onBatchCopy, onBatchPolicy }: {
  mode: SelectMode
  contextMenu: { x: number; y: number; ids: Set<number> } | null
  selectedIds: Set<number>
  onClear: () => void
  onDelete: () => void
  onDownload: () => void
  onBatchMove: () => void
  onBatchCopy: () => void
  onBatchPolicy: () => void
}) {
  const { t } = useTranslation()
  if (selectedIds.size === 0) return null
  const BatchButtons = ({ iconCls }: { iconCls?: string }) => (
    <>
      <button type="button" onClick={onBatchMove} className={`flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-sm text-slate-200 hover:bg-white/10 ${iconCls || ''}`}><FolderInput className="h-4 w-4" />{t('batchMove')}</button>
      <button type="button" onClick={onBatchCopy} className={`flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-sm text-slate-200 hover:bg-white/10 ${iconCls || ''}`}><FolderSync className="h-4 w-4" />{t('batchCopy')}</button>
      <button type="button" onClick={onBatchPolicy} className={`flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-sm text-slate-200 hover:bg-white/10 ${iconCls || ''}`}><Layers className="h-4 w-4" />{t('batchPolicy')}</button>
    </>
  )
  if (mode === 'context' && contextMenu) {
    return (
      <div className="context-float glass-strong rounded-xl p-1 shadow-glass-lg" style={{ top: Math.min(contextMenu.y, window.innerHeight - 320), left: Math.min(contextMenu.x, window.innerWidth - 260) }}>
        <div className="flex items-center justify-between gap-2 px-2 py-1.5 border-b border-white/10">
          <span className="text-xs font-semibold text-slate-200">{selectedIds.size} {t('selectAll')}</span>
          <button type="button" onClick={onClear} className="grid h-6 w-6 place-items-center rounded-lg text-slate-400 hover:bg-white/10"><X className="h-3.5 w-3.5" /></button>
        </div>
        <button type="button" onClick={onDownload} className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-sm text-slate-200 hover:bg-white/10"><Download className="h-4 w-4" />{t('download')}</button>
        <BatchButtons />
        <button type="button" onClick={onDelete} className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-sm text-rose-300 hover:bg-rose-500/10"><Trash2 className="h-4 w-4" />{t('batchDelete')}</button>
      </div>
    )
  }
  if (mode === 'floating') {
    return (
      <div className="floating-select-bar glass-strong flex flex-wrap items-center gap-2 rounded-2xl px-3 py-2 shadow-glass-lg">
        <span className="text-xs text-slate-200">{selectedIds.size} {t('selectAll')}</span>
        <div className="mx-1 h-5 w-px bg-white/10" />
        <button type="button" onClick={onDownload} className="btn-ghost py-1.5"><Download className="h-4 w-4" />{t('download')}</button>
        <button type="button" onClick={onBatchMove} className="btn-ghost py-1.5"><FolderInput className="h-4 w-4" />{t('batchMove')}</button>
        <button type="button" onClick={onBatchCopy} className="btn-ghost py-1.5"><FolderSync className="h-4 w-4" />{t('batchCopy')}</button>
        <button type="button" onClick={onBatchPolicy} className="btn-ghost py-1.5"><Layers className="h-4 w-4" />{t('batchPolicy')}</button>
        <button type="button" onClick={onDelete} className="btn-ghost py-1.5 text-rose-300"><Trash2 className="h-4 w-4" />{t('batchDelete')}</button>
        <button type="button" onClick={onClear} className="btn-ghost py-1.5"><X className="h-4 w-4" /></button>
      </div>
    )
  }
  // drawer
  return (
    <div className="glass-strong fixed right-0 top-1/3 z-70 w-56 rounded-l-2xl p-2 shadow-glass-lg border-r-0 animate-slide-up">
      <div className="flex items-center justify-between px-2 py-1.5 border-b border-white/10">
        <span className="text-xs font-semibold text-slate-200">{selectedIds.size} {t('selectAll')}</span>
        <button type="button" onClick={onClear} className="grid h-6 w-6 place-items-center rounded-lg text-slate-400 hover:bg-white/10"><X className="h-3.5 w-3.5" /></button>
      </div>
      <button type="button" onClick={onDownload} className="mt-1 flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-sm text-slate-200 hover:bg-white/10"><Download className="h-4 w-4" />{t('download')}</button>
      <BatchButtons />
      <button type="button" onClick={onDelete} className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-sm text-rose-300 hover:bg-rose-500/10"><Trash2 className="h-4 w-4" />{t('batchDelete')}</button>
    </div>
  )
}
