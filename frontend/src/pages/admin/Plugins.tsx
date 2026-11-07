import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  AlertTriangle, CheckCircle2, Copy, Download, ExternalLink, FileCode2, KeyRound, LayoutGrid,
  Loader2, Package, Plug, Plus, Power, Puzzle, RefreshCw, ScrollText, ShieldAlert, ShieldCheck,
  ShieldQuestion, ShieldX, Square, Store, Trash2, XCircle,
} from 'lucide-react'
import {
  api,
  type AgreementRecord, type HookInfo, type PluginContractDiag,
  type PluginGlobalState, type PluginLogEntry, type PluginSignatureInfo,
  type PluginTrustKey, type PluginTrustState, type PluginUIData, type PluginView,
  type StorePlugin,
} from '../../api/client'
import { notifyPluginUIChanged } from '../../store/pluginUI'
import PluginAgreementModal from './PluginAgreementModal'
import {
  ContractModal, GlobalSwitchCard, PluginRiskModal, UIExtensionsPanel,
} from './PluginV2Panels'

type Tab = 'installed' | 'store' | 'hooks' | 'ui' | 'trust'

// 签名策略三档。i18n key 分开写，避免拼接 key（漏一个语言就变成原始串）。
const TRUST_MODES = ['off', 'warn', 'require'] as const
const TRUST_MODE_LABEL: Record<string, string> = {
  off: 'plugin.trustModeOff',
  warn: 'plugin.trustModeWarn',
  require: 'plugin.trustModeRequire',
}
const TRUST_MODE_HINT: Record<string, string> = {
  off: 'plugin.trustModeOffHint',
  warn: 'plugin.trustModeWarnHint',
  require: 'plugin.trustModeRequireHint',
}

