import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  AlertTriangle, CheckCircle2, Download, ExternalLink, Loader2, Package, Plug, Power,
  Puzzle, RefreshCw, ScrollText, Square, Store, Trash2, XCircle,
} from 'lucide-react'
import {
  api,
  type AgreementRecord, type HookInfo, type PluginLogEntry, type PluginView,
  type StorePlugin,
} from '../../api/client'
import PluginAgreementModal from './PluginAgreementModal'

type Tab = 'installed' | 'store' | 'hooks'

const STATUS_STYLE: Record<string, { cls: string; icon: any }> = {
  running: { cls: 'bg-emerald-500/15 text-emerald-300 border-emerald-400/25', icon: CheckCircle2 },
  crashed: { cls: 'bg-rose-500/15 text-rose-300 border-rose-400/25', icon: AlertTriangle },
  failed: { cls: 'bg-rose-500/15 text-rose-300 border-rose-400/25', icon: XCircle },
  stopped: { cls: 'bg-slate-500/15 text-slate-400 border-white/10', icon: Square },
  disabled: { cls: 'bg-slate-500/15 text-slate-400 border-white/10', icon: Square },
  starting: { cls: 'bg-amber-500/15 text-amber-300 border-amber-400/25', icon: Loader2 },
  stopping: { cls: 'bg-amber-500/15 text-amber-300 border-amber-400/25', icon: Loader2 },
}

