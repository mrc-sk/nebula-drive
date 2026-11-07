import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  AlertTriangle, Check, CheckCircle2, Eye, FileCode2, Info, Layers,
  LayoutGrid, Loader2, Palette, Power, Puzzle, ShieldAlert, SlidersHorizontal, X,
} from 'lucide-react'
import type {
  PluginContractDiag,
  PluginGlobalState,
  PluginUIData,
} from '../../api/client'

/**
 * 插件平台 v2 的后台面板集合。
 *
 * 单独成文件是有意的：Plugins.tsx 已经 700+ 行，把「总开关 / 风险弹窗 /
 * 契约诊断 / UI 扩展点」塞进去会让那个文件彻底不可维护。
 */

// ---- 全局总开关 ----

export function GlobalSwitchCard({
  state, busy, onTurnOn, onTurnOff,
}: {
  state: PluginGlobalState | null
  busy: boolean
  onTurnOn: () => void
  onTurnOff: () => void
}) {
  const { t } = useTranslation()
  const enabled = !!state?.enabled
  return (
    <section className={`glass rounded-2xl border p-4 ${
      enabled ? 'border-emerald-400/25' : 'border-white/10'
    }`}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <Power className={`h-4 w-4 ${enabled ? 'text-emerald-300' : 'text-slate-400'}`} />
            <h2 className="text-sm font-semibold text-white">{t('pluginV2.globalTitle')}</h2>
            <span className={`rounded-full border px-2 py-0.5 text-[10px] ${
              enabled
                ? 'border-emerald-400/25 bg-emerald-500/10 text-emerald-300'
                : 'border-white/10 bg-white/5 text-slate-400'
            }`}>
              {enabled ? t('pluginV2.globalOn') : t('pluginV2.globalOff')}
            </span>
          </div>
          <p className="mt-1.5 text-xs leading-relaxed text-slate-400">
            {t('pluginV2.globalHint')}
          </p>
          {state && (
            <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-slate-400">
              <span>{t('pluginV2.protocol')}: <b className="text-slate-200">v{state.protocolVersion}</b></span>
              <span>{t('pluginV2.hooksTotal')}: <b className="text-slate-200">{state.totalHooks}</b></span>
              <span>{t('pluginV2.hooksWired')}: <b className="text-emerald-300">{state.wiredHooks}</b></span>
              <span className="text-cyan-glow">{t('pluginV2.compatibleV1')}</span>
            </div>
          )}
        </div>
        <button
          type="button"
          onClick={enabled ? onTurnOff : onTurnOn}
          disabled={busy}
          className={enabled ? 'btn-ghost flex items-center gap-1.5' : 'btn-primary flex items-center gap-1.5'}
        >
          {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Power className="h-4 w-4" />}
          {enabled ? t('pluginV2.turnOff') : t('pluginV2.turnOn')}
        </button>
      </div>
    </section>
  )
}

// ---- 开启前风险确认弹窗 ----