// keyId 是公钥 sha256 的前 8 字节的 hex（16 个字符）——见后端 pbclus.KeyID。
// 用它把「粘贴的是 keyId」和「粘贴的是 base64 公钥」区分开：
// 二者只能选一个发给后端，发错会得到"公钥不是合法 base64"这种误导性报错。
const looksLikeKeyID = (s: string) => /^[0-9a-fA-F]{16}$/.test(s.trim())

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

  // v2 插件平台：全局总开关、风险确认、三文件契约诊断、UI 扩展点
  const [global, setGlobal] = useState<PluginGlobalState | null>(null)
  const [globalBusy, setGlobalBusy] = useState(false)
  const [riskOpen, setRiskOpen] = useState(false)
  const [diagFor, setDiagFor] = useState('')
  const [diag, setDiag] = useState<PluginContractDiag | null>(null)
  const [diagBusy, setDiagBusy] = useState('')
  const [uiData, setUiData] = useState<PluginUIData | null>(null)

  const load = useCallback(async () => {
    try {
      const [p, h, s, ag, g, ui] = await Promise.allSettled([
        api.admin.plugins(),
        api.admin.pluginsHooks(),
        api.admin.pluginsStore(),
        api.admin.pluginAgreement(),
        api.admin.pluginsGlobal(),
        api.pluginsUI(),
      ])

      if (p.status === 'fulfilled' && p.value?.code === 0) {
        setRows(p.value.data || [])
        setMeta({ hostReady: p.value.meta?.hostReady, pluginsDir: p.value.meta?.pluginsDir })
      }
      if (h.status === 'fulfilled' && h.value?.code === 0) setHooks(h.value.data || [])
      if (s.status === 'fulfilled' && s.value?.code === 0) setStore(s.value.data || [])
      if (g.status === 'fulfilled' && g.value?.code === 0) setGlobal(g.value.data || null)
      if (ui.status === 'fulfilled' && ui.value?.code === 0) setUiData(ui.value.data || null)

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
      // 启停插件 / 切换总开关后，让主布局的侧边栏与主题立即同步
      notifyPluginUIChanged()
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

  // 开启必须先过风险确认弹窗；关闭不需要二次确认（降风险方向）。
  const turnGlobal = (enabled: boolean) => {
    if (enabled) {
      setRiskOpen(true)
      return
    }
    void setGlobalEnabled(false, false)
  }

  const setGlobalEnabled = async (enabled: boolean, acknowledge: boolean) => {
    setGlobalBusy(true)
    try {
      const r = await api.admin.setPluginsGlobal(enabled, acknowledge)
      if (r?.code === 0) {
        flash('ok', enabled ? t('pluginV2.turnedOn') : t('pluginV2.turnedOff'))
        setRiskOpen(false)
        load()
      } else {
        flash('err', r?.message || t('pluginV2.globalFailed'))
      }
    } catch (e: any) {
      flash('err', errMsg(e, t('pluginV2.globalFailed')))
    } finally {
      setGlobalBusy(false)
    }
  }

  const openDiag = async (name: string) => {
    setDiagFor(name)
    setDiag(null)
    setDiagBusy(name)
    try {
      const r = await api.admin.pluginContract(name)
      if (r?.code === 0) setDiag(r.data || null)
      else flash('err', r?.message || t('pluginV2.diagFailed'))
    } catch (e: any) {
      flash('err', errMsg(e, t('pluginV2.diagFailed')))
      setDiagFor('')
    } finally {
      setDiagBusy('')
    }
  }

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

  // 保存插件配置。
  //
  // 后端会把配置**加密**后落库（models.Encrypted），所以这里不必也不该
  // 在前端做任何加密处理 —— 前端只负责校验 JSON 合法性，
  // 剩下的交给服务端（前端加密没有意义：密钥不能发给浏览器）。
  const doSaveConfig = async (name: string, config: string) => {
    setBusy(name)
    try {
      const res = await api.admin.updatePluginConfig(name, config)
      if (res?.code === 0) {
        flash('ok', t('plugin.configSaved'))
        load()
      } else {
        flash('err', res?.message || t('plugin.configSaveFailed'))
      }
    } catch (e: any) {
      if (e?.data?.needAgreement) return handleNeedAgreement()
      flash('err', errMsg(e, t('plugin.configSaveFailed')))
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

      <GlobalSwitchCard
        state={global}
        busy={globalBusy}
        onTurnOn={() => turnGlobal(true)}
        onTurnOff={() => turnGlobal(false)}
      />

      <div className="glass inline-flex w-fit gap-1.5 rounded-2xl p-1.5">
        {([
          ['installed', Package, t('plugin.tabInstalled')],
          ['store', Store, t('plugin.tabStore')],
          ['hooks', Plug, t('plugin.tabHooks')],
          ['ui', LayoutGrid, t('pluginV2.uiTitle')],
          ['trust', ShieldCheck, t('plugin.trustTab')],
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
          diagBusy={diagBusy}
          onEnable={doEnable}
          onDisable={doDisable}
          onRestart={doRestart}
          onUninstall={(p) => { setUninstalling(p); setConfirmPwd(''); setConfirmErr('') }}
          onOpenLogs={openLogs}
          onDiagnose={openDiag}
          onSaveConfig={doSaveConfig}
        />
      ) : tab === 'store' ? (
        <StoreList store={store} busy={busy} onInstall={doInstall} />
      ) : tab === 'hooks' ? (
        <HooksList hooks={hooks} wiredCount={wiredCount} handlerCount={handlerCount} />
      ) : tab === 'ui' ? (
        <UIExtensionsPanel data={uiData} />
      ) : (
        <TrustPanel flash={flash} onChanged={load} />
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

      {/* v2：开启插件总开关前的强制风险确认（后端也会校验 acknowledge=true） */}
      <PluginRiskModal
        open={riskOpen}
        busy={globalBusy}
        onConfirm={() => void setGlobalEnabled(true, true)}
        onClose={() => setRiskOpen(false)}
      />
      {diagFor && (
        <ContractModal
          name={diagFor}
          data={diag}
          onClose={() => { setDiagFor(''); setDiag(null) }}
        />
      )}
    </div>
  )
}

// ---- 已安装 ----

function InstalledList({
  rows, busy, logs, logFor, diagBusy,
  onEnable, onDisable, onRestart, onUninstall, onOpenLogs, onDiagnose, onSaveConfig,
}: {
  rows: PluginView[]
  busy: string
  logs: PluginLogEntry[]
  logFor: string
  diagBusy: string
  onEnable: (p: PluginView) => void
  onDisable: (p: PluginView) => void
  onRestart: (p: PluginView) => void
  onUninstall: (p: PluginView) => void
  onOpenLogs: (name: string) => void
  onDiagnose: (name: string) => void
  onSaveConfig: (name: string, config: string) => void
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
                  <SignatureBadge info={p.signatureInfo} />
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

                {/* 配置编辑。配置在数据库里是**加密存储**的
                    （models.Encrypted），接口下发给管理端的仍是明文 JSON —— 
                    否则管理员没法核对/修改自己填的配置。 */}
                <ConfigEditor plugin={p} saving={working} onSave={onSaveConfig} />
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
                  onClick={() => onDiagnose(p.name)}
                  className="grid h-8 w-8 place-items-center rounded-lg hover:bg-white/10"
                  title={t('pluginV2.diag')}
                >
                  {diagBusy === p.name
                    ? <Loader2 className="h-4 w-4 animate-spin" />
                    : <FileCode2 className="h-4 w-4" />}
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

// ---- 配置编辑 ----

/**
 * ConfigEditor 插件配置编辑框。
 *
 * 几个刻意的设计：
 *
 *  - **草稿与已存配置分开**：列表刷新（load()）会带回新的 plugin.config，
 *    但只在**没有正在编辑**时才覆盖草稿 —— 否则管理员输入到一半，
 *    一次后台刷新就会把内容冲掉。
 *  - **保存前校验 JSON**：后端也会拒非法 JSON，但在这里先拦一道，
 *    错误提示离输入框更近，不用等一次网络往返。
 *  - **不显示"加密"之外的处理**：配置在库里是密文（后端 models.Encrypted），
 *    前端不做任何加密 —— 密钥不能发给浏览器，前端加密没有意义。
 *  - 空配置合法（很多插件靠默认值工作），此时不做 JSON 校验。
 */
function ConfigEditor({
  plugin, saving, onSave,
}: {
  plugin: PluginView
  saving: boolean
  onSave: (name: string, config: string) => void
}) {
  const { t } = useTranslation()
  const stored = plugin.config || ''
  const [draft, setDraft] = useState(stored)
  const [editing, setEditing] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    if (!editing) {
      setDraft(stored)
      setErr('')
    }
  }, [stored, editing])

  const startEdit = () => {
    // 能解析就顺手美化一下：配置多是单行 JSON，直接编辑很难看清层级
    let pretty = stored
    if (stored.trim() !== '') {
      try { pretty = JSON.stringify(JSON.parse(stored), null, 2) } catch { /* 保持原样 */ }
    }
    setDraft(pretty)
    setEditing(true)
    setErr('')
  }

  const save = () => {
    const v = draft.trim()
    if (v !== '') {
      try {
        JSON.parse(v)
      } catch {
        setErr(t('plugin.configInvalidJson'))
        return
      }
    }
    setErr('')
    setEditing(false)
    onSave(plugin.name, v)
  }

  return (
    <div className="mt-3 rounded-xl border border-white/10 bg-white/5 p-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[11px] font-semibold text-slate-300">
          {t('plugin.configTitle')}
        </span>
        <span className="rounded bg-emerald-500/15 px-1.5 py-0.5 text-[10px] text-emerald-300">
          {t('plugin.configEncrypted')}
        </span>
        {!editing && (
          <button
            type="button"
            onClick={startEdit}
            disabled={saving}
            className="ml-auto rounded-lg border border-white/10 px-2 py-0.5 text-[11px] text-slate-300 hover:bg-white/10 disabled:opacity-40"
          >
            {t('plugin.configEdit')}
          </button>
        )}
      </div>

      {editing ? (
        <>
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            spellCheck={false}
            rows={Math.min(12, Math.max(4, draft.split('\n').length))}
            className="mt-2 w-full resize-y rounded-lg border border-white/10 bg-black/30 p-2 font-mono text-[11px] text-slate-200 outline-none focus:border-cyan-glow/40"
            placeholder="{}"
          />
          {err && <p className="mt-1 text-[10px] text-rose-300">{err}</p>}
          <div className="mt-2 flex items-center gap-2">
            <button
              type="button"
              onClick={save}
              disabled={saving}
              className="inline-flex items-center gap-1 rounded-lg bg-cyan-500/20 px-2.5 py-1 text-[11px] text-cyan-200 hover:bg-cyan-500/30 disabled:opacity-40"
            >
              {saving && <Loader2 className="h-3 w-3 animate-spin" />}
              {t('plugin.configSave')}
            </button>
            <button
              type="button"
              onClick={() => { setEditing(false); setDraft(stored); setErr('') }}
              className="rounded-lg border border-white/10 px-2.5 py-1 text-[11px] text-slate-400 hover:bg-white/10"
            >
              {t('plugin.configCancel')}
            </button>
          </div>
        </>
      ) : stored ? (
        <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-black/20 p-2 font-mono text-[11px] text-slate-300">
          {stored}
        </pre>
      ) : (
        <p className="mt-2 text-[11px] text-slate-500">{t('plugin.configEmpty')}</p>
      )}

      <p className="mt-2 text-[10px] leading-relaxed text-slate-500">
        {t('plugin.configEncryptedHint')}
      </p>
    </div>
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
                {h.planned && (
                  <span
                    className="rounded bg-violet-500/15 px-1.5 py-0.5 text-[10px] text-violet-300"
                    title={h.plan}
                  >
                    {t('plugin.planned')}
                  </span>
                )}
              </div>
              <p className="mt-1.5 text-xs leading-relaxed text-slate-400">{h.doc}</p>
              {h.planned && h.plan && (
                <p className="mt-1.5 text-[11px] leading-relaxed text-violet-300/80">
                  <span className="font-semibold">{t('plugin.planLabel')}</span>
                  {h.plan}
                </p>
              )}
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

// ---- 签名徽标 ----

/**
 * SignatureBadge 已安装插件的签名状态徽标。
 *
 * 三种状态严格区分，尤其**不能把「签名有效」画成「受信任」**：
 * 后端的 present / valid / trusted 是三个独立事实，
 * valid 只说明"签名与包内容自洽"——任何人都能生成一对密钥签出一个
 * valid=true 的包，只有 trusted（keyId 在信任库里）才叫受信任。
 */
function SignatureBadge({ info }: { info?: PluginSignatureInfo }) {
  const { t } = useTranslation()

  // 没有 signatureInfo 或 present=false → 未签名（灰）
  if (!info || !info.present) {
    return (
      <span
        className="inline-flex items-center gap-1 rounded-full border border-white/10 bg-slate-500/15 px-2 py-0.5 text-[10px] text-slate-400"
        title={t('plugin.sigBadgeNoneHint')}
      >
        <ShieldQuestion className="h-3 w-3" />
        {t('plugin.sigBadgeNone')}
      </span>
    )
  }

  // present && !valid → 签名无效（红）。后端给的原因放在 title 里。
  if (!info.valid) {
    return (
      <span
        className="inline-flex items-center gap-1 rounded-full border border-rose-400/25 bg-rose-500/15 px-2 py-0.5 text-[10px] text-rose-300"
        title={info.error || t('plugin.sigBadgeInvalidHint')}
      >
        <ShieldX className="h-3 w-3" />
        {t('plugin.sigBadgeInvalid')}
      </span>
    )
  }

  // present && valid && trusted → 已签名 · 受信任（绿）
  if (info.trusted) {
    const who = info.keyName || info.keyId || '—'
    return (
      <span
        className="inline-flex items-center gap-1 rounded-full border border-emerald-400/25 bg-emerald-500/15 px-2 py-0.5 text-[10px] text-emerald-300"
        title={t('plugin.sigBadgeTrustedHint', { key: who })}
      >
        <ShieldCheck className="h-3 w-3" />
        {t('plugin.sigBadgeTrusted', { key: who })}
      </span>
    )
  }

  // present && valid && !trusted → 已签名 · 未受信任（琥珀）
  // 这是最容易误读的一档：签名没问题，只是签名者不在信任库里。
  const kid = info.keyId || '—'
  return (
    <span
      className="inline-flex items-center gap-1 rounded-full border border-amber-400/25 bg-amber-500/15 px-2 py-0.5 text-[10px] text-amber-300"
      title={t('plugin.sigBadgeUntrustedHint')}
    >
      <ShieldAlert className="h-3 w-3" />
      {t('plugin.sigBadgeUntrusted', { key: kid })}
    </span>
  )
}

// ---- 签名与信任 ----

/**
 * TrustPanel 签名与信任。
 *
 * 界面上有三条**不能抹平**的区分，它们都对应后端刻意的设计：
 *
 *  1. 「签名有效」≠「受信任」——valid 只说明签名与内容自洽，
 *     只有 trusted（keyId 在信任库里）才叫受信任；
 *  2. 「信任库损坏」≠「空信任库」——后者是安全默认值，
 *     前者必须醒目报警，否则管理员会以为自己配的信任关系丢了；
 *  3. 切到 require 被 409 挡回是**正常流程**（存在无签名记录的已安装插件），
 *     要弹确认框并把后端原话展示出来，确认后带 confirm=true 重发。
 *
 * 所有展示都来自接口返回，没有任何写死的示例密钥。
 */
function TrustPanel({
  flash, onChanged,
}: {
  flash: (type: 'ok' | 'err', text: string) => void
  onChanged: () => void
}) {
  const { t } = useTranslation()
  const [data, setData] = useState<PluginTrustState | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadErr, setLoadErr] = useState('')
  const [busy, setBusy] = useState('')
  const [copied, setCopied] = useState(false)

  // 添加公钥
  const [input, setInput] = useState('')
  const [name, setName] = useState('')
  const [addErr, setAddErr] = useState('')
  const [addIsPrivateKey, setAddIsPrivateKey] = useState(false)
  const [addWarning, setAddWarning] = useState('')

  // 二次确认：移除密钥 / 切到 require
  const [removing, setRemoving] = useState<PluginTrustKey | null>(null)
  const [pendingMode, setPendingMode] = useState<
    { mode: string; message: string; unsigned?: number } | null
  >(null)

  const load = useCallback(async () => {
    try {
      const res = await api.admin.pluginTrust()
      if (res?.code === 0) {
        setData(res.data)
        setLoadErr('')
      } else {
        setLoadErr(res?.message || t('plugin.loadFailed'))
      }
    } catch (e: any) {
      setLoadErr(e?.message || t('plugin.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => { load() }, [load])

  const copyPath = async () => {
    const p = data?.path || ''
    if (!p) return
    try {
      await navigator.clipboard.writeText(p)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // 剪贴板 API 在非安全上下文不可用。路径本来就明文显示在页面上，
      // 静默降级即可，不必为一次复制失败打断流程。
    }
  }

  const applyMode = async (mode: string, confirm: boolean) => {
    setBusy('mode')
    try {
      const res = await api.admin.setPluginTrustMode(mode, confirm)
      if (res?.code === 0) {
        setPendingMode(null)
        // 用本地化的策略说明做提示：后端也返回 message，
        // 但它是中文单语，五语言的界面里不该直接显示。
        flash('ok', t(TRUST_MODE_HINT[mode] || 'plugin.trustMode'))
        await load()
        onChanged()
      } else {
        flash('err', res?.message || t('plugin.loadFailed'))
      }
    } catch (e: any) {
      // 409 + needConfirm：后端要求二次确认（有存量无签名插件）。
      // 不是错误，是流程 —— 弹确认框，确认后带 confirm=true 重发。
      if (e?.data?.needConfirm) {
        setPendingMode({
          mode,
          message: e?.message || '',
          unsigned: typeof e?.data?.unsigned === 'number' ? e.data.unsigned : undefined,
        })
        return
      }
      flash('err', e?.message || t('plugin.loadFailed'))
    } finally {
      setBusy('')
    }
  }

  const doAdd = async () => {
    const raw = input.trim()
    if (!raw || !name.trim()) return
    setBusy('add')
    setAddErr('')
    setAddIsPrivateKey(false)
    setAddWarning('')
    try {
      // 一个输入框同时接受 publicKey(base64) 与 keyId：二者只能发一个，
      // 后端按 publicKey 优先。keyId 是 16 位 hex，据此区分。
      const res = await api.admin.addPluginTrustKey(
        looksLikeKeyID(raw)
          ? { keyId: raw, name: name.trim() }
          : { publicKey: raw, name: name.trim() },
      )
      if (res?.code === 0) {
        setInput('')
        setName('')
        setAddWarning(res.data?.warning || '')
        flash('ok', t('plugin.trustKeyCount', { n: res.data?.count ?? 0 }))
        await load()
        onChanged()
      } else {
        setAddErr(res?.message || t('plugin.loadFailed'))
      }
    } catch (e: any) {
      // 后端用 400 + 「私钥」把"贴进来的是私钥"这件事明确挡住。
      // 这条 message 必须原样展示——它拦下的是一次私钥泄漏，
      // 简化成"输入非法"会让用户继续把私钥发给别人。
      const m = e?.message || t('plugin.loadFailed')
      setAddErr(m)
      setAddIsPrivateKey(String(m).includes('私钥'))
    } finally {
      setBusy('')
    }
  }

  const doRemove = async () => {
    if (!removing) return
    const k = removing
    setBusy('remove:' + k.keyId)
    try {
      const res = await api.admin.removePluginTrustKey(k.keyId)
      if (res?.code === 0) {
        flash('ok', t('plugin.trustKeyRemoved', { name: k.name || k.keyId }))
        setRemoving(null)
        await load()
        onChanged()
      } else {
        flash('err', res?.message || t('plugin.loadFailed'))
        setRemoving(null)
      }
    } catch (e: any) {
      flash('err', e?.message || t('plugin.loadFailed'))
      setRemoving(null)
    } finally {
      setBusy('')
    }
  }

  if (loading) {
    return (
      <div className="grid h-48 place-items-center">
        <Loader2 className="h-8 w-8 animate-spin text-nebula-300" />
      </div>
    )
  }

  if (!data) {
    return (
      <div className="glass rounded-2xl p-6 text-sm text-rose-300">
        <div className="flex items-start gap-2">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          <span className="break-all">{loadErr || t('plugin.loadFailed')}</span>
        </div>
      </div>
    )
  }

  const keys = data.keys || []
  // 信任库损坏时后端只返回 error，三个计数字段会缺失 —— 不能显示成 0。
  const stat = (v?: number) => (typeof v === 'number' ? String(v) : '—')

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold text-white">{t('plugin.trustTitle')}</h2>
        <p className="text-xs text-slate-400">{t('plugin.trustSubtitle')}</p>
      </div>

      {/* 信任库文件损坏：必须立刻看到，绝不能显示成"空库" */}
      {data.error && (
        <div className="rounded-2xl border border-rose-400/40 bg-rose-500/15 p-5">
          <div className="flex items-center gap-2 text-sm font-semibold text-rose-200">
            <AlertTriangle className="h-4 w-4" />
            {t('plugin.trustStoreBroken')}
          </div>
          <p className="mt-2 break-all font-mono text-xs leading-relaxed text-rose-200/90">
            {data.error}
          </p>
        </div>
      )}

      {/* 策略：三选一，每档一句话说明影响 */}
      <div className="glass rounded-2xl p-5">
        <div className="flex items-center gap-2 text-sm font-semibold">
          <ShieldCheck className="h-4 w-4 text-nebula-300" />
          {t('plugin.trustMode')}
        </div>
        <div className="mt-3 grid gap-2 sm:grid-cols-3">
          {TRUST_MODES.map((m) => {
            const active = data.mode === m
            return (
              <button
                key={m}
                type="button"
                disabled={busy === 'mode'}
                onClick={() => { if (!active) applyMode(m, false) }}
                className={`rounded-xl border px-3 py-2.5 text-left transition disabled:opacity-50 ${
                  active
                    ? 'border-cyan-glow/40 bg-gradient-to-r from-nebula-500/25 to-cyan-glow/10 text-white'
                    : 'border-white/10 bg-white/5 text-slate-300 hover:bg-white/10'
                }`}
              >
                <span className="flex items-center gap-1.5 text-sm font-medium">
                  {busy === 'mode' && active
                    ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    : active
                      ? <CheckCircle2 className="h-3.5 w-3.5 text-cyan-glow" />
                      : <ShieldQuestion className="h-3.5 w-3.5 opacity-40" />}
                  {t(TRUST_MODE_LABEL[m])}
                </span>
                <span className="mt-1 block text-[11px] leading-relaxed text-slate-400">
                  {t(TRUST_MODE_HINT[m])}
                </span>
              </button>
            )
          })}
        </div>
      </div>

      {/* 统计：已安装 / 有签名 / 受信任 */}
      <div className="glass grid grid-cols-3 divide-x divide-white/10 rounded-2xl">
        {([
          ['plugin.trustStatInstalled', data.installedCount],
          ['plugin.trustStatSigned', data.signedCount],
          ['plugin.trustStatTrusted', data.trustedCount],
        ] as const).map(([k, v]) => (
          <div key={k} className="px-5 py-4 text-center">
            <div className="text-xl font-semibold text-white">{stat(v)}</div>
            <div className="mt-0.5 text-[11px] text-slate-400">{t(k)}</div>
          </div>
        ))}
      </div>

      {/* 信任库路径：管理员需要知道这个事实 */}
      <div className="glass rounded-2xl p-5">
        <div className="mb-2 flex items-center gap-2 text-sm font-semibold">
          <KeyRound className="h-4 w-4 text-nebula-300" />
          {t('plugin.trustStorePath')}
        </div>
        <div className="flex items-center gap-2">
          <code className="min-w-0 flex-1 break-all rounded-lg border border-white/10 bg-black/30 px-3 py-2 font-mono text-[11px] text-slate-300">
            {data.path || '—'}
          </code>
          <button
            type="button"
            onClick={copyPath}
            disabled={!data.path}
            className="btn-ghost h-9 shrink-0 px-3 disabled:opacity-40"
          >
            {copied
              ? <CheckCircle2 className="h-4 w-4 text-emerald-300" />
              : <Copy className="h-4 w-4" />}
            <span className="text-xs">
              {copied ? t('plugin.trustCopied') : t('plugin.trustCopy')}
            </span>
          </button>
        </div>
        <p className="mt-2 text-[11px] text-slate-500">{t('plugin.trustStorePathHint')}</p>
      </div>

      {/* 核心理念：签名有效 ≠ 可信 */}
      <div className="flex items-start gap-2 rounded-2xl border border-amber-400/25 bg-amber-500/10 px-4 py-3 text-xs leading-relaxed text-amber-200">
        <ShieldAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
        <span>{t('plugin.sigNotTrustedNote')}</span>
      </div>

      {/* 密钥列表 */}
      <div className="glass overflow-hidden rounded-2xl">
        <div className="flex flex-wrap items-center gap-2 border-b border-white/10 px-5 py-3.5">
          <KeyRound className="h-4 w-4 text-cyan-glow" />
          <span className="text-sm font-semibold">{t('plugin.trustKeys')}</span>
          {/* 信任库损坏时 keys 一定是空的 —— 但不能因此显示"0 枚密钥"，
              那看起来就像"信任库是空的"，正是要避免的误读 */}
          {!data.error && (
            <span className="ml-auto rounded-full border border-white/10 bg-white/5 px-2.5 py-0.5 text-[11px] text-slate-400">
              {t('plugin.trustKeyCount', { n: keys.length })}
            </span>
          )}
        </div>

        {keys.length === 0 ? (
          data.error ? (
            // 文件读不出来 ≠ 空库：这里不能显示"当前没有信任任何发布者"
            <div className="px-5 py-8 text-center">
              <ShieldX className="mx-auto mb-2 h-8 w-8 text-rose-300 opacity-70" />
              <p className="text-sm text-rose-300">{t('plugin.trustStoreBroken')}</p>
            </div>
          ) : (
            <div className="px-5 py-8 text-center">
              <ShieldQuestion className="mx-auto mb-2 h-8 w-8 text-slate-500 opacity-40" />
              <p className="text-sm text-slate-300">{t('plugin.trustKeyEmpty')}</p>
              <p className="mt-1 text-xs text-slate-500">{t('plugin.trustEmptyHint')}</p>
            </div>
          )
        ) : (
          <>
            <div className="hidden grid-cols-[1.3fr_1.1fr_1fr_auto] gap-3 border-b border-white/10 px-5 py-2 text-[10px] uppercase tracking-wider text-slate-500 md:grid">
              <span>{t('plugin.trustKeyName')}</span>
              <span>{t('plugin.trustKeyId')}</span>
              <span>{t('plugin.trustKeyAddedAt')}</span>
              <span />
            </div>
            <div className="divide-y divide-white/10">
              {keys.map((k) => (
                <div
                  key={k.keyId}
                  className="grid grid-cols-1 gap-2 px-5 py-3 md:grid-cols-[1.3fr_1.1fr_1fr_auto] md:items-center md:gap-3"
                >
                  <div className="min-w-0">
                    <div className="break-all text-sm text-white">{k.name || '—'}</div>
                    {k.publicKey ? (
                      <span className="mt-1 inline-block rounded-full border border-emerald-400/20 bg-emerald-500/10 px-2 py-0.5 text-[10px] text-emerald-300">
                        {t('plugin.trustKeyHasPublic')}
                      </span>
                    ) : (
                      // 只登记 keyId 挡不住"同一个 keyId 配了别人的公钥"
                      <span
                        className="mt-1 inline-block rounded-full border border-amber-400/25 bg-amber-500/10 px-2 py-0.5 text-[10px] text-amber-300"
                        title={t('plugin.trustKeyIdOnlyWarn')}
                      >
                        {t('plugin.trustKeyNoPublic')}
                      </span>
                    )}
                  </div>
                  <code className="break-all rounded bg-white/5 px-1.5 py-0.5 font-mono text-[11px] text-slate-400">
                    {k.keyId}
                  </code>
                  <span className="break-all text-[11px] text-slate-400">{k.addedAt || '—'}</span>
                  <button
                    type="button"
                    onClick={() => setRemoving(k)}
                    disabled={busy === 'remove:' + k.keyId}
                    className="inline-flex w-fit shrink-0 items-center gap-1 rounded-lg border border-rose-400/25 px-2.5 py-1 text-[11px] text-rose-300 hover:bg-rose-500/15 disabled:opacity-40"
                  >
                    {busy === 'remove:' + k.keyId
                      ? <Loader2 className="h-3 w-3 animate-spin" />
                      : <Trash2 className="h-3 w-3" />}
                    {t('plugin.trustKeyRemove')}
                  </button>
                </div>
              ))}
            </div>
          </>
        )}
      </div>

      {/* 添加公钥 */}
      <div className="glass rounded-2xl p-5">
        <div className="flex items-center gap-2 text-sm font-semibold">
          <Plus className="h-4 w-4 text-nebula-300" />
          {t('plugin.trustAddTitle')}
        </div>
        <p className="mt-1 text-xs leading-relaxed text-slate-400">{t('plugin.trustAddHint')}</p>

        {/* 私钥被后端挡住时用最醒目的样式原样展示它的 message */}
        {addErr && (addIsPrivateKey ? (
          <div className="mt-3 rounded-xl border-2 border-rose-400/60 bg-rose-500/20 px-4 py-3">
            <div className="flex items-center gap-2 text-sm font-semibold text-rose-200">
              <ShieldX className="h-4 w-4" />
              {t('plugin.trustPrivateKeyWarn')}
            </div>
            <p className="mt-2 break-all text-xs leading-relaxed text-rose-100">{addErr}</p>
          </div>
        ) : (
          <div className="mt-3 rounded-lg border border-rose-400/30 bg-rose-500/10 px-3 py-2 text-xs break-all text-rose-300">
            {addErr}
          </div>
        ))}

        {/* 只登记 keyId 时后端会给警告，必须显示出来 */}
        {addWarning && (
          <div className="mt-3 flex items-start gap-2 rounded-lg border border-amber-400/25 bg-amber-500/10 px-3 py-2 text-xs text-amber-200">
            <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            <span className="break-all">
              <span className="font-semibold">{t('plugin.trustKeyIdOnlyWarn')}: </span>
              {addWarning}
            </span>
          </div>
        )}

        <div className="mt-3 flex flex-col gap-2 sm:flex-row sm:items-center">
          <input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            spellCheck={false}
            placeholder={t('plugin.trustAddPublicKeyPlaceholder')}
            className="glass-input min-w-0 flex-1 font-mono text-xs"
          />
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={t('plugin.trustAddNamePlaceholder')}
            className="glass-input sm:w-56"
          />
          <button
            type="button"
            onClick={doAdd}
            disabled={!input.trim() || !name.trim() || busy === 'add'}
            className="btn-primary h-9 shrink-0 px-4"
          >
            {busy === 'add'
              ? <Loader2 className="h-4 w-4 animate-spin" />
              : <Plus className="h-4 w-4" />}
            {t('plugin.trustAddSubmit')}
          </button>
        </div>
        <p className="mt-2 text-[11px] text-slate-500">{t('plugin.trustAddKeyIdPlaceholder')}</p>
      </div>

      {/* 移除确认 */}
      {removing && (
        <div className="fixed inset-0 z-[100] flex items-center justify-center p-4">
          <div
            className="absolute inset-0 bg-black/70 backdrop-blur-sm"
            onClick={() => setRemoving(null)}
          />
          <div className="glass-strong relative z-10 w-full max-w-md rounded-2xl border border-white/15 p-6">
            <div className="mb-3 flex items-center gap-2">
              <Trash2 className="h-5 w-5 text-rose-300" />
              <h3 className="text-base font-semibold text-white">{t('plugin.trustKeyRemove')}</h3>
            </div>
            <p className="mb-3 text-sm leading-relaxed text-slate-300">
              {t('plugin.trustKeyRemoveConfirm')}
            </p>
            <div className="mb-4 rounded-lg border border-white/10 bg-black/30 px-3 py-2">
              <div className="break-all text-xs text-slate-300">{removing.name || '—'}</div>
              <code className="break-all font-mono text-[11px] text-slate-500">
                {removing.keyId}
              </code>
            </div>
            <div className="flex justify-end gap-2">
              <button type="button" onClick={() => setRemoving(null)} className="btn-ghost">
                {t('common.cancel')}
              </button>
              <button
                type="button"
                onClick={doRemove}
                disabled={busy === 'remove:' + removing.keyId}
                className="btn-primary"
              >
                {busy === 'remove:' + removing.keyId
                  ? <Loader2 className="h-4 w-4 animate-spin" />
                  : <Trash2 className="h-4 w-4" />}
                {t('plugin.trustKeyRemove')}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 切到 require 的二次确认：内容来自后端 409 的 message */}
      {pendingMode && (
        <div className="fixed inset-0 z-[100] flex items-center justify-center p-4">
          <div
            className="absolute inset-0 bg-black/70 backdrop-blur-sm"
            onClick={() => setPendingMode(null)}
          />
          <div className="glass-strong relative z-10 w-full max-w-md rounded-2xl border border-white/15 p-6">
            <div className="mb-3 flex items-center gap-2">
              <ShieldAlert className="h-5 w-5 text-amber-300" />
              <h3 className="text-base font-semibold text-white">
                {t('plugin.trustModeConfirmTitle')}
              </h3>
            </div>
            <p className="mb-3 text-sm leading-relaxed text-slate-300">
              {t('plugin.trustModeConfirmBody')}
            </p>
            {pendingMode.message && (
              <p className="mb-3 break-all rounded-lg border border-amber-400/25 bg-amber-500/10 px-3 py-2 text-xs leading-relaxed text-amber-200">
                {pendingMode.message}
              </p>
            )}
            {typeof pendingMode.unsigned === 'number' && (
              <p className="mb-3 text-xs text-slate-400">
                {t('plugin.sigBadgeNone')}: {pendingMode.unsigned}
              </p>
            )}
            <div className="flex justify-end gap-2">
              <button type="button" onClick={() => setPendingMode(null)} className="btn-ghost">
                {t('common.cancel')}
              </button>
              <button
                type="button"
                onClick={() => applyMode(pendingMode.mode, true)}
                disabled={busy === 'mode'}
                className="btn-primary"
              >
                {busy === 'mode'
                  ? <Loader2 className="h-4 w-4 animate-spin" />
                  : <ShieldCheck className="h-4 w-4" />}
                {t('plugin.trustModeRequire')}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