export default function Plugins() {
  const { t } = useTranslation()
  const [tab, setTab] = useState<Tab>('installed')
  const [loading, setLoading] = useState(true)
  const [rows, setRows] = useState<PluginView[]>([])
  const [hooks, setHooks] = useState<HookInfo[]>([])
  const [store, setStore] = useState<StorePlugin[]>([])
  const [meta, setMeta] = useState<{ pluginsDir?: string; hostReady?: boolean }>({})
  const [busy, setBusy] = useState<string>('')
  const [msg, setMsg] = useState<{ type: 'ok' | 'err'; text: string } | null>(null)
  const [logs, setLogs] = useState<PluginLogEntry[]>([])
  const [logFor, setLogFor] = useState('')

  // 协议弹窗
  const [agreement, setAgreement] = useState<any>(null)
  const [needAgree, setNeedAgree] = useState(false)

  // 卸载确认
  const [uninstalling, setUninstalling] = useState<PluginView | null>(null)
  const [confirmPwd, setConfirmPwd] = useState('')
  const [confirmErr, setConfirmErr] = useState('')

  const load = useCallback(async () => {
    try {
      const [p, h, s, ag] = await Promise.allSettled([
        api.admin.plugins(),
        api.admin.pluginsHooks(),
        api.admin.pluginsStore(),
        api.admin.pluginAgreement(),
      ])

      if (p.status === 'fulfilled' && p.value?.code === 0) {
        setRows(p.value.data || [])
        setMeta({ hostReady: p.value.meta?.hostReady, pluginsDir: p.value.meta?.pluginsDir })
      }
      if (h.status === 'fulfilled' && h.value?.code === 0) setHooks(h.value.data || [])
      if (s.status === 'fulfilled' && s.value?.code === 0) setStore(s.value.data || [])

      // 首次进入或协议升级 → 弹协议
      if (ag.status === 'fulfilled' && ag.value?.code === 0) {
        const st = ag.value.data
        setAgreement(st?.agreement || null)
        setNeedAgree(!st?.accepted)
      }
    } catch (e: any) {
      setMsg({ type: 'err', text: e?.message || t('plugin.loadFailed') })
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => { load() }, [load])

  const flash = (type: 'ok' | 'err', text: string) => {
    setMsg({ type, text })
    setTimeout(() => setMsg(null), 4000)
  }

  // 后端返回 403 needAgreement（竞态：同意后协议又被改）→ 重新拉协议并弹窗
  const handleNeedAgreement = () => {
    setNeedAgree(true)
    api.admin.pluginAgreement().then((r) => {
      if (r?.code === 0) setAgreement(r.data?.agreement || null)
    })
  }

  const errMsg = (e: any, fallback: string) => e?.message || fallback

  const doEnable = async (p: PluginView) => {
    setBusy(p.name)
    try {
      const res = await api.admin.enablePlugin(p.name)
      if (res?.code === 0) flash('ok', t('plugin.enabled', { name: p.name }))
      else flash('err', res?.message || t('plugin.enableFailed'))
      load()
    } catch (e: any) {
      if (e?.data?.needAgreement) return handleNeedAgreement()
      flash('err', errMsg(e, t('plugin.enableFailed')))
    } finally { setBusy('') }
  }

  const doDisable = async (p: PluginView) => {
    setBusy(p.name)
    try {
      const res = await api.admin.disablePlugin(p.name)
      if (res?.code === 0) flash('ok', t('plugin.disabled', { name: p.name }))
      else flash('err', res?.message || t('plugin.disableFailed'))
      load()
    } catch (e: any) {
      flash('err', errMsg(e, t('plugin.disableFailed')))
    } finally { setBusy('') }
  }

  const doRestart = async (p: PluginView) => {
    setBusy(p.name)
    try {
      const res = await api.admin.restartPlugin(p.name)
      if (res?.code === 0) flash('ok', t('plugin.restarted', { name: p.name }))
      else flash('err', res?.message || t('plugin.restartFailed'))
      load()
    } catch (e: any) {
      if (e?.data?.needAgreement) return handleNeedAgreement()
      flash('err', errMsg(e, t('plugin.restartFailed')))
    } finally { setBusy('') }
  }

  const doInstall = async (sp: StorePlugin) => {
    if (!sp.url) {
      flash('err', t('plugin.noDownloadUrl'))
      return
    }
    setBusy(sp.name)
    try {
      const res = await api.admin.installPlugin({ source: 'url', url: sp.url })
      if (res?.code === 0) {
        flash('ok', res.data?.message || t('plugin.installed', { name: sp.name }))
        load()
      } else {
        flash('err', res?.message || t('plugin.installFailed'))
      }
    } catch (e: any) {
      if (e?.data?.needAgreement) return handleNeedAgreement()
      flash('err', errMsg(e, t('plugin.installFailed')))
    } finally { setBusy('') }
  }

  const doUninstall = async () => {
    if (!uninstalling) return
    const p = uninstalling
    setBusy(p.name)
    setConfirmErr('')
    try {
      const res = await api.admin.uninstallPlugin(p.name, confirmPwd)
      if (res?.code === 0) {
        flash('ok', t('plugin.uninstalled', { name: p.name }))
        setUninstalling(null)
        setConfirmPwd('')
        load()
      } else {
        setConfirmErr(res?.message || t('plugin.uninstallFailed'))
      }
    } catch (e: any) {
      if (e?.data?.needAgreement) {
        setUninstalling(null)
        return handleNeedAgreement()
      }
      setConfirmErr(errMsg(e, t('plugin.uninstallFailed')))
    } finally { setBusy('') }
  }

  const openLogs = async (name: string) => {
    // 再次点击同一插件 → 收起面板
    if (logFor === name) {
      setLogFor('')
      return
    }
    setLogFor(name)
    setLogs([])
    try {
      const res = await api.admin.pluginLogs(name, 200)
      if (res?.code === 0) setLogs(res.data || [])
    } catch { /* 日志读取失败不打断主流程 */ }
  }

  const wiredCount = useMemo(() => hooks.filter((h) => h.wired).length, [hooks])
  const handlerCount = useMemo(() => hooks.reduce((n, h) => n + (h.count || 0), 0), [hooks])

  return (
    <div className="space-y-5">
      <div className="flex items-start justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-2xl font-semibold">{t('ns_admin.plugins')}</h1>
          <p className="text-sm text-slate-400">{t('plugin.subtitle')}</p>
        </div>
        <div className="flex items-center gap-2">
          {meta.hostReady === false && (
            <span className="rounded-full border border-amber-400/25 bg-amber-500/10 px-2.5 py-1 text-xs text-amber-300">
              {t('plugin.hostNotReady')}
            </span>
          )}
          <button
            type="button"
            onClick={() => { setLoading(true); load() }}
            className="btn-ghost h-9 px-3"
            title={t('common.refresh')}
          >
            <RefreshCw className="h-4 w-4" />
          </button>
        </div>
      </div>

      {msg && (
        <div
          className={`flex items-start gap-2 rounded-xl border px-4 py-2.5 text-sm ${
            msg.type === 'ok'
              ? 'border-emerald-400/30 bg-emerald-500/10 text-emerald-300'
              : 'border-rose-400/30 bg-rose-500/10 text-rose-300'
          }`}
        >
          {msg.type === 'ok'
            ? <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" />
            : <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />}
          <span className="break-all">{msg.text}</span>
        </div>
      )}

      <div className="glass inline-flex w-fit gap-1.5 rounded-2xl p-1.5">
        {([
          ['installed', Package, t('plugin.tabInstalled')],
          ['store', Store, t('plugin.tabStore')],
          ['hooks', Plug, t('plugin.tabHooks')],
        ] as const).map(([k, Icon, label]) => (
          <button
            key={k}
            type="button"
            onClick={() => setTab(k as Tab)}
            className={`flex items-center gap-2 rounded-xl px-4 py-2 text-sm transition ${
              tab === k
                ? 'bg-gradient-to-r from-nebula-500/30 to-cyan-glow/15 text-white shadow-inner-glow'
                : 'text-slate-300 hover:bg-white/10'
            }`}
          >
            <Icon className="h-4 w-4" />
            {label}
          </button>
        ))}
      </div>

      {loading ? (
        <div className="grid h-48 place-items-center">
          <Loader2 className="h-8 w-8 animate-spin text-nebula-300" />
        </div>
      ) : tab === 'installed' ? (
        <InstalledList
          rows={rows}
          busy={busy}
          logFor={logFor}
          logs={logs}
          onEnable={doEnable}
          onDisable={doDisable}
          onRestart={doRestart}
          onUninstall={(p) => { setUninstalling(p); setConfirmPwd(''); setConfirmErr('') }}
          onOpenLogs={openLogs}
        />
      ) : tab === 'store' ? (
        <StoreList store={store} busy={busy} onInstall={doInstall} />
      ) : (
        <HooksList hooks={hooks} wiredCount={wiredCount} handlerCount={handlerCount} />
      )}

      {uninstalling && (
        <div className="fixed inset-0 z-[100] flex items-center justify-center p-4">
          <div
            className="absolute inset-0 bg-black/70 backdrop-blur-sm"
            onClick={() => setUninstalling(null)}
          />
          <div className="glass-strong relative z-10 w-full max-w-md rounded-2xl border border-white/15 p-6">
            <div className="mb-3 flex items-center gap-2">
              <Trash2 className="h-5 w-5 text-rose-300" />
              <h3 className="text-base font-semibold text-white">
                {t('plugin.uninstallTitle', { name: uninstalling.name })}
              </h3>
            </div>
            <p className="mb-4 text-sm text-slate-300">{t('plugin.uninstallWarning')}</p>
            {confirmErr && (
              <div className="mb-3 rounded-lg border border-rose-400/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
                {confirmErr}
              </div>
            )}
            <label className="mb-1.5 block text-xs text-slate-400">
              {t('plugin.confirmPassword')}
            </label>
            <input
              type="password"
              value={confirmPwd}
              onChange={(e) => setConfirmPwd(e.target.value)}
              className="glass-input mb-4 w-full"
              autoFocus
            />
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setUninstalling(null)}
                className="btn-ghost"
              >
                {t('common.cancel')}
              </button>
              <button
                type="button"
                onClick={doUninstall}
                disabled={!confirmPwd || busy === uninstalling.name}
                className="btn-primary"
              >
                {busy === uninstalling.name
                  ? <Loader2 className="h-4 w-4 animate-spin" />
                  : <Trash2 className="h-4 w-4" />}
                {t('plugin.uninstall')}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 协议弹窗：首次进入或协议升级时出现 */}
      <PluginAgreementModal
        open={needAgree}
        agreement={agreement}
        onAccepted={(_rec: AgreementRecord) => {
          setNeedAgree(false)
          flash('ok', t('pluginAgreement.accepted'))
          load()
        }}
        onClose={() => {
          // 不同意就不提供绕过路径：弹窗保持打开，
          // 用户必须滚动读完并勾选才能继续。
        }}
      />
    </div>
  )
}

// ---- 已安装 ----

function InstalledList({
  rows, busy, logs, logFor, onEnable, onDisable, onRestart, onUninstall, onOpenLogs,
}: {
  rows: PluginView[]
  busy: string
  logs: PluginLogEntry[]
  logFor: string
  onEnable: (p: PluginView) => void
  onDisable: (p: PluginView) => void
  onRestart: (p: PluginView) => void
  onUninstall: (p: PluginView) => void
  onOpenLogs: (name: string) => void
}) {
  const { t } = useTranslation()

  if (rows.length === 0) {
    return (
      <div className="glass rounded-2xl p-10 text-center text-slate-400">
        <Puzzle className="mx-auto mb-3 h-10 w-10 opacity-40" />
        {t('plugin.emptyInstalled')}
      </div>
    )
  }

  return (
    <div className="space-y-4">
      {rows.map((p) => {
        const st = STATUS_STYLE[p.status] || STATUS_STYLE.stopped
        const Icon = st.icon
        const isOff = p.status === 'stopped' || p.status === 'disabled'
        const spinning = p.status === 'starting' || p.status === 'stopping'
        const working = busy === p.name

        return (
          <div key={p.id || p.name} className="glass rounded-2xl p-5">
            <div className="flex flex-wrap items-start gap-4">
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="truncate text-sm font-semibold text-white">
                    {p.title || p.name}
                  </span>
                  <code className="rounded bg-white/5 px-1.5 py-0.5 font-mono text-[11px] text-slate-400">
                    {p.name}
                  </code>
                  {p.version && <span className="text-xs text-slate-400">v{p.version}</span>}
                  {p.author && <span className="text-xs text-slate-500">{p.author}</span>}
                </div>
                {p.description && (
                  <p className="mt-2 text-xs text-slate-400">{p.description}</p>
                )}

                {p.hooksList.length > 0 && (
                  <div className="mt-2 flex flex-wrap gap-1">
                    {p.hooksList.map((h) => (
                      <span
                        key={h}
                        className="rounded-full border border-white/10 bg-white/5 px-2 py-0.5 font-mono text-[10px] text-cyan-glow/80"
                      >
                        {h}
                      </span>
                    ))}
                  </div>
                )}

                {p.permissions && p.permissions.length > 0 && (
                  <div className="mt-1.5 flex flex-wrap items-center gap-1 text-[10px] text-slate-500">
                    <span>{t('plugin.permissions')}:</span>
                    {p.permissions.map((pm) => (
                      <span key={pm} className="rounded bg-white/5 px-1.5 py-0.5">{pm}</span>
                    ))}
                  </div>
                )}
              </div>

              <div className="flex shrink-0 items-center gap-2">
                <span
                  className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium ${st.cls}`}
                >
                  <Icon className={`h-3.5 w-3.5 ${spinning ? 'animate-spin' : ''}`} />
                  {p.status}
                  {p.pid > 0 && <span className="opacity-70">#{p.pid}</span>}
                </span>

                {isOff ? (
                  <button
                    type="button"
                    onClick={() => onEnable(p)}
                    disabled={working}
                    className="grid h-8 w-8 place-items-center rounded-lg hover:bg-white/10 disabled:opacity-40"
                    title={t('plugin.enable')}
                  >
                    {working
                      ? <Loader2 className="h-4 w-4 animate-spin" />
                      : <Power className="h-4 w-4" />}
                  </button>
                ) : (
                  <button
                    type="button"
                    onClick={() => onDisable(p)}
                    disabled={working}
                    className="grid h-8 w-8 place-items-center rounded-lg hover:bg-white/10 disabled:opacity-40"
                    title={t('plugin.disable')}
                  >
                    {working
                      ? <Loader2 className="h-4 w-4 animate-spin" />
                      : <Square className="h-4 w-4" />}
                  </button>
                )}

                <button
                  type="button"
                  onClick={() => onRestart(p)}
                  disabled={working || !p.enabled}
                  className="grid h-8 w-8 place-items-center rounded-lg hover:bg-white/10 disabled:opacity-30"
                  title={t('plugin.restart')}
                >
                  <RefreshCw className="h-4 w-4" />
                </button>
                <button
                  type="button"
                  onClick={() => onOpenLogs(p.name)}
                  className={`grid h-8 w-8 place-items-center rounded-lg hover:bg-white/10 ${
                    logFor === p.name ? 'bg-white/10 text-cyan-glow' : ''
                  }`}
                  title={t('plugin.logs')}
                >
                  <ScrollText className="h-4 w-4" />
                </button>
                <button
                  type="button"
                  onClick={() => onUninstall(p)}
                  disabled={working}
                  className="grid h-8 w-8 place-items-center rounded-lg text-rose-300 hover:bg-rose-500/15 disabled:opacity-40"
                  title={t('plugin.uninstall')}
                >
                  <Trash2 className="h-4 w-4" />
                </button>
              </div>
            </div>

            {/* 「数据库说启用但进程没跑」是最容易被忽略的坑，必须单独提示 */}
            {(p.runtimeError || p.manifestError) && (
              <div className="mt-3 flex items-start gap-2 rounded-lg border border-amber-400/25 bg-amber-500/10 px-3 py-2 text-xs text-amber-200">
                <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                <span className="break-all">{p.manifestError || p.runtimeError}</span>
              </div>
            )}

            {p.restartsNow > 0 && (
              <div className="mt-2 text-[11px] text-slate-500">
                {t('plugin.restarts', { n: p.restartsNow })}
              </div>
            )}

            {logFor === p.name && (
              <div className="mt-3 max-h-64 overflow-y-auto rounded-xl border border-white/10 bg-black/30 p-3">
                {logs.length === 0 ? (
                  <p className="text-xs text-slate-500">{t('plugin.noLogs')}</p>
                ) : (
                  <div className="space-y-1">
                    {logs.map((l, i) => (
                      <div key={i} className="flex gap-2 font-mono text-[11px] leading-relaxed">
                        <span className="shrink-0 text-slate-600">{l.time}</span>
                        <span
                          className={
                            l.level === 'error' ? 'text-rose-400'
                              : l.level === 'warn' ? 'text-amber-400'
                              : 'text-slate-400'
                          }
                        >
                          {l.message}
                        </span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

// ---- 商店 ----

function StoreList({
  store, busy, onInstall,
}: { store: StorePlugin[]; busy: string; onInstall: (p: StorePlugin) => void }) {
  const { t } = useTranslation()
  const [url, setUrl] = useState('')
  const [saving, setSaving] = useState(false)

  const saveURL = async () => {
    setSaving(true)
    try {
      const cur = await api.admin.settings()
      const data = (cur && cur.code === 0 && cur.data) || {}
      await api.admin.saveSettings({ ...data, 'plugin.store.url': url })
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <div className="glass rounded-2xl p-5">
        <div className="mb-3 flex items-center gap-2 text-sm font-semibold">
          <Store className="h-4 w-4 text-nebula-300" />
          {t('plugin.storeUrl')}
        </div>
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <input
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder="https://plugins.example.com/nebula/"
            className="glass-input flex-1"
          />
          <button
            type="button"
            onClick={saveURL}
            disabled={saving}
            className="btn-primary h-9 px-4"
          >
            {saving && <Loader2 className="h-4 w-4 animate-spin" />}
            {t('common.save')}
          </button>
        </div>
        <p className="mt-2 text-[11px] text-slate-500">{t('plugin.storeUrlHint')}</p>
      </div>

      {store.length === 0 ? (
        <div className="glass rounded-2xl p-10 text-center text-slate-400">
          <Store className="mx-auto mb-3 h-10 w-10 opacity-40" />
          {t('plugin.emptyStore')}
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {store.map((p) => (
            <div key={p.name} className="glass flex flex-col rounded-2xl p-5">
              <div className="flex items-start gap-3">
                <span className="grid h-11 w-11 shrink-0 place-items-center rounded-xl bg-gradient-to-br from-nebula-500 to-cyan-glow">
                  <Puzzle className="h-5 w-5 text-white" />
                </span>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-semibold">{p.title || p.name}</div>
                  <div className="truncate text-xs text-slate-400">
                    {p.version ? `v${p.version}` : ''} {p.author ? `· ${p.author}` : ''}
                  </div>
                </div>
              </div>
              <p className="mt-3 min-h-[40px] text-xs text-slate-300 line-clamp-2">
                {p.description || '—'}
              </p>
              {p.tags && p.tags.length > 0 && (
                <div className="mt-2 flex flex-wrap gap-1">
                  {p.tags.map((tg) => (
                    <span
                      key={tg}
                      className="rounded-full border border-white/10 bg-white/5 px-2 py-0.5 text-[10px] text-slate-300"
                    >
                      {tg}
                    </span>
                  ))}
                </div>
              )}
              <div className="mt-auto flex items-center justify-between border-t border-white/10 pt-3">
                {p.installed ? (
                  <span className="inline-flex items-center gap-1 rounded-full bg-emerald-500/15 px-2 py-0.5 text-xs text-emerald-300">
                    <CheckCircle2 className="h-3 w-3" />{t('plugin.installedLabel')}
                  </span>
                ) : (
                  <span />
                )}
                <button
                  type="button"
                  onClick={() => onInstall(p)}
                  disabled={busy === p.name}
                  className="btn-primary h-8 px-3 text-xs"
                >
                  {busy === p.name
                    ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    : <Download className="h-3.5 w-3.5" />}
                  {t('plugin.install')}
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </>
  )
}

// ---- 钩子 ----

function HooksList({
  hooks, wiredCount, handlerCount,
}: { hooks: HookInfo[]; wiredCount: number; handlerCount: number }) {
  const { t } = useTranslation()

  return (
    <div className="glass overflow-hidden rounded-2xl">
      <div className="flex flex-wrap items-center gap-2 border-b border-white/10 px-5 py-3.5">
        <Plug className="h-4 w-4 text-cyan-glow" />
        <span className="text-sm font-semibold">{t('plugin.hookList')}</span>
        <span className="ml-auto text-xs text-slate-400">
          {t('plugin.hookSummary', {
            wired: wiredCount,
            total: hooks.length,
            handlers: handlerCount,
          })}
        </span>
      </div>

      <div className="divide-y divide-white/10">
        {hooks.map((h) => (
          <div
            key={h.name}
            className="grid grid-cols-[auto_1fr_auto] items-start gap-4 px-5 py-3.5"
          >
            <div className="mt-0.5 flex flex-col items-center gap-1.5">
              <span
                className={`inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-xs font-semibold ${
                  h.count > 0
                    ? 'border border-emerald-400/20 bg-emerald-500/15 text-emerald-300'
                    : 'border border-white/10 bg-slate-500/15 text-slate-400'
                }`}
              >
                <Puzzle className="h-3 w-3" />{h.count}
              </span>
              {h.count > 0 && (
                <span className="text-[10px] text-slate-500">
                  {h.outProcess > 0
                    ? `${h.outProcess} ${t('plugin.outOfProcess')}`
                    : t('plugin.inProcess')}
                </span>
              )}
            </div>

            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <code className="rounded-lg border border-white/10 bg-white/5 px-2 py-0.5 font-mono text-xs font-semibold text-nebula-200">
                  {h.name}
                </code>
                <span
                  className={`rounded px-1.5 py-0.5 text-[10px] ${
                    h.mode === 'sync'
                      ? 'bg-cyan-500/15 text-cyan-300'
                      : 'bg-slate-500/15 text-slate-400'
                  }`}
                >
                  {h.mode}
                </span>
                {h.blockable && (
                  <span className="rounded bg-rose-500/15 px-1.5 py-0.5 text-[10px] text-rose-300">
                    {t('plugin.blockable')}
                  </span>
                )}
                {!h.wired && (
                  <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] text-amber-300">
                    {t('plugin.notWired')}
                  </span>
                )}
              </div>
              <p className="mt-1.5 text-xs leading-relaxed text-slate-400">{h.doc}</p>
            </div>

            <div className="text-right">
              <span className="inline-grid h-8 w-8 place-items-center text-slate-700">
                <ExternalLink className="h-4 w-4 opacity-30" />
              </span>
            </div>
          </div>
        ))}
      </div>

      {handlerCount === 0 && (
        <div className="border-t border-white/10 px-5 py-4 text-center text-xs text-slate-500">
          {t('plugin.noHandlers')}
        </div>
      )}
    </div>
  )
}
