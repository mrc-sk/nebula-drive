import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import CodeMirror from '@uiw/react-codemirror'
import { javascript } from '@codemirror/lang-javascript'
import { python } from '@codemirror/lang-python'
import { go } from '@codemirror/lang-go'
import { json } from '@codemirror/lang-json'
import { html } from '@codemirror/lang-html'
import { css } from '@codemirror/lang-css'
import { markdown } from '@codemirror/lang-markdown'
import { sql } from '@codemirror/lang-sql'
import { java } from '@codemirror/lang-java'
import { rust } from '@codemirror/lang-rust'
import { php } from '@codemirror/lang-php'
import { yaml } from '@codemirror/lang-yaml'
import { xml } from '@codemirror/lang-xml'
import { cpp } from '@codemirror/lang-cpp'
import {
  Code2, Save, Sparkles, FileCode, Search, Loader2, Lock, Tag, X,
  ChevronRight, CircleDot, Check, AlertCircle,
} from 'lucide-react'
import MainLayout from '../layouts/MainLayout'
import { api, type FileItem, type AIStatus } from '../api/client'

const TEXT_EXT = new Set([
  'txt', 'md', 'json', 'jsonc', 'js', 'jsx', 'ts', 'tsx', 'mjs', 'cjs',
  'go', 'py', 'java', 'c', 'h', 'cpp', 'cc', 'hpp', 'cs', 'rs', 'rb', 'php',
  'sh', 'bash', 'html', 'htm', 'css', 'scss', 'less', 'sql', 'yaml', 'yml',
  'xml', 'csv', 'toml', 'ini', 'conf', 'env', 'log', 'vue', 'svelte',
])

function isTextFile(f: { name: string; extension?: string; mimeType?: string; isDir?: boolean }) {
  if (f.isDir) return false
  const name = (f.name || '').toLowerCase()
  const ext = (f.extension || name.split('.').pop() || '').toLowerCase()
  return TEXT_EXT.has(ext) || name === 'dockerfile' || name === 'makefile' ||
    (f.mimeType || '').startsWith('text/')
}

function languageExtension(lang: string): any[] {
  switch (lang) {
    case 'javascript': return [javascript({ jsx: true })]
    case 'typescript': return [javascript({ jsx: true, typescript: true })]
    case 'python': return [python()]
    case 'go': return [go()]
    case 'json': return [json()]
    case 'html': return [html()]
    case 'css': return [css()]
    case 'markdown': return [markdown()]
    case 'sql': return [sql()]
    case 'java': return [java()]
    case 'rust': return [rust()]
    case 'php': return [php()]
    case 'yaml': return [yaml()]
    case 'xml': return [xml()]
    case 'c': case 'cpp': case 'csharp': return [cpp()]
    default: return []
  }
}

