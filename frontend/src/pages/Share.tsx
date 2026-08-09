import { useEffect, useState, useCallback } from 'react'
import { useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import {
  Folder,
  ChevronRight,
  ChevronDown,
  FolderOpen,
  Lock,
  Download,
  Copy,
  Clock,
  Eye,
  FileText,
  Image,
  Film,
  Music,
  Package,
  File,
  ArrowLeftRight,
  KeyRound,
  X,
  AlertCircle,
  Check,
  ServerCrash,
} from 'lucide-react'
import { api } from '../api/client'

interface ShareNode {
  id: string | number
  name: string
  type: 'dir' | 'file'
  size?: number
  mime?: string
  children?: ShareNode[]
  open?: boolean
}

type Stage = 'loading' | 'pwd' | 'extract' | 'ready' | 'error'

export default function Share() {
  const { t } = useTranslation()
  const { id } = useParams<{ id: string }>()
  const [tree, setTree] = useState<ShareNode[]>([])
  const [sel, setSel] = useState<ShareNode | null>(null)
  const [meta, setMeta] = useState<any>(null)
  const [copyOk, setCopyOk] = useState(false)
  const [errMsg, setErrMsg] = useState<string>('')

  const [stage, setStage] = useState<Stage>('loading')
  const [pwd, setPwd] = useState('')
  const [extractCode, setExtractCode] = useState('')
  const [pwdErr, setPwdErr] = useState('')
  const [extractErr, setExtractErr] = useState('')
  const [verifying, setVerifying] = useState(false)

  const pickFirst = (nodes: ShareNode[]): ShareNode | null => {
    if (!Array.isArray(nodes)) return null
    for (const n of nodes) {
      if (n.type === 'file') return n
      if (Array.isArray(n.children) && n.children.length) {
        const f: ShareNode | null = pickFirst(n.children)
        if (f) return f
      }
    }
    return null
  }

  const tryLoadData = useCallback(async (password?: string, extract?: string) => {
    const shareId = id || (new URLSearchParams(location.search).get('token')) || ''
    if (!shareId) {
      setStage('error'); setErrMsg('缺少分享标识')
      return
    }
    setVerifying(true); setErrMsg('')
    try {
      const r = await api.shares.detail(shareId, password, extract)
      if (r?.code === 0) {
        const gotTree: ShareNode[] = Array.isArray(r.data?.tree) ? r.data.tree : []
        setMeta(r.data?.meta || r.data || null)
        setTree(gotTree)
        const first = pickFirst(gotTree)
        if (first) setSel(first)
        setStage('ready')
      } else if (r?.code === 2) {
        setStage('pwd')
      } else if (r?.code === 3) {
        setStage('extract')
      } else {
        setStage('error')
        setErrMsg(r?.message || '加载分享失败')
      }
    } catch (e: any) {
      setStage('error')
      setErrMsg(e?.response?.data?.message || e?.message || '加载失败，稍后重试')
    } finally {
      setVerifying(false)
    }
  }, [id])

  useEffect(() => {
    tryLoadData()
  }, [tryLoadData])

  const submitPwd = async () => {
    if (!pwd.trim()) { setPwdErr('请输入访问密码'); return }
    setPwdErr('')
    const shareId = id || (new URLSearchParams(location.search).get('token')) || ''
    setVerifying(true); setErrMsg('')
    try {
      const r = await api.shares.detail(shareId, pwd, undefined)
      if (r?.code === 0) {
        const gotTree: ShareNode[] = Array.isArray(r.data?.tree) ? r.data.tree : []
        setMeta(r.data?.meta || r.data || null)
        setTree(gotTree)
        const first = pickFirst(gotTree)
        if (first) setSel(first)
        setStage('ready')
      } else if (r?.code === 2) {
        setPwdErr('密码错误')
      } else if (r?.code === 3) {
        setStage('extract')
      } else {
        setStage('error')
        setErrMsg(r?.message || '加载分享失败')
      }
    } catch (e: any) {
      setStage('error')
      setErrMsg(e?.response?.data?.message || e?.message || '加载失败，稍后重试')
    } finally {
      setVerifying(false)
    }
  }

  const submitExtract = async () => {
    if (!extractCode.trim() || extractCode.length < 4) { setExtractErr('请输入4位提取码'); return }
    setExtractErr('')
    const shareId = id || (new URLSearchParams(location.search).get('token')) || ''
    setVerifying(true); setErrMsg('')
    try {
      const r = await api.shares.detail(shareId, pwd, extractCode)
      if (r?.code === 0) {
        const gotTree: ShareNode[] = Array.isArray(r.data?.tree) ? r.data.tree : []
        setMeta(r.data?.meta || r.data || null)
        setTree(gotTree)
        const first = pickFirst(gotTree)
        if (first) setSel(first)
        setStage('ready')
      } else if (r?.code === 3) {
        setExtractErr('提取码错误')
      } else {
        setStage('error')
        setErrMsg(r?.message || '加载分享失败')
      }
    } catch (e: any) {
      setStage('error')
      setErrMsg(e?.response?.data?.message || e?.message || '加载失败，稍后重试')
    } finally {
      setVerifying(false)
    }
  }

  const toggle = (node: ShareNode) => {
    const walk = (list: ShareNode[]): ShareNode[] =>
      list.map((x) => {
        if (x.id === node.id && x.type === 'dir') return { ...x, open: !x.open }
        return x.children ? { ...x, children: walk(x.children) } : x
      })
    setTree(walk(tree))
  }

  const select = (node: ShareNode) => {
    if (node.type === 'dir') toggle(node)
    else setSel(node)
  }

  const copyLink = async () => {
    try {
      await navigator.clipboard.writeText(location.href)
      setCopyOk(true)
      setTimeout(() => setCopyOk(false), 1500)
    } catch {
      alert('Copied')
    }
  }

  return (
    <div className="min-h-screen bg-gradient-to-br from-slate-900 via-[#0f172a] to-[#1e1b4b] px-3 py-4 text-slate-100 md:px-8 md:py-6">
      <div className="mx-auto mb-4 flex max-w-6xl items-center justify-between">
        <div className="flex items-center gap-2 text-lg font-semibold">
          <div className="site-logo grid h-9 w-9 place-items-center rounded-xl bg-gradient-to-br from-nebula-500 to-cyan-glow shadow-lg">
            <ArrowLeftRight className="h-5 w-5 text-white" />
          </div>
          <span>{meta?.title || t('ns_share.title')}</span>
        </div>
        <div className="hidden items-center gap-4 text-xs text-slate-400 sm:flex">
          <span className="flex items-center gap-1"><Clock className="h-3.5 w-3.5" />
            过期: {meta?.expireAt ? new Date(meta.expireAt).toLocaleDateString() : '-'}
          </span>
          <span className="flex items-center gap-1"><Eye className="h-3.5 w-3.5" />{meta?.viewTimes || 0}</span>
          <span className="flex items-center gap-1"><Download className="h-3.5 w-3.5" />{meta?.downloadTimes || 0}</span>
        </div>
      </div>

      {stage === 'error' ? (
        <div className="mx-auto grid max-w-6xl place-items-center py-20">
          <div className="glass rounded-3xl p-10 text-center shadow-glass-lg">
            <div className="mx-auto mb-5 grid h-16 w-16 place-items-center rounded-2xl bg-gradient-to-br from-rose-500/30 to-rose-700/20 border border-rose-400/30">
              <ServerCrash className="h-8 w-8 text-rose-300" />
            </div>
            <div className="text-lg font-semibold">{t('ns_share.loadFailed') || '分享加载失败'}</div>
            <div className="mt-2 break-all text-sm text-slate-400">
              {errMsg || t('ns_share.loadFailedHint') || '分享不存在、已过期、或网络错误，请检查后重试'}
            </div>
            <button
              type="button"
              onClick={() => tryLoadData()}
              className="btn-primary mt-6"
            >
              {t('retry') || '重试'}
            </button>
          </div>
        </div>
      ) : (
        <div className="mx-auto grid max-w-6xl grid-cols-1 gap-4 lg:grid-cols-[280px_1fr]">
          <aside className="glass rounded-2xl p-3">
            <div className="mb-2 flex items-center justify-between px-1">
              <div className="text-xs font-semibold uppercase text-slate-400">{t('files')}</div>
              {meta?.size && <div className="text-xs text-slate-500">{fmtSize(meta.size)}</div>}
            </div>
            <div className="max-h-[70vh] overflow-auto pr-1">
              {stage === 'ready' || stage === 'loading' ? (
                <Tree level={0} nodes={tree} sel={sel} onSel={select} onToggle={toggle} />
              ) : (
                <div className="p-3 text-center text-xs text-slate-400">请先完成验证</div>
              )}
              {stage === 'ready' && tree.length === 0 && (
                <div className="p-5 text-center text-xs text-slate-500">此分享目录为空</div>
              )}
            </div>
          </aside>

          <section className="glass rounded-2xl p-4 min-h-[70vh] flex flex-col">
            {!sel ? (
              <div className="flex flex-1 flex-col items-center justify-center text-slate-400">
                <FolderOpen className="mb-3 h-12 w-12 opacity-60" />
                <div className="text-sm">{t('ns_share.selectPreview') || 'Select a file to preview'}</div>
              </div>
            ) : (
              <>
                <div className="mb-3 flex items-center justify-between">
                  <div>
                    <div className="text-sm font-medium">{sel.name}</div>
                    <div className="text-xs text-slate-400">
                      {sel.type === 'file' ? fmtSize(sel.size || 0) : '-'} · {sel.mime || 'dir'}
                    </div>
                  </div>
                  <div className="flex gap-2">
                    <button type="button" onClick={copyLink} className="btn-ghost !py-1.5 text-xs">
                      {copyOk ? <span className="text-emerald-400">OK</span> : <Copy className="h-3.5 w-3.5" />}
                      {t('copyLink')}
                    </button>
                    <button type="button" className="btn-primary !py-1.5 text-xs">
                      <Download className="h-3.5 w-3.5" />
                      {t('download')}
                    </button>
                  </div>
                </div>

                <Preview node={sel} />
              </>
            )}
          </section>
        </div>
      )}

      {stage === 'pwd' && (
        <GlassModal onClose={() => {}} hideClose>
          <div className="space-y-4">
            <div className="flex flex-col items-center gap-2 text-center">
              <div className="grid h-14 w-14 place-items-center rounded-2xl bg-gradient-to-br from-amber-400/30 to-amber-600/20 border border-amber-400/30">
                <Lock className="h-7 w-7 text-amber-300" />
              </div>
              <div>
                <div className="text-lg font-semibold">此分享已加密</div>
                <div className="mt-1 text-sm text-slate-400">请输入访问密码以继续</div>
              </div>
            </div>
            <label className="block">
              <input
                type="password"
                autoFocus
                className="glass-input text-center text-lg tracking-widest"
                placeholder="请输入访问密码"
                value={pwd}
                onChange={(e) => { setPwd(e.target.value); setPwdErr('') }}
                onKeyDown={(e) => e.key === 'Enter' && submitPwd()}
              />
            </label>
            {pwdErr && (
              <div className="flex items-center gap-2 rounded-xl border border-rose-400/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
                <AlertCircle className="h-3.5 w-3.5" /><span>{pwdErr}</span>
              </div>
            )}
            <button type="button" onClick={submitPwd} disabled={verifying} className="btn-primary w-full">
              {verifying ? <div className="h-4 w-4 animate-spin rounded-full border-2 border-white/30 border-t-white" /> : <Check className="h-4 w-4" />}
              验证密码
            </button>
          </div>
        </GlassModal>
      )}

      {stage === 'extract' && (
        <GlassModal onClose={() => {}} hideClose>
          <div className="space-y-4">
            <div className="flex flex-col items-center gap-2 text-center">
              <div className="grid h-14 w-14 place-items-center rounded-2xl bg-gradient-to-br from-cyan-400/30 to-nebula-600/20 border border-cyan-400/30">
                <KeyRound className="h-7 w-7 text-cyan-300" />
              </div>
              <div>
                <div className="text-lg font-semibold">需要提取码</div>
                <div className="mt-1 text-sm text-slate-400">请输入 4 位提取码</div>
              </div>
            </div>
            <label className="block">
              <input
                type="text"
                maxLength={4}
                autoFocus
                className="glass-input text-center text-2xl font-bold tracking-[0.8em] uppercase"
                placeholder="0000"
                value={extractCode}
                onChange={(e) => { setExtractCode(e.target.value.replace(/[^0-9a-zA-Z]/g, '').slice(0, 4)); setExtractErr('') }}
                onKeyDown={(e) => e.key === 'Enter' && submitExtract()}
              />
            </label>
            {extractErr && (
              <div className="flex items-center gap-2 rounded-xl border border-rose-400/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
                <AlertCircle className="h-3.5 w-3.5" /><span>{extractErr}</span>
              </div>
            )}
            <button type="button" onClick={submitExtract} disabled={verifying} className="btn-primary w-full">
              {verifying ? <div className="h-4 w-4 animate-spin rounded-full border-2 border-white/30 border-t-white" /> : <Check className="h-4 w-4" />}
              确认提取码
            </button>
          </div>
        </GlassModal>
      )}
    </div>
  )
}

function GlassModal({ children, onClose, hideClose }: { children: React.ReactNode; onClose: () => void; hideClose?: boolean }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4 backdrop-blur-md animate-fade-in">
      <div className="glass-strong animate-slide-up relative w-full max-w-md overflow-hidden rounded-3xl p-6 shadow-glass-lg">
        {!hideClose && (
          <button type="button" onClick={onClose} className="absolute right-3 top-3 grid h-8 w-8 place-items-center rounded-xl text-slate-400 hover:bg-white/10 hover:text-slate-100">
            <X className="h-4 w-4" />
          </button>
        )}
        {children}
      </div>
    </div>
  )
}

