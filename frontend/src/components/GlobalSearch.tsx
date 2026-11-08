import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Search, Sparkles, Clock, Flame, ArrowRight, X, Loader2, Command } from 'lucide-react'
import { api, type SuggestResult, type AIFileBrief } from '../api/client'

function formatSize(bytes: number): string {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let n = bytes
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++ }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

const MODE_LABEL: Record<string, string> = {
  local: '本地智能',
  gateway: 'AI 网关',
  plugin: '插件模型',
}

/**
 * 顶栏全局搜索框。
 *
 * 之前后端 /api/files/search 和搜索历史接口一直存在，但前端没有任何入口，
 * 这次补齐并叠加智能推荐：联想词、搜索历史、近期常用、热门、自然语言搜索。
 */
export default function GlobalSearch() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [q, setQ] = useState('')
  const [open, setOpen] = useState(false)
  const [loading, setLoading] = useState(false)
  const [data, setData] = useState<SuggestResult | null>(null)
  const boxRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  // 输入防抖拉推荐；空查询时也拉一次，用于展示「近期常用 / 热门」。
  useEffect(() => {
    if (!open) return
    let alive = true
    const timer = window.setTimeout(() => {
      setLoading(true)
      api.files.suggest(q, 8)
        .then((res) => { if (alive) setData(res.data) })
        .catch(() => { if (alive) setData(null) })
        .finally(() => { if (alive) setLoading(false) })
    }, q ? 220 : 0)
    return () => { alive = false; window.clearTimeout(timer) }
  }, [q, open])

  // Ctrl/Cmd + K 聚焦；Esc 关闭。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        inputRef.current?.focus()
        setOpen(true)
      }
      if (e.key === 'Escape') setOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // 点击外部关闭。
  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (boxRef.current && !boxRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [])

  const runSearch = (keyword: string, ai = false) => {
    const kw = keyword.trim()
    if (!kw) return
    setOpen(false)
    api.files.saveSearchHistory(kw).catch(() => {})
    navigate(`/search?q=${encodeURIComponent(kw)}${ai ? '&ai=1' : ''}`)
  }

  const openFile = (f: AIFileBrief) => {
    setOpen(false)
    navigate(`/search?q=${encodeURIComponent(f.name)}`)
  }

  const rows = data?.recent || []
  const hot = data?.hot || []

  return (
    <div ref={boxRef} className="relative min-w-0 flex-1 md:max-w-xl">
      <div className="glass flex h-10 items-center gap-2 rounded-xl px-3">
        <Search className="h-4 w-4 shrink-0 text-slate-400" />
        <input
          ref={inputRef}
          value={q}
          onChange={(e) => { setQ(e.target.value); setOpen(true) }}
          onFocus={() => setOpen(true)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') runSearch(q)
          }}
          placeholder={t('ai.searchPlaceholder')}
          className="min-w-0 flex-1 bg-transparent text-sm text-white placeholder:text-slate-500 focus:outline-none"
        />
        {q ? (
          <button type="button" onClick={() => { setQ(''); inputRef.current?.focus() }}
            className="text-slate-400 hover:text-white" aria-label="clear">
            <X className="h-3.5 w-3.5" />
          </button>
        ) : (
          <span className="hidden items-center gap-0.5 rounded border border-white/10 px-1.5 py-0.5 text-[10px] text-slate-500 sm:flex">
            <Command className="h-2.5 w-2.5" />K
          </span>
        )}
        {loading && <Loader2 className="h-3.5 w-3.5 animate-spin text-cyan-glow" />}
      </div>

      {open && (
        <div className="glass-strong animate-fade-in absolute left-0 right-0 top-12 z-50 max-h-[70vh] overflow-y-auto rounded-2xl border border-white/10 p-3 shadow-glass-lg">
          <div className="mb-2 flex items-center justify-between px-1">
            <span className="flex items-center gap-1.5 text-[11px] font-medium text-cyan-glow">
              <Sparkles className="h-3.5 w-3.5" />
              {t('ai.smartSearch')} · {MODE_LABEL[data?.mode || 'local'] || data?.mode}
            </span>
            <button type="button" onClick={() => runSearch(q, true)}
              className="flex items-center gap-1 rounded-lg bg-nebula-500/20 px-2 py-1 text-[11px] text-nebula-200 hover:bg-nebula-500/30">
              <Sparkles className="h-3 w-3" />{t('ai.askAI')}<ArrowRight className="h-3 w-3" />
            </button>
          </div>

          {/* 联想词 */}
          {!!data?.suggestions?.length && (
            <Section title={t('ai.suggestions')}>
              <div className="flex flex-wrap gap-1.5">
                {data.suggestions.map((s) => (
                  <button key={s} type="button" onClick={() => { setQ(s); runSearch(s) }}
                    className="max-w-full truncate rounded-lg bg-white/5 px-2.5 py-1.5 text-xs text-slate-200 hover:bg-white/15">
                    {s}
                  </button>
                ))}
              </div>
            </Section>
          )}

          {/* 搜索历史 */}
          {!!data?.history?.length && (
            <Section title={t('ai.history')} icon={<Clock className="h-3 w-3" />}>
              <div className="flex flex-wrap gap-1.5">
                {data.history.slice(0, 8).map((s) => (
                  <button key={s} type="button" onClick={() => { setQ(s); runSearch(s) }}
                    className="max-w-[180px] truncate rounded-lg px-2 py-1 text-xs text-slate-400 hover:bg-white/10 hover:text-white">
                    {s}
                  </button>
                ))}
              </div>
            </Section>
          )}

          {/* 近期常用 */}
          {!!rows.length && (
            <Section title={t('ai.recentUsed')} icon={<Clock className="h-3 w-3" />}>
              <FileRows files={rows} onPick={openFile} formatSize={formatSize} />
            </Section>
          )}

          {/* 热门 */}
          {!!hot.length && (
            <Section title={t('ai.hot')} icon={<Flame className="h-3 w-3" />}>
              <FileRows files={hot.slice(0, 5)} onPick={openFile} formatSize={formatSize} />
            </Section>
          )}

          {!loading && !data?.suggestions?.length && !rows.length && !hot.length && (
            <div className="px-2 py-6 text-center text-xs text-slate-500">
              {t('ai.noRecommend')}
            </div>
          )}

          <div className="mt-2 border-t border-white/10 px-1 pt-2 text-[10px] text-slate-500">
            {t('ai.searchHint')}
          </div>
        </div>
      )}
    </div>
  )
}

function Section({ title, icon, children }: { title: string; icon?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="mb-2.5">
      <div className="mb-1.5 flex items-center gap-1 px-1 text-[10px] uppercase tracking-wider text-slate-400">
        {icon}{title}
      </div>
      {children}
    </div>
  )
}

function FileRows({ files, onPick, formatSize }: {
  files: AIFileBrief[]
  onPick: (f: AIFileBrief) => void
  formatSize: (n: number) => string
}) {
  return (
    <div className="space-y-0.5">
      {files.map((f) => (
        <button key={f.id} type="button" onClick={() => onPick(f)}
          className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-xs hover:bg-white/10">
          <span className="min-w-0 flex-1 truncate text-slate-200">{f.name}</span>
          <span className="shrink-0 text-[10px] text-slate-500">{formatSize(f.size)}</span>
          {f.accessCount > 1 && (
            <span className="shrink-0 rounded bg-amber-400/10 px-1 text-[10px] text-amber-300">×{f.accessCount}</span>
          )}
        </button>
      ))}
    </div>
  )
}
