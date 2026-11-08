import { useEffect, useState } from 'react'
import { useSearchParams, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import {
  Search, Sparkles, FileText, FileCode, Image as ImageIcon, Film, Music,
  FileArchive, File as FileIcon, Download, Code2, Loader2, Clock, Flame, X, Tag,
} from 'lucide-react'
import MainLayout from '../layouts/MainLayout'
import { api, fetchBlob, type FileItem, type AIFileBrief, type AIStatus } from '../api/client'

function formatSize(bytes: number): string {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0, n = bytes
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++ }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

function iconOf(f: { name: string; mimeType: string; extension: string }) {
  const ext = (f.extension || '').toLowerCase()
  const mt = (f.mimeType || '').toLowerCase()
  if (mt.startsWith('image/') || ['jpg', 'png', 'gif', 'webp', 'svg'].includes(ext)) return ImageIcon
  if (mt.startsWith('video/') || ['mp4', 'mkv', 'mov'].includes(ext)) return Film
  if (mt.startsWith('audio/') || ['mp3', 'wav', 'flac'].includes(ext)) return Music
  if (['zip', 'rar', '7z', 'tar', 'gz'].includes(ext)) return FileArchive
  if (['js', 'ts', 'go', 'py', 'java', 'json', 'html', 'css'].includes(ext)) return FileCode
  if (['txt', 'md', 'csv', 'xml', 'yaml', 'yml'].includes(ext)) return FileText
  return FileIcon
}

const TEXT_EXT = new Set([
  'txt', 'md', 'json', 'js', 'ts', 'jsx', 'tsx', 'go', 'py', 'java', 'c', 'cpp', 'h',
  'html', 'css', 'xml', 'yaml', 'yml', 'sql', 'sh', 'php', 'rs', 'rb', 'ini', 'conf',
  'csv', 'toml', 'vue', 'scss', 'less',
])

export default function SearchResults() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const q = params.get('q') || ''
  const aiMode = params.get('ai') === '1'
  const [input, setInput] = useState(q)
  const [loading, setLoading] = useState(false)
  const [list, setList] = useState<FileItem[]>([])
  const [total, setTotal] = useState(0)
  const [answer, setAnswer] = useState('')
  const [filters, setFilters] = useState<Record<string, any> | null>(null)
  const [mode, setMode] = useState('local')
  const [recent, setRecent] = useState<AIFileBrief[]>([])
  const [hot, setHot] = useState<AIFileBrief[]>([])
  const [status, setStatus] = useState<AIStatus | null>(null)
  const [summary, setSummary] = useState<{ id: number; text: string; tags: string[]; provider: string } | null>(null)
  const [busyId, setBusyId] = useState(0)

  useEffect(() => { setInput(q) }, [q])
  useEffect(() => {
    api.ai.status().then((r) => setStatus(r.data)).catch(() => {})
    api.files.recent(6).then((r) => setRecent(r.data)).catch(() => {})
    api.files.hot(6).then((r) => setHot(r.data)).catch(() => {})
  }, [])

  useEffect(() => {
    if (!q) { setList([]); return }
    let alive = true
    setLoading(true)
    const run = aiMode
      ? api.ai.search(q, 50).then((r) => {
          if (!alive) return
          setAnswer(r.data.answer); setFilters(r.data.filters); setMode(r.data.mode)
          setList(r.data.list || []); setTotal((r.data.list || []).length)
        })
      : api.files.search({ keyword: q, size: 50 }).then((r) => {
          if (!alive) return
          setList(r.data.list || []); setTotal(r.data.total || 0)
          setAnswer(''); setFilters(null); setMode('local')
        })
    run.catch(() => { if (alive) setList([]) }).finally(() => { if (alive) setLoading(false) })
    return () => { alive = false }
  }, [q, aiMode])

  const submit = (ai = aiMode) => {
    const kw = input.trim()
    if (!kw) return
    api.files.saveSearchHistory(kw).catch(() => {})
    setParams({ q: kw, ...(ai ? { ai: '1' } : {}) })
  }

  const download = async (f: FileItem) => {
    try {
      const blob = await fetchBlob(api.files.downloadURL(f.id))
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url; a.download = f.name; a.click()
      setTimeout(() => URL.revokeObjectURL(url), 2000)
    } catch (e) { alert(String(e)) }
  }

  const showSummary = async (f: FileItem) => {
    setBusyId(f.id)
    try {
      const r = await api.ai.summary(f.id)
      setSummary({ id: f.id, text: r.data.summary, tags: r.data.tags, provider: r.data.provider })
    } catch (e) { alert(String(e)) }
    finally { setBusyId(0) }
  }

  const crumbs = [{ name: t('ai.searchResults') || '搜索结果' }]

  return (
    <MainLayout breadcrumb={crumbs}>
      <div className="mx-auto max-w-6xl space-y-5 p-4 md:p-6">
        {/* 查询条 */}
        <div className="glass-strong rounded-2xl p-4">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
            <div className="glass flex h-11 flex-1 items-center gap-2 rounded-xl px-3">
              <Search className="h-4 w-4 text-slate-400" />
              <input
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && submit()}
                placeholder={t('ai.searchPlaceholder')}
                className="min-w-0 flex-1 bg-transparent text-sm text-white placeholder:text-slate-500 focus:outline-none"
              />
              {input && (
                <button type="button" onClick={() => setInput('')} className="text-slate-400 hover:text-white">
                  <X className="h-3.5 w-3.5" />
                </button>
              )}
            </div>
            <button type="button" onClick={() => submit(false)} className="btn-ghost h-11 px-4 text-sm">
              {t('ai.normalSearch')}
            </button>
            <button type="button" onClick={() => submit(true)}
              className="flex h-11 items-center justify-center gap-1.5 rounded-xl bg-gradient-to-r from-nebula-500/80 to-cyan-glow/70 px-4 text-sm font-medium text-white shadow-inner-glow">
              <Sparkles className="h-4 w-4" />{t('ai.aiSearch')}
            </button>
          </div>
          <div className="mt-3 flex flex-wrap items-center gap-2 text-[11px] text-slate-400">
            <span className="rounded-lg bg-white/5 px-2 py-1">
              {aiMode ? t('ai.aiMode') : t('ai.normalMode')}
            </span>
            {status && (
              <span className="rounded-lg bg-white/5 px-2 py-1">
                {t('ai.currentMode')}: {status.mode === 'local' ? t('ai.modeLocal') : status.mode === 'plugin' ? t('ai.modePlugin') : t('ai.modeGateway')}
              </span>
            )}
            {!!filters && (
              <>
                {Object.entries(filters).filter(([, v]) => v !== '' && v !== 0 && v !== null && !Array.isArray(v))
                  .slice(0, 6).map(([k, v]) => (
                    <span key={k} className="rounded-lg bg-cyan-glow/10 px-2 py-1 text-cyan-glow">{k}: {String(v)}</span>
                  ))}
              </>
            )}
          </div>
        </div>

        {answer && (
          <div className="glass rounded-2xl border border-cyan-glow/20 p-4">
            <div className="mb-1 flex items-center gap-1.5 text-xs font-medium text-cyan-glow">
              <Sparkles className="h-3.5 w-3.5" />{t('ai.aiAnswer')} · {mode}
            </div>
            <p className="text-sm text-slate-200">{answer}</p>
          </div>
        )}

        <div className="grid gap-5 lg:grid-cols-[1fr_280px]">
          {/* 结果列表 */}
          <div className="glass-strong overflow-hidden rounded-2xl">
            <div className="flex items-center justify-between border-b border-white/10 px-4 py-3">
              <span className="text-sm font-semibold text-white">
                {t('ai.results')} · {total}
              </span>
              {loading && <Loader2 className="h-4 w-4 animate-spin text-cyan-glow" />}
            </div>
            {!loading && list.length === 0 && (
              <div className="px-4 py-12 text-center text-sm text-slate-500">{t('ai.noResults')}</div>
            )}
            <div className="divide-y divide-white/5">
              {list.map((f) => {
                const Icon = iconOf(f)
                const isText = TEXT_EXT.has((f.extension || '').toLowerCase())
                return (
                  <div key={f.id} className="group flex items-center gap-3 px-4 py-3 hover:bg-white/5">
                    <Icon className="h-5 w-5 shrink-0 text-cyan-glow" />
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm text-slate-100">{f.name}</div>
                      <div className="text-[11px] text-slate-500">
                        {formatSize(f.size)} · {new Date(f.updatedAt).toLocaleString()}
                      </div>
                    </div>
                    <div className="flex shrink-0 items-center gap-1 opacity-60 transition group-hover:opacity-100">
                      <button type="button" onClick={() => showSummary(f)} title={t('ai.summary')}
                        className="grid h-8 w-8 place-items-center rounded-lg hover:bg-white/10">
                        {busyId === f.id ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Tag className="h-3.5 w-3.5" />}
                      </button>
                      {status?.codeSpaceEnabled && isText && (
                        <button type="button" onClick={() => navigate(`/code?file=${f.id}`)} title={t('ai.codeSpace')}
                          className="grid h-8 w-8 place-items-center rounded-lg hover:bg-white/10">
                          <Code2 className="h-3.5 w-3.5" />
                        </button>
                      )}
                      <button type="button" onClick={() => download(f)} title={t('download') || '下载'}
                        className="grid h-8 w-8 place-items-center rounded-lg hover:bg-white/10">
                        <Download className="h-3.5 w-3.5" />
                      </button>
                    </div>
                  </div>
                )
              })}
            </div>
          </div>

          {/* 侧栏：近期常用 / 热门 */}
          <div className="space-y-4">
            <SideCard title={t('ai.recentUsed')} icon={<Clock className="h-3.5 w-3.5" />}
              files={recent} onPick={(f) => navigate(`/search?q=${encodeURIComponent(f.name)}`)} />
            <SideCard title={t('ai.hot')} icon={<Flame className="h-3.5 w-3.5" />}
              files={hot} onPick={(f) => navigate(`/search?q=${encodeURIComponent(f.name)}`)} />
          </div>
        </div>

        {summary && (
          <div className="glass-strong fixed bottom-6 right-6 z-40 w-[min(420px,90vw)] rounded-2xl border border-cyan-glow/20 p-4 shadow-glass-lg">
            <div className="mb-2 flex items-center justify-between">
              <span className="flex items-center gap-1.5 text-xs font-medium text-cyan-glow">
                <Sparkles className="h-3.5 w-3.5" />{t('ai.summary')} · {summary.provider}
              </span>
              <button type="button" onClick={() => setSummary(null)} className="text-slate-400 hover:text-white">
                <X className="h-3.5 w-3.5" />
              </button>
            </div>
            <p className="text-sm leading-relaxed text-slate-200">{summary.text || t('ai.noSummary')}</p>
            {!!summary.tags.length && (
              <div className="mt-3 flex flex-wrap gap-1.5">
                {summary.tags.map((tag) => (
                  <span key={tag} className="rounded-lg bg-white/10 px-2 py-0.5 text-[11px] text-slate-300">#{tag}</span>
                ))}
              </div>
            )}
          </div>
        )}
      </div>
    </MainLayout>
  )
}

function SideCard({ title, icon, files, onPick }: {
  title: string; icon: React.ReactNode; files: AIFileBrief[]; onPick: (f: AIFileBrief) => void
}) {
  return (
    <div className="glass rounded-2xl p-3">
      <div className="mb-2 flex items-center gap-1.5 px-1 text-[11px] font-medium text-slate-300">{icon}{title}</div>
      {files.length === 0 && <div className="px-1 py-3 text-[11px] text-slate-500">—</div>}
      <div className="space-y-0.5">
        {files.map((f) => (
          <button key={f.id} type="button" onClick={() => onPick(f)}
            className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-xs hover:bg-white/10">
            <span className="min-w-0 flex-1 truncate text-slate-200">{f.name}</span>
            <span className="shrink-0 text-[10px] text-slate-500">{formatSize(f.size)}</span>
          </button>
        ))}
      </div>
    </div>
  )
}