function Tree({
  level, nodes, sel, onSel, onToggle,
}: { level: number; nodes: ShareNode[]; sel: ShareNode | null; onSel: (n: ShareNode) => void; onToggle: (n: ShareNode) => void }) {
  return (
    <ul className={level === 0 ? '' : 'pl-4'}>
      {nodes.map((n) => {
        const active = sel?.id === n.id
        return (
          <li key={n.id}>
            <button
              type="button"
              onClick={() => onSel(n)}
              className={`group flex w-full items-center gap-1.5 rounded-lg px-2 py-1.5 text-sm transition ${
                active ? 'bg-nebula-500/20 text-white' : 'text-slate-200 hover:bg-white/5'
              }`}
            >
              {n.type === 'dir' ? (
                <span
                  className="grid h-4 w-4 place-items-center text-slate-400 group-hover:text-slate-200"
                  onClick={(e) => {
                    e.stopPropagation()
                    onToggle(n)
                  }}
                >
                  {n.open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
                </span>
              ) : (
                <span className="w-4" />
              )}
              <FileIcon node={n} />
              <span className="truncate">{n.name}</span>
            </button>
            {n.type === 'dir' && n.open && n.children && (
              <Tree level={level + 1} nodes={n.children} sel={sel} onSel={onSel} onToggle={onToggle} />
            )}
          </li>
        )
      })}
    </ul>
  )
}

function FileIcon({ node }: { node: ShareNode }) {
  if (node.type === 'dir') {
    return node.open ? (
      <FolderOpen className="h-4 w-4 text-amber-300" />
    ) : (
      <Folder className="h-4 w-4 text-amber-300" />
    )
  }
  const m = node.mime || ''
  if (m.startsWith('image/')) return <Image className="h-4 w-4 text-rose-300" />
  if (m.startsWith('video/')) return <Film className="h-4 w-4 text-purple-300" />
  if (m.startsWith('audio/')) return <Music className="h-4 w-4 text-cyan-300" />
  if (/zip|rar|7z|tar|gz/.test(m)) return <Package className="h-4 w-4 text-emerald-300" />
  if (/text|markdown|tsx?|jsx?|json|css|html|xml/.test(m)) return <FileText className="h-4 w-4 text-sky-300" />
  return <File className="h-4 w-4 text-slate-300" />
}

function Preview({ node }: { node: ShareNode }) {
  const mime = node.mime || ''
  if (mime.startsWith('image/')) {
    return (
      <div className="flex flex-1 items-center justify-center overflow-hidden rounded-xl bg-black/30 p-4">
        <img
          src="https://trae-api-cn.mchost.guru/api/ide/v1/text_to_image?prompt=Nebula%20cloud%20storage%20interface%20screenshot%2C%20clean%20ui%2C%20frosted%20glass%20design%2C%20purple%20cyan%20gradient&image_size=landscape_16_9"
          alt={node.name}
          className="max-h-[60vh] max-w-full rounded-lg shadow-glass-lg"
          onError={(e) => ((e.target as HTMLImageElement).style.display = 'none')}
        />
      </div>
    )
  }
  if (mime.startsWith('video/')) {
    return (
      <div className="flex flex-1 items-center justify-center rounded-xl bg-black/30 p-4">
        <div className="text-sm text-slate-400">▶ Video Preview · {node.name}</div>
      </div>
    )
  }
  if (mime.startsWith('audio/')) {
    return (
      <div className="flex flex-1 items-center justify-center rounded-xl bg-black/30 p-4">
        <div className="w-full max-w-md space-y-2">
          <div className="text-center text-slate-300">{node.name}</div>
          <div className="h-10 w-full rounded-lg bg-white/5" />
        </div>
      </div>
    )
  }
  if (/text|markdown|tsx?|jsx?|json|css|html|xml|md/.test(mime)) {
    const code = `// ${node.name}\nimport { useTranslation } from 'react-i18next'\n\nexport default function Demo() {\n  const { t } = useTranslation()\n  return <div className=\"glass\">{t('preview')}</div>\n}\n`
    return (
      <div className="flex-1 overflow-auto rounded-xl bg-black/30 p-4">
        <pre className="text-xs leading-relaxed text-slate-300">
          <code>{code}</code>
        </pre>
      </div>
    )
  }
  return (
    <div className="flex flex-1 items-center justify-center text-slate-400">
      <div className="text-center">
        <Package className="mx-auto mb-2 h-10 w-10 text-slate-500" />
        <div className="text-sm">No preview available</div>
      </div>
    </div>
  )
}

function fmtSize(b: number) {
  if (!b) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0, n = b
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++ }
  return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${u[i]}`
}