export default function CodeSpace() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const fileId = Number(params.get('file') || 0)

  const [status, setStatus] = useState<AIStatus | null>(null)
  const [statusLoading, setStatusLoading] = useState(true)
  const [fileList, setFileList] = useState<FileItem[]>([])
  const [filter, setFilter] = useState('')
  const [listLoading, setListLoading] = useState(false)

  const [content, setContent] = useState('')
  const [origin, setOrigin] = useState('')
  const [fileName, setFileName] = useState('')
  const [language, setLanguage] = useState('text')
  const [version, setVersion] = useState(0)
  const [loadingFile, setLoadingFile] = useState(false)
  const [saving, setSaving] = useState(false)
  const [completing, setCompleting] = useState(false)
  const [toast, setToast] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null)
  const [summary, setSummary] = useState<{ text: string; tags: string[]; provider: string } | null>(null)
  const [hint, setHint] = useState('')
  const viewRef = useRef<any>(null)

  useEffect(() => {
    api.ai.status()
      .then((r) => setStatus(r.data))
      .catch(() => setStatus(null))
      .finally(() => setStatusLoading(false))
  }, [])

  // 左栏文件列表：默认展示近期访问过的文本文件，输入关键词时改为全库检索。
  useEffect(() => {
    if (!status?.codeSpaceEnabled) return
    let alive = true
    setListLoading(true)
    const run = filter.trim()
      ? api.files.search({ keyword: filter.trim(), size: 40 }).then((r) => r.data.list || [])
      : Promise.all([
          // 最近访问过的文件（有埋点后才会有）
          api.files.recent(30).then((r) => r.data.map((x) => ({
            id: x.id, name: x.name, extension: x.extension, size: x.size,
            mimeType: x.mimeType, updatedAt: x.updatedAt, isDir: false,
          } as unknown as FileItem))).catch(() => [] as FileItem[]),
          // 根目录文件：新站点还没有任何埋点时，左栏也不能是空的
          api.files.list(null, false).then((r) => r.data || []).catch(() => [] as FileItem[]),
        ]).then(([recentRows, rootRows]) => {
          const seen = new Set<number>()
          return [...recentRows, ...rootRows].filter((f) => {
            if (seen.has(f.id)) return false
            seen.add(f.id)
            return true
          })
        })
    run.then((rows) => { if (alive) setFileList(rows.filter(isTextFile)) })
      .catch(() => { if (alive) setFileList([]) })
      .finally(() => { if (alive) setListLoading(false) })
    return () => { alive = false }
  }, [filter, status?.codeSpaceEnabled, fileId])

  // 载入文件内容。
  useEffect(() => {
    if (!fileId || !status?.codeSpaceEnabled) return
    let alive = true
    setLoadingFile(true)
    setSummary(null)
    api.files.getContent(fileId)
      .then((r) => {
        if (!alive) return
        setContent(r.data.content); setOrigin(r.data.content)
        setFileName(r.data.name); setLanguage(r.data.language)
        setVersion(r.data.version)
      })
      .catch((e) => { if (alive) setToast({ kind: 'err', text: String(e) }) })
      .finally(() => { if (alive) setLoadingFile(false) })
    return () => { alive = false }
  }, [fileId, status?.codeSpaceEnabled])

  const dirty = content !== origin

  const flash = useCallback((kind: 'ok' | 'err', text: string) => {
    setToast({ kind, text })
    window.setTimeout(() => setToast(null), 2600)
  }, [])

  const save = useCallback(async () => {
    if (!fileId || saving || !dirty) return
    setSaving(true)
    try {
      const r = await api.files.saveContent(fileId, content)
      setOrigin(content); setVersion(r.data.version)
      flash('ok', t('ai.saved'))
    } catch (e) { flash('err', String(e)) }
    finally { setSaving(false) }
  }, [fileId, content, dirty, saving, flash, t])

  // Ctrl/Cmd+S 保存，Alt+/ 或 Ctrl+Space 触发 AI 补全。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
        e.preventDefault(); save()
      }
      if (((e.ctrlKey || e.metaKey) && e.code === 'Space') || (e.altKey && e.key === '/')) {
        e.preventDefault(); complete()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  })

  async function complete() {
    const view = viewRef.current
    if (!view || completing) return
    setCompleting(true)
    try {
      const pos = view.state.selection.main.from
      const doc = view.state.doc.toString()
      const r = await api.ai.complete({
        fileId, fileName, language,
        prefix: doc.slice(Math.max(0, pos - 1200), pos),
        suffix: doc.slice(pos, pos + 300),
        doc: doc.slice(0, 200000),
        maxTokens: 256,
      })
      setHint(r.data.hint)
      const insert = r.data.text || (r.data.items[0]?.insertText ?? '')
      if (insert) {
        view.dispatch({
          changes: { from: pos, insert },
          selection: { anchor: pos + insert.length },
        })
        view.focus()
        flash('ok', `${t('ai.completed')} · ${r.data.mode}`)
      } else {
        flash('ok', t('ai.noCompletion'))
      }
    } catch (e) { flash('err', String(e)) }
    finally { setCompleting(false) }
  }

  async function showSummary() {
    if (!fileId) return
    try {
      const r = await api.ai.summary(fileId)
      setSummary({ text: r.data.summary, tags: r.data.tags, provider: r.data.provider })
    } catch (e) { flash('err', String(e)) }
  }

  const extensions = useMemo(() => languageExtension(language), [language])

  if (!statusLoading && !status?.codeSpaceEnabled) {
    return (
      <MainLayout breadcrumb={[{ name: t('ai.codeSpace') }]}>
        <div className="mx-auto max-w-lg p-10 text-center">
          <div className="glass-strong rounded-2xl p-8">
            <Lock className="mx-auto mb-3 h-8 w-8 text-slate-400" />
            <div className="text-sm font-medium text-slate-200">{t('ai.codeSpaceOff')}</div>
            <div className="mt-2 text-xs text-slate-500">{t('ai.codeSpaceOffTip')}</div>
            <button type="button" onClick={() => navigate('/admin/ai')} className="btn-ghost mt-4 text-xs">
              {t('ai.goSettings')}
            </button>
          </div>
        </div>
      </MainLayout>
    )
  }

  return (
    <MainLayout breadcrumb={[{ name: t('ai.codeSpace') }]}>
      <div className="flex h-[calc(100vh-9rem)] gap-3 p-3 md:p-5">
        {/* 文件列表 */}
        <aside className="glass-strong hidden w-64 shrink-0 flex-col overflow-hidden rounded-2xl md:flex">
          <div className="border-b border-white/10 p-3">
            <div className="mb-2 flex items-center gap-1.5 text-xs font-semibold text-slate-200">
              <FileCode className="h-3.5 w-3.5 text-cyan-glow" />{t('ai.codeFiles')}
            </div>
            <div className="glass flex h-9 items-center gap-2 rounded-lg px-2">
              <Search className="h-3.5 w-3.5 text-slate-400" />
              <input value={filter} onChange={(e) => setFilter(e.target.value)}
                placeholder={t('ai.filterFiles')}
                className="min-w-0 flex-1 bg-transparent text-xs text-white placeholder:text-slate-500 focus:outline-none" />
              {listLoading && <Loader2 className="h-3 w-3 animate-spin text-cyan-glow" />}
            </div>
          </div>
          <div className="flex-1 overflow-y-auto p-2">
            {!listLoading && fileList.length === 0 && (
              <div className="px-2 py-6 text-center text-[11px] text-slate-500">{t('ai.noCodeFiles')}</div>
            )}
            {fileList.map((f) => (
              <button key={f.id} type="button" onClick={() => setParams({ file: String(f.id) })}
                className={`mb-0.5 flex w-full items-center gap-2 rounded-lg px-2 py-2 text-left text-xs transition ${
                  f.id === fileId ? 'bg-nebula-500/25 text-white' : 'text-slate-300 hover:bg-white/10'
                }`}>
                <FileCode className="h-3.5 w-3.5 shrink-0 text-cyan-glow" />
                <span className="min-w-0 flex-1 truncate">{f.name}</span>
                <ChevronRight className="h-3 w-3 shrink-0 opacity-40" />
              </button>
            ))}
          </div>
        </aside>

        {/* 编辑器 */}
        <div className="glass-strong flex min-w-0 flex-1 flex-col overflow-hidden rounded-2xl">
          <div className="flex flex-wrap items-center gap-2 border-b border-white/10 px-3 py-2">
            <div className="flex min-w-0 items-center gap-2">
              <Code2 className="h-4 w-4 shrink-0 text-cyan-glow" />
              <span className="truncate text-sm font-medium text-white">
                {fileName || t('ai.noFileSelected')}
              </span>
              {dirty && <CircleDot className="h-3 w-3 shrink-0 text-amber-400" />}
              {!!fileName && (
                <span className="shrink-0 rounded bg-white/5 px-1.5 py-0.5 text-[10px] text-slate-400">
                  {language} · v{version}
                </span>
              )}
            </div>
            <div className="ml-auto flex items-center gap-1">
              <button type="button" onClick={showSummary} disabled={!fileId}
                title={t('ai.summary')}
                className="flex h-8 items-center gap-1 rounded-lg px-2 text-xs text-slate-300 hover:bg-white/10 disabled:opacity-40">
                <Tag className="h-3.5 w-3.5" /><span className="hidden sm:inline">{t('ai.summary')}</span>
              </button>
              <button type="button" onClick={complete} disabled={!fileId || completing}
                title="Alt + /"
                className="flex h-8 items-center gap-1 rounded-lg px-2 text-xs text-cyan-glow hover:bg-white/10 disabled:opacity-40">
                {completing ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Sparkles className="h-3.5 w-3.5" />}
                <span className="hidden sm:inline">{t('ai.complete')}</span>
              </button>
              <button type="button" onClick={save} disabled={!fileId || !dirty || saving}
                className="flex h-8 items-center gap-1 rounded-lg bg-gradient-to-r from-nebula-500/80 to-cyan-glow/70 px-3 text-xs font-medium text-white disabled:opacity-40">
                {saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}
                {t('ai.save')}
              </button>
            </div>
          </div>

          <div className="relative flex-1 overflow-hidden">
            {loadingFile && (
              <div className="absolute inset-0 z-10 grid place-items-center bg-slate-900/60">
                <Loader2 className="h-6 w-6 animate-spin text-cyan-glow" />
              </div>
            )}
            {!fileId ? (
              <div className="grid h-full place-items-center text-sm text-slate-500">
                {t('ai.pickFile')}
              </div>
            ) : (
              <CodeMirror
                value={content}
                height="100%"
                theme="dark"
                extensions={extensions}
                onChange={setContent}
                onCreateEditor={(view) => { viewRef.current = view }}
                basicSetup={{ foldGutter: true, highlightActiveLine: true, autocompletion: true }}
                style={{ height: '100%', fontSize: '13px' }}
              />
            )}
          </div>

          <div className="flex items-center justify-between border-t border-white/10 px-3 py-1.5 text-[10px] text-slate-500">
            <span>{t('ai.editorHint')}</span>
            <span>{content.length} {t('ai.chars')}{hint ? ` · ${hint}` : ''}</span>
          </div>
        </div>

        {/* 摘要侧栏 */}
        {summary && (
          <div className="glass-strong absolute bottom-6 right-6 z-40 w-[min(380px,88vw)] rounded-2xl border border-cyan-glow/20 p-4 shadow-glass-lg">
            <div className="mb-2 flex items-center justify-between">
              <span className="flex items-center gap-1.5 text-xs font-medium text-cyan-glow">
                <Sparkles className="h-3.5 w-3.5" />{t('ai.summary')} · {summary.provider}
              </span>
              <button type="button" onClick={() => setSummary(null)} className="text-slate-400 hover:text-white">
                <X className="h-3.5 w-3.5" />
              </button>
            </div>
            <p className="text-xs leading-relaxed text-slate-200">{summary.text || t('ai.noSummary')}</p>
            {!!summary.tags.length && (
              <div className="mt-2 flex flex-wrap gap-1.5">
                {summary.tags.map((tag) => (
                  <span key={tag} className="rounded bg-white/10 px-1.5 py-0.5 text-[10px] text-slate-300">#{tag}</span>
                ))}
              </div>
            )}
          </div>
        )}

        {toast && (
          <div className={`absolute bottom-6 left-1/2 z-50 flex -translate-x-1/2 items-center gap-2 rounded-xl px-4 py-2 text-xs shadow-glass-lg ${
            toast.kind === 'ok' ? 'bg-emerald-500/20 text-emerald-200' : 'bg-rose-500/20 text-rose-200'
          }`}>
            {toast.kind === 'ok' ? <Check className="h-3.5 w-3.5" /> : <AlertCircle className="h-3.5 w-3.5" />}
            {toast.text}
          </div>
        )}
      </div>
    </MainLayout>
  )
}
