import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Sparkles, Save, Loader2, Check, AlertCircle, Zap, ShieldCheck,
  Code2, Search, Tag, MessageSquare, FileText, Activity, RefreshCw,
} from 'lucide-react'
import { api, type AIConfig } from '../../api/client'

const SCENES = [
  { key: 'search', labelKey: 'ai.sceneSearch', Icon: Search },
  { key: 'recommend', labelKey: 'ai.sceneRecommend', Icon: Zap },
  { key: 'complete', labelKey: 'ai.sceneComplete', Icon: Code2 },
  { key: 'summary', labelKey: 'ai.sceneSummary', Icon: FileText },
  { key: 'ask', labelKey: 'ai.sceneAsk', Icon: MessageSquare },
] as const

const MODE_TEXT: Record<string, string> = {
  local: '本地智能（不调用模型）',
  gateway: 'AI 网关（内置 OpenAI 兼容端点）',
  plugin: '插件模型（社区 ai Provider 接管）',
}

export default function AISettings() {
  const { t } = useTranslation()
  const [cfg, setCfg] = useState<AIConfig | null>(null)
  const [form, setForm] = useState<Partial<AIConfig>>({})
  const [usage, setUsage] = useState<any>(null)
  const [logs, setLogs] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [toast, setToast] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null)

  const load = () => {
    setLoading(true)
    Promise.all([
      api.ai.getConfig(),
      api.ai.usage().catch(() => null),
      api.ai.logs(20).catch(() => ({ data: [] })),
    ]).then(([c, u, l]) => {
      setCfg(c.data); setForm(c.data)
      setUsage(u?.data || null); setLogs(l.data || [])
    }).catch((e) => flash('err', String(e)))
      .finally(() => setLoading(false))
  }
  useEffect(load, [])

  function flash(kind: 'ok' | 'err', text: string) {
    setToast({ kind, text })
    window.setTimeout(() => setToast(null), 3200)
  }

  const set = <K extends keyof AIConfig>(key: K, value: AIConfig[K]) =>
    setForm((f) => ({ ...f, [key]: value }))

  const save = async () => {
    setSaving(true)
    try {
      const r = await api.ai.saveConfig(form)
      setCfg(r.data); setForm(r.data)
      flash('ok', t('ai.configSaved'))
    } catch (e) { flash('err', String(e)) }
    finally { setSaving(false) }
  }

  const test = async () => {
    setTesting(true)
    try {
      const r = await api.ai.testConfig(form)
      flash('ok', `${t('ai.testOk')}: ${r.data.reply}`)
    } catch (e) { flash('err', String(e)) }
    finally { setTesting(false) }
  }

  if (loading || !cfg) {
    return <div className="grid h-64 place-items-center"><Loader2 className="h-6 w-6 animate-spin text-cyan-glow" /></div>
  }

  return (
    <div className="space-y-5 p-1">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-2xl font-semibold text-white">
            <Sparkles className="h-6 w-6 text-cyan-glow" />{t('ai.settings')}
          </h1>
          <p className="mt-1 text-sm text-slate-400">{t('ai.settingsSubtitle')}</p>
        </div>
        <div className="flex items-center gap-2">
          <button type="button" onClick={load} className="btn-ghost flex h-9 items-center gap-1.5 text-xs">
            <RefreshCw className="h-3.5 w-3.5" />{t('ai.refresh')}
          </button>
          <button type="button" onClick={test} disabled={testing}
            className="btn-ghost flex h-9 items-center gap-1.5 text-xs">
            {testing ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Zap className="h-3.5 w-3.5" />}
            {t('ai.test')}
          </button>
          <button type="button" onClick={save} disabled={saving}
            className="flex h-9 items-center gap-1.5 rounded-xl bg-gradient-to-r from-nebula-500/80 to-cyan-glow/70 px-4 text-xs font-medium text-white disabled:opacity-40">
            {saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}
            {t('ai.save')}
          </button>
        </div>
      </div>

      {/* 当前模式 */}
      <div className="glass flex flex-wrap items-center gap-3 rounded-2xl p-4">
        <div className="grid h-9 w-9 place-items-center rounded-xl bg-gradient-to-br from-nebula-500/40 to-cyan-glow/30">
          <Activity className="h-4 w-4 text-cyan-glow" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="text-sm font-medium text-white">{MODE_TEXT[cfg.mode] || cfg.mode}</div>
          <div className="text-[11px] text-slate-400">{t('ai.modeTip')}</div>
        </div>
        <span className={`rounded-lg px-2 py-1 text-[11px] ${cfg.enabled ? 'bg-emerald-400/15 text-emerald-300' : 'bg-slate-500/15 text-slate-400'}`}>
          {cfg.enabled ? t('ai.enabled') : t('ai.disabled')}
        </span>
      </div>

      <div className="grid gap-4 lg:grid-cols-[1fr_320px]">
        <div className="space-y-4">
          {/* 总开关 */}
          <Section title={t('ai.general')}>
            <Toggle label={t('ai.enableAI')} desc={t('ai.enableAITip')}
              checked={!!form.enabled} onChange={(v) => set('enabled', v)} />
            <Toggle label={t('ai.enableCodeSpace')} desc={t('ai.enableCodeSpaceTip')}
              checked={!!form.codeSpaceEnabled} onChange={(v) => set('codeSpaceEnabled', v)} />
            <div className="mt-3 grid gap-3 sm:grid-cols-2">
              <Toggle label={t('ai.autoSummary')} desc={t('ai.autoSummaryTip')}
                checked={!!form.autoSummary} onChange={(v) => set('autoSummary', v)} />
              <Toggle label={t('ai.autoTag')} desc={t('ai.autoTagTip')}
                checked={!!form.autoTag} onChange={(v) => set('autoTag', v)} />
            </div>
          </Section>

          {/* 模型接入 */}
          <Section title={t('ai.modelSection')}>
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label={t('ai.baseUrl')} hint={t('ai.baseUrlTip')}>
                <input className="glass-input w-full" value={form.baseUrl || ''}
                  onChange={(e) => set('baseUrl', e.target.value)} placeholder="https://api.openai.com/v1" />
              </Field>
              <Field label={t('ai.model')}>
                <input className="glass-input w-full" value={form.model || ''}
                  onChange={(e) => set('model', e.target.value)} placeholder="gpt-4o-mini" />
              </Field>
              <Field label={t('ai.apiKey')} hint={cfg.apiKeySet ? t('ai.apiKeySet') : t('ai.apiKeyEmpty')}>
                <input className="glass-input w-full" type="password" value={form.apiKey || ''}
                  onChange={(e) => set('apiKey', e.target.value)} placeholder="sk-..." autoComplete="off" />
              </Field>
              <Field label={t('ai.provider')} hint={t('ai.providerTip')}>
                <select className="glass-input w-full" value={form.provider || ''}
                  onChange={(e) => set('provider', e.target.value)}>
                  <option value="">{t('ai.builtinGateway')}</option>
                  {(cfg.providers || []).map((p) => <option key={p} value={p}>{p}</option>)}
                </select>
              </Field>
              <Field label={t('ai.timeoutSec')}>
                <input className="glass-input w-full" type="number" min={1} max={300}
                  value={form.timeoutSec ?? 30} onChange={(e) => set('timeoutSec', Number(e.target.value))} />
              </Field>
              <Field label={t('ai.maxTokens')}>
                <input className="glass-input w-full" type="number" min={1} max={8192}
                  value={form.maxTokens ?? 1024} onChange={(e) => set('maxTokens', Number(e.target.value))} />
              </Field>
              <Field label={t('ai.temperature')}>
                <input className="glass-input w-full" type="number" step="0.1" min={0} max={2}
                  value={form.temperature ?? 0.3} onChange={(e) => set('temperature', Number(e.target.value))} />
              </Field>
              <Field label={t('ai.rateLimit')} hint={t('ai.rateLimitTip')}>
                <input className="glass-input w-full" type="number" min={1} max={1000}
                  value={form.rateLimit ?? 30} onChange={(e) => set('rateLimit', Number(e.target.value))} />
              </Field>
            </div>
            <div className="mt-3 flex items-start gap-2 rounded-xl bg-amber-400/10 p-3 text-[11px] leading-relaxed text-amber-200">
              <ShieldCheck className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              {t('ai.privacyNote')}
            </div>
          </Section>

          {/* 场景开关 */}
          <Section title={t('ai.scenes')}>
            <div className="grid gap-2 sm:grid-cols-2">
              {SCENES.map(({ key, labelKey, Icon }) => (
                <button key={key} type="button"
                  onClick={() => set('scenes', { ...(form.scenes || {}), [key]: !(form.scenes?.[key] ?? true) })}
                  className={`flex items-center gap-2 rounded-xl border px-3 py-2.5 text-left text-xs transition ${
                    form.scenes?.[key] ?? true
                      ? 'border-cyan-glow/30 bg-cyan-glow/10 text-slate-100'
                      : 'border-white/10 bg-white/5 text-slate-400'
                  }`}>
                  <Icon className="h-4 w-4 shrink-0" />
                  <span className="flex-1">{t(labelKey)}</span>
                  <span className={`h-2 w-2 rounded-full ${(form.scenes?.[key] ?? true) ? 'bg-emerald-400' : 'bg-slate-600'}`} />
                </button>
              ))}
            </div>
          </Section>
        </div>

        {/* 右栏：用量与日志 */}
        <div className="space-y-4">
          <div className="glass rounded-2xl p-4">
            <div className="mb-3 flex items-center gap-1.5 text-sm font-medium text-white">
              <Activity className="h-4 w-4 text-cyan-glow" />{t('ai.usage')}
            </div>
            <div className="grid grid-cols-2 gap-2">
              <Stat label={t('ai.totalCalls')} value={usage?.total ?? 0} />
              <Stat label={t('ai.success')} value={usage?.success ?? 0} />
              <Stat label={t('ai.tokens')} value={usage?.tokens ?? 0} />
              <Stat label={t('ai.last7Days')} value={usage?.last7Days ?? 0} />
              <Stat label={t('ai.failed')} value={usage?.failed ?? 0} />
              <Stat label={t('ai.avgLatency')} value={`${usage?.avgLatencyMs ?? 0}ms`} />
            </div>
            {!!usage?.byScene && (
              <div className="mt-3 border-t border-white/10 pt-2 text-[10px] text-slate-400">
                {Object.entries(usage.byScene).map(([k, v]) => (
                  <span key={k} className="mr-2 inline-block">{k}: {String(v)}</span>
                ))}
              </div>
            )}
          </div>

          <div className="glass rounded-2xl p-4">
            <div className="mb-2 flex items-center justify-between">
              <span className="flex items-center gap-1.5 text-sm font-medium text-white">
                <Tag className="h-4 w-4 text-cyan-glow" />{t('ai.recentLogs')}
              </span>
              <button type="button" onClick={load} className="text-[10px] text-slate-400 hover:text-white">
                {t('ai.refresh')}
              </button>
            </div>
            <div className="max-h-72 space-y-1 overflow-y-auto">
              {logs.length === 0 && <div className="py-4 text-center text-[11px] text-slate-500">{t('ai.noLogs')}</div>}
              {logs.map((l) => (
                <div key={l.id} className="flex items-center gap-2 rounded-lg bg-white/5 px-2 py-1.5 text-[10px]">
                  <span className={l.success ? 'text-emerald-400' : 'text-rose-400'}>
                    {l.success ? '●' : '●'}
                  </span>
                  <span className="w-16 shrink-0 truncate text-slate-300">{l.scene}</span>
                  <span className="min-w-0 flex-1 truncate text-slate-500">{l.provider}/{l.model}</span>
                  <span className="shrink-0 text-slate-500">{l.latencyMs}ms</span>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>

      {toast && (
        <div className={`fixed bottom-6 left-1/2 z-50 flex -translate-x-1/2 items-center gap-2 rounded-xl px-4 py-2 text-xs shadow-glass-lg ${
          toast.kind === 'ok' ? 'bg-emerald-500/20 text-emerald-200' : 'bg-rose-500/20 text-rose-200'
        }`}>
          {toast.kind === 'ok' ? <Check className="h-3.5 w-3.5" /> : <AlertCircle className="h-3.5 w-3.5" />}
          {toast.text}
        </div>
      )}
    </div>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="glass-strong rounded-2xl p-4">
      <div className="mb-3 text-sm font-semibold text-white">{title}</div>
      {children}
    </div>
  )
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <div className="mb-1 flex items-center justify-between text-[11px] text-slate-300">
        <span>{label}</span>{hint && <span className="text-[10px] text-slate-500">{hint}</span>}
      </div>
      {children}
    </label>
  )
}

function Toggle({ label, desc, checked, onChange }: {
  label: string; desc?: string; checked: boolean; onChange: (v: boolean) => void
}) {
  return (
    <button type="button" onClick={() => onChange(!checked)}
      className="flex w-full items-center gap-3 rounded-xl px-1 py-2 text-left">
      <span className={`relative h-5 w-9 shrink-0 rounded-full transition ${checked ? 'bg-nebula-500' : 'bg-slate-600'}`}>
        <span className={`absolute top-0.5 h-4 w-4 rounded-full bg-white transition-all ${checked ? 'left-[18px]' : 'left-0.5'}`} />
      </span>
      <span className="min-w-0">
        <span className="block text-xs font-medium text-slate-100">{label}</span>
        {desc && <span className="block text-[10px] text-slate-500">{desc}</span>}
      </span>
    </button>
  )
}

function Stat({ label, value }: { label: string; value: any }) {
  return (
    <div className="rounded-xl bg-white/5 px-3 py-2">
      <div className="text-[10px] text-slate-500">{label}</div>
      <div className="text-sm font-semibold text-white">{String(value)}</div>
    </div>
  )
}