export function PluginRiskModal({
  open, busy, onConfirm, onClose,
}: {
  open: boolean
  busy: boolean
  onConfirm: () => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [ack, setAck] = useState(false)

  useEffect(() => {
    if (open) setAck(false)
  }, [open])

  if (!open) return null
  const risks = ['risk1', 'risk2', 'risk3', 'risk4'] as const

  return (
    <div className="fixed inset-0 z-[110] flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/75 backdrop-blur-sm" onClick={busy ? undefined : onClose} />
      <div className="glass-strong relative z-10 w-full max-w-xl overflow-hidden rounded-2xl border border-amber-400/25 shadow-2xl">
        <div className="flex items-start gap-3 border-b border-white/10 px-6 py-4">
          <div className="grid h-10 w-10 shrink-0 place-items-center rounded-xl bg-amber-500/15">
            <ShieldAlert className="h-5 w-5 text-amber-300" />
          </div>
          <div className="min-w-0 flex-1">
            <h2 className="text-base font-semibold text-amber-200">{t('pluginV2.riskTitle')}</h2>
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            className="grid h-8 w-8 place-items-center rounded-lg text-slate-400 hover:bg-white/10 hover:text-white disabled:opacity-40"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="max-h-[60vh] overflow-y-auto px-6 py-5">
          <p className="mb-4 text-sm leading-relaxed text-slate-200">{t('pluginV2.riskIntro')}</p>
          <ul className="space-y-2.5">
            {risks.map((k) => (
              <li key={k} className="flex gap-2.5 text-xs leading-relaxed text-slate-300">
                <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-amber-300" />
                <span>{t(`pluginV2.${k}`)}</span>
              </li>
            ))}
          </ul>
        </div>

        <div className="border-t border-white/10 px-6 py-4">
          <label className="flex cursor-pointer items-start gap-2.5 text-xs text-slate-200">
            <input
              type="checkbox"
              checked={ack}
              disabled={busy}
              onChange={(e) => setAck(e.target.checked)}
              className="mt-0.5 h-4 w-4 shrink-0 rounded border-white/20 bg-white/5 accent-amber-400"
            />
            <span>{t('pluginV2.riskAck')}</span>
          </label>
          <div className="mt-4 flex justify-end gap-2">
            <button type="button" onClick={onClose} disabled={busy} className="btn-ghost">
              {t('common.cancel')}
            </button>
            <button
              type="button"
              onClick={onConfirm}
              disabled={!ack || busy}
              className="btn-primary flex items-center gap-1.5"
            >
              {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Check className="h-4 w-4" />}
              {t('pluginV2.riskConfirm')}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}

// ---- 三文件契约诊断 ----

export function ContractModal({
  name, data, onClose,
}: {
  name: string
  data: PluginContractDiag | null
  onClose: () => void
}) {
  const { t } = useTranslation()
  return (
    <div className="fixed inset-0 z-[105] flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/70 backdrop-blur-sm" onClick={onClose} />
      <div className="glass-strong relative z-10 flex max-h-[84vh] w-full max-w-2xl flex-col overflow-hidden rounded-2xl border border-white/15 shadow-2xl">
        <div className="flex items-start gap-3 border-b border-white/10 px-6 py-4">
          <div className="grid h-10 w-10 shrink-0 place-items-center rounded-xl bg-gradient-to-br from-nebula-500/30 to-cyan-glow/10 border border-white/10">
            <FileCode2 className="h-5 w-5 text-cyan-glow" />
          </div>
          <div className="min-w-0 flex-1">
            <h2 className="text-base font-semibold text-white">{t('pluginV2.contractTitle')}</h2>
            <p className="mt-0.5 truncate text-xs text-slate-400">{name}</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="grid h-8 w-8 place-items-center rounded-lg text-slate-400 hover:bg-white/10 hover:text-white"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-6 py-5 text-sm">
          {!data ? (
            <div className="grid h-24 place-items-center text-slate-400">
              <Loader2 className="h-6 w-6 animate-spin" />
            </div>
          ) : (
            <>
              <div className={`flex items-center gap-2 rounded-lg border px-3 py-2 text-xs ${
                data.ok
                  ? 'border-emerald-400/30 bg-emerald-500/10 text-emerald-300'
                  : 'border-rose-400/30 bg-rose-500/10 text-rose-300'
              }`}>
                {data.ok
                  ? <CheckCircle2 className="h-4 w-4 shrink-0" />
                  : <AlertTriangle className="h-4 w-4 shrink-0" />}
                <span>{data.ok ? t('pluginV2.contractOk') : t('pluginV2.contractBad')}</span>
                {data.v1 && <span className="ml-auto text-slate-400">{t('pluginV2.contractV1')}</span>}
              </div>

              {data.error && (
                <pre className="whitespace-pre-wrap break-all rounded-lg border border-rose-400/20 bg-rose-500/5 px-3 py-2 text-xs text-rose-200">
                  {data.error}
                </pre>
              )}

              <Row label={t('pluginV2.requiredFiles')} value={(data.required || []).join(' / ')} />
              <Row
                label={t('pluginV2.missingFiles')}
                value={(data.missingFiles || []).length ? (data.missingFiles || []).join(', ') : '—'}
              />
              <Row label={t('pluginV2.protocol')} value={data.protocolVersion ? `v${data.protocolVersion}` : '—'} />
              <Row label="runtime" value={data.runtime || '—'} />

              <div>
                <div className="mb-1.5 text-xs text-slate-400">{t('pluginV2.detectedInterpreters')}</div>
                {data.runtimeBins && Object.keys(data.runtimeBins).length > 0 ? (
                  <div className="space-y-1">
                    {Object.entries(data.runtimeBins).map(([bin, path]) => (
                      <div key={bin} className="glass flex items-center gap-2 rounded-lg px-3 py-1.5 text-xs">
                        <Check className="h-3.5 w-3.5 text-emerald-300" />
                        <span className="font-mono text-slate-200">{bin}</span>
                        <span className="truncate text-slate-400">{path}</span>
                      </div>
                    ))}
                  </div>
                ) : (
                  <div className="text-xs text-slate-500">{t('pluginV2.noInterpreter')}</div>
                )}
              </div>

              {Array.isArray(data.hooks) && data.hooks.length > 0 && (
                <div>
                  <div className="mb-1.5 text-xs text-slate-400">hooks ({data.hooks.length})</div>
                  <div className="flex flex-wrap gap-1.5">
                    {data.hooks.map((h) => (
                      <span key={h} className="rounded-md border border-white/10 bg-white/5 px-2 py-0.5 font-mono text-[11px] text-slate-300">
                        {h}
                      </span>
                    ))}
                  </div>
                </div>
              )}

              {Array.isArray(data.pages) && data.pages.length > 0 && (
                <div>
                  <div className="mb-1.5 text-xs text-slate-400">{t('pluginV2.pages')} ({data.pages.length})</div>
                  <div className="space-y-1">
                    {data.pages.map((p: any, i: number) => (
                      <div key={i} className="glass rounded-lg px-3 py-1.5 text-xs text-slate-300">
                        <span className="font-mono text-cyan-glow">{p?.name}</span>
                        <span className="mx-2 text-slate-500">·</span>
                        {p?.title || '—'}
                        <span className="mx-2 text-slate-500">·</span>
                        <span className="text-slate-400">{p?.area} / {p?.home || '—'}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {data.dir && (
                <div className="flex items-start gap-1.5 text-[11px] text-slate-500">
                  <Info className="mt-0.5 h-3 w-3 shrink-0" />
                  <span className="break-all font-mono">{data.dir}</span>
                </div>
              )}
            </>
          )}
        </div>

        <div className="border-t border-white/10 px-6 py-3 text-right">
          <button type="button" onClick={onClose} className="btn-ghost">{t('common.close')}</button>
        </div>
      </div>
    </div>
  )
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-start gap-3 text-xs">
      <span className="w-28 shrink-0 text-slate-400">{label}</span>
      <span className="min-w-0 break-all font-mono text-slate-200">{value}</span>
    </div>
  )
}

// ---- UI 扩展点 / 注册型能力总览 ----

export function UIExtensionsPanel({ data }: { data: PluginUIData | null }) {
  const { t } = useTranslation()
  const main = data?.sidebar.main || []
  const admin = data?.sidebar.admin || []
  const pub = data?.public || []
  const themes = Object.entries(data?.themes || {})
  const settings = Object.entries(data?.settings || {})
  const providers = data?.providers || []
  const empty =
    main.length + admin.length + pub.length + themes.length + settings.length + providers.length === 0

  if (empty) {
    return (
      <div className="glass grid h-40 place-items-center rounded-2xl text-sm text-slate-400">
        {t('pluginV2.noUIExtensions')}
      </div>
    )
  }

  return (
    <div className="grid gap-4 md:grid-cols-2">
      <Group title={t('pluginV2.uiTitle')} Icon={LayoutGrid} count={main.length + admin.length + pub.length}>
        <Sub title={t('pluginV2.sidebarMain')} items={[...main, ...pub].map((i) => `${i.title} · ${i.plugin}/${i.name}`)} />
        <Sub title={t('pluginV2.sidebarAdmin')} items={admin.map((i) => `${i.title} · ${i.plugin}/${i.name}`)} />
        <Sub title={t('pluginV2.themes')} items={themes.map(([p, th]) => th?.label || th?.name || p)} />
        <Sub title={t('pluginV2.settings')} items={settings.map(([p, f]) => `${p} (${(f || []).length})`)} />
      </Group>

      <Group title={t('pluginV2.providersTitle')} Icon={Layers} count={providers.length}>
        {providers.length === 0 ? (
          <div className="text-xs text-slate-500">{t('pluginV2.noProviders')}</div>
        ) : (
          <div className="space-y-1.5">
            {providers.map((p, i) => (
              <div key={`${p.kind}-${p.name}-${i}`} className="glass flex flex-wrap items-center gap-2 rounded-lg px-3 py-2 text-xs">
                <span className="rounded-md border border-cyan-glow/25 bg-cyan-glow/10 px-1.5 py-0.5 font-mono text-cyan-glow">
                  {p.kind}
                </span>
                <span className="font-mono text-slate-200">{p.name}</span>
                <span className="text-slate-500">·</span>
                <span className="truncate text-slate-400">{p.plugin}</span>
                {p.hook && <span className="ml-auto font-mono text-[11px] text-slate-500">{p.hook}</span>}
              </div>
            ))}
          </div>
        )}
      </Group>

      <div className="glass rounded-2xl p-4 md:col-span-2">
        <div className="mb-3 flex items-center gap-2 text-sm font-semibold text-white">
          <Eye className="h-4 w-4 text-cyan-glow" />
          {t('pluginV2.uiTitle')} · {t('pluginV2.protocol')} v{data?.protocolVersion ?? 2}
        </div>
        <div className="grid gap-2 text-xs text-slate-400 sm:grid-cols-3">
          <Badge Icon={Puzzle} label={t('pluginV2.pages')} value={main.length + admin.length + pub.length} />
          <Badge Icon={Palette} label={t('pluginV2.themes')} value={themes.length} />
          <Badge Icon={SlidersHorizontal} label={t('pluginV2.settings')} value={settings.length} />
        </div>
      </div>
    </div>
  )
}

function Group({
  title, Icon, count, children,
}: {
  title: string
  Icon: any
  count: number
  children: React.ReactNode
}) {
  return (
    <section className="glass rounded-2xl p-4">
      <div className="mb-3 flex items-center gap-2 text-sm font-semibold text-white">
        <Icon className="h-4 w-4 text-cyan-glow" />
        {title}
        <span className="ml-auto rounded-full border border-white/10 bg-white/5 px-2 py-0.5 text-[10px] text-slate-400">
          {count}
        </span>
      </div>
      <div className="space-y-3">{children}</div>
    </section>
  )
}

function Sub({ title, items }: { title: string; items: string[] }) {
  if (!items.length) return null
  return (
    <div>
      <div className="mb-1 text-[11px] uppercase tracking-wider text-slate-500">{title}</div>
      <div className="flex flex-wrap gap-1.5">
        {items.map((s, i) => (
          <span key={`${s}-${i}`} className="rounded-md border border-white/10 bg-white/5 px-2 py-0.5 text-[11px] text-slate-300">
            {s}
          </span>
        ))}
      </div>
    </div>
  )
}

function Badge({ Icon, label, value }: { Icon: any; label: string; value: number }) {
  return (
    <div className="glass flex items-center gap-2 rounded-xl px-3 py-2">
      <Icon className="h-4 w-4 text-nebula-300" />
      <span className="flex-1">{label}</span>
      <b className="text-white">{value}</b>
    </div>
  )
}
