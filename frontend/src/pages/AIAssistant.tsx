import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Sparkles, Send, Loader2, Bot, User, FileText, RotateCcw, Settings2 } from 'lucide-react'
import MainLayout from '../layouts/MainLayout'
import { api, type AIStatus, type FileItem } from '../api/client'

interface Msg {
  role: 'user' | 'assistant'
  text: string
  mode?: string
  refs?: FileItem[]
}

const QUICK = [
  '我最近改过哪些文件？',
  '找出所有大于 10MB 的 PDF',
  '列出最近一周的代码文件',
  '帮我按名字排序文件',
]

export default function AIAssistant() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [status, setStatus] = useState<AIStatus | null>(null)
  const [msgs, setMsgs] = useState<Msg[]>([])
  const [input, setInput] = useState('')
  const [busy, setBusy] = useState(false)
  const bottomRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    api.ai.status().then((r) => setStatus(r.data)).catch(() => {})
  }, [])
  useEffect(() => { bottomRef.current?.scrollIntoView({ behavior: 'smooth' }) }, [msgs, busy])

  const send = async (question?: string) => {
    const q = (question ?? input).trim()
    if (!q || busy) return
    setInput('')
    setMsgs((m) => [...m, { role: 'user', text: q }])
    setBusy(true)
    try {
      const history = msgs.filter((m) => m.role === 'assistant').slice(-6).map((m) => m.text)
      const r = await api.ai.ask(q, history)
      setMsgs((m) => [...m, {
        role: 'assistant', text: r.data.answer, mode: r.data.mode, refs: r.data.refs,
      }])
    } catch (e) {
      setMsgs((m) => [...m, { role: 'assistant', text: `${t('ai.askFailed')}: ${String(e)}`, mode: 'error' }])
    } finally { setBusy(false) }
  }

  const modeLabel = status?.mode === 'gateway' ? t('ai.modeGateway')
    : status?.mode === 'plugin' ? t('ai.modePlugin') : t('ai.modeLocal')

  return (
    <MainLayout breadcrumb={[{ name: t('ai.assistant') }]}>
      <div className="mx-auto flex h-[calc(100vh-9rem)] max-w-4xl flex-col p-4 md:p-6">
        <div className="glass-strong flex flex-1 flex-col overflow-hidden rounded-2xl">
          {/* 头部 */}
          <div className="flex items-center justify-between border-b border-white/10 px-4 py-3">
            <div className="flex items-center gap-2">
              <div className="grid h-8 w-8 place-items-center rounded-xl bg-gradient-to-br from-nebula-500/80 to-cyan-glow/70">
                <Sparkles className="h-4 w-4 text-white" />
              </div>
              <div>
                <div className="text-sm font-semibold text-white">{t('ai.assistant')}</div>
                <div className="text-[10px] text-slate-400">{t('ai.currentMode')}: {modeLabel}{status?.model ? ` · ${status.model}` : ''}</div>
              </div>
            </div>
            <div className="flex items-center gap-1">
              <button type="button" onClick={() => setMsgs([])} title={t('ai.clearChat')}
                className="grid h-8 w-8 place-items-center rounded-lg text-slate-400 hover:bg-white/10 hover:text-white">
                <RotateCcw className="h-3.5 w-3.5" />
              </button>
              <button type="button" onClick={() => navigate('/admin/ai')} title={t('ai.settings')}
                className="grid h-8 w-8 place-items-center rounded-lg text-slate-400 hover:bg-white/10 hover:text-white">
                <Settings2 className="h-3.5 w-3.5" />
              </button>
            </div>
          </div>

          {/* 消息区 */}
          <div className="flex-1 space-y-4 overflow-y-auto p-4">
            {msgs.length === 0 && (
              <div className="flex h-full flex-col items-center justify-center gap-4 text-center">
                <div className="grid h-14 w-14 place-items-center rounded-2xl bg-gradient-to-br from-nebula-500/30 to-cyan-glow/20">
                  <Bot className="h-7 w-7 text-cyan-glow" />
                </div>
                <div>
                  <div className="text-sm font-medium text-slate-200">{t('ai.assistantWelcome')}</div>
                  <div className="mt-1 max-w-md text-xs leading-relaxed text-slate-500">{t('ai.assistantTip')}</div>
                </div>
                <div className="flex max-w-lg flex-wrap justify-center gap-2">
                  {QUICK.map((q) => (
                    <button key={q} type="button" onClick={() => send(q)}
                      className="rounded-xl border border-white/10 bg-white/5 px-3 py-1.5 text-xs text-slate-300 hover:bg-white/10">
                      {q}
                    </button>
                  ))}
                </div>
                {status?.mode === 'local' && (
                  <div className="mt-2 rounded-xl bg-amber-400/10 px-3 py-2 text-[11px] text-amber-200">
                    {t('ai.localModeTip')}
                  </div>
                )}
              </div>
            )}
            {msgs.map((m, i) => (
              <div key={i} className={`flex gap-3 ${m.role === 'user' ? 'flex-row-reverse' : ''}`}>
                <div className={`grid h-8 w-8 shrink-0 place-items-center rounded-xl ${
                  m.role === 'user' ? 'bg-white/10' : 'bg-gradient-to-br from-nebula-500/50 to-cyan-glow/40'
                }`}>
                  {m.role === 'user' ? <User className="h-4 w-4" /> : <Bot className="h-4 w-4" />}
                </div>
                <div className={`max-w-[80%] rounded-2xl px-4 py-2.5 text-sm leading-relaxed ${
                  m.role === 'user' ? 'bg-nebula-500/25 text-white' : 'glass text-slate-200'
                }`}>
                  <div className="whitespace-pre-wrap">{m.text}</div>
                  {m.mode && m.role === 'assistant' && (
                    <div className="mt-1.5 text-[10px] text-slate-500">{t('ai.currentMode')}: {m.mode}</div>
                  )}
                  {!!m.refs?.length && (
                    <div className="mt-2 flex flex-wrap gap-1.5 border-t border-white/10 pt-2">
                      {m.refs.slice(0, 8).map((f) => (
                        <button key={f.id} type="button"
                          onClick={() => navigate(`/search?q=${encodeURIComponent(f.name)}`)}
                          className="flex items-center gap-1 rounded-lg bg-white/5 px-2 py-1 text-[11px] text-slate-300 hover:bg-white/15">
                          <FileText className="h-3 w-3" />{f.name}
                        </button>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            ))}
            {busy && (
              <div className="flex items-center gap-2 text-xs text-slate-400">
                <Loader2 className="h-3.5 w-3.5 animate-spin" />{t('ai.thinking')}
              </div>
            )}
            <div ref={bottomRef} />
          </div>

          {/* 输入区 */}
          <div className="border-t border-white/10 p-3">
            <div className="glass flex items-end gap-2 rounded-xl p-2">
              <textarea
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() }
                }}
                rows={1}
                placeholder={t('ai.askPlaceholder')}
                className="max-h-28 min-h-[36px] flex-1 resize-none bg-transparent px-2 py-2 text-sm text-white placeholder:text-slate-500 focus:outline-none"
              />
              <button type="button" onClick={() => send()} disabled={busy || !input.trim()}
                className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-gradient-to-br from-nebula-500 to-cyan-glow text-white disabled:opacity-40">
                {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}
              </button>
            </div>
            <div className="mt-2 px-1 text-[10px] text-slate-500">{t('ai.assistantPrivacy')}</div>
          </div>
        </div>
      </div>
    </MainLayout>
  )
}
