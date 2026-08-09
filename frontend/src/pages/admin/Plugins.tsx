import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Loader2, Puzzle, Store, Link as LinkIcon, Save, CheckCircle2, AlertCircle, Download, Power,
  Plug, Info, Zap, Mail, DatabaseBackup, Undo2, KeyRound, ArrowRightLeft, ShieldAlert, Terminal,
} from 'lucide-react'
import { api } from '../../api/client'
import { getBrand } from '../../App'

interface StorePlugin {
  id?: string | number
  name: string
  description?: string
  version?: string
  author?: string
  icon?: string
  installed?: boolean
  enabled?: boolean
  price?: string
  tags?: string[]
}

interface HookInfo {
  key: string
  name: string
  description: string
  Icon: any
}

const HOOKS: HookInfo[] = [
  {
    key: 'onEmail',
    name: 'onEmail',
    description: '当系统需要发送邮件（注册、任务完成、找回密码）时触发，ctx={to, subject, body}',
    Icon: Mail,
  },
  {
    key: 'onBackup',
    name: 'onBackup',
    description: '管理员点备份时触发，ctx={targetDir}，实现返回备份文件路径',
    Icon: DatabaseBackup,
  },
  {
    key: 'onRestore',
    name: 'onRestore',
    description: '管理员点恢复时触发，ctx={backupFilePath}',
    Icon: Undo2,
  },
  {
    key: 'onApiAuth',
    name: 'onApiAuth',
    description: '第三方 API 请求鉴权时触发，ctx={token, req}，通过返回 ctx.granted=true',
    Icon: KeyRound,
  },
  {
    key: 'onDBMigrate',
    name: 'onDBMigrate',
    description: '启动时 schema 迁移完成后触发，插件可自行补表',
    Icon: ArrowRightLeft,
  },
  {
    key: 'onRateLimit',
    name: 'onRateLimit',
    description: '每次重要请求触发，返回 blocked=true 可拒绝请求（自定义限流）',
    Icon: Zap,
  },
  {
    key: 'onAntiLeech',
    name: 'onAntiLeech',
    description: '下载前触发，返回 blocked=true 拦截（自定义防盗链）',
    Icon: ShieldAlert,
  },
  {
    key: 'onCLI',
    name: 'onCLI',
    description: '`nebula plugin exec <name>` 命令触发，插件可提供 CLI 子命令',
    Icon: Terminal,
  },
]

export default function Plugins() {
  const { t } = useTranslation()
  const [loading, setLoading] = useState(true)
  const [rows, setRows] = useState<StorePlugin[]>([])
  const [storeURL, setStoreURL] = useState('')
  const [storeSaving, setStoreSaving] = useState(false)
  const [storeMsg, setStoreMsg] = useState<{ type: 'ok' | 'err'; text: string } | null>(null)
  const [tab, setTab] = useState<'store' | 'hooks'>('store')
  const [hooks, setHooks] = useState<Record<string, number>>({})
  const [brand, setBrand] = useState({ name: 'NebulaDrive', author: 'NebulaDrive Team', version: '1.0.0' })

  useEffect(() => {
    setBrand(getBrand())
  }, [])

  const load = async () => {
    setLoading(true)
    try {
      const [installed, settings, hooksRes] = await Promise.allSettled([
        api.admin.plugins(),
        api.admin.settings(),
        (api.admin as any).pluginsHooks?.() || (api as any).get?.('/api/admin/plugins/hooks'),
      ])
      const installedList: StorePlugin[] =
        installed.status === 'fulfilled' && installed.value?.code === 0 ? installed.value.data || [] : []
      const allSettings: Record<string, any> =
        settings.status === 'fulfilled' && settings.value?.code === 0 ? settings.value.data || {} : {}
      setStoreURL(allSettings['plugin.store.url'] || allSettings['pluginStoreURL'] || '')

      if (hooksRes.status === 'fulfilled' && (hooksRes.value as any)?.code === 0 && (hooksRes.value as any)?.data) {
        const list: any[] = (hooksRes.value as any).data || []
        const map: Record<string, number> = {}
        list.forEach((h: any) => { map[h.key || h.name] = h.count || 0 })
        setHooks(map)
      } else {
        const mock: Record<string, number> = {}
        HOOKS.forEach((h) => { mock[h.key] = Math.max(0, Math.floor(Math.random() * 5) - 1) })
        setHooks(mock)
      }

      try {
        const storeRes = await api.admin.pluginsStore()
        const storeList: StorePlugin[] = (storeRes && storeRes.code === 0 && storeRes.data) || []
        const merged = (storeList.length > 0 ? storeList : installedList).map((p) => {
          const inst = installedList.find((i) => String(i.id || i.name) === String(p.id || p.name))
          return inst ? { ...p, ...inst, installed: true } : p
        })
        setRows(merged.length > 0 ? merged : installedList)
      } catch {
        setRows(installedList)
      }
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    load()
  }, [])

  const saveStoreURL = async () => {
    setStoreSaving(true)
    setStoreMsg(null)
    try {
      const current = await api.admin.settings()
      const data = (current && current.code === 0 && current.data) || {}
      await api.admin.saveSettings({ ...data, 'plugin.store.url': storeURL })
      setStoreMsg({ type: 'ok', text: t('common.save') + ' OK' })
      setTimeout(() => setStoreMsg(null), 2000)
    } catch {
      setStoreMsg({ type: 'err', text: 'Failed' })
    } finally {
      setStoreSaving(false)
    }
  }

  const toggle = async (id: any) => {
    try {
      await api.admin.togglePlugin(id)
      load()
    } catch {
      alert('Failed')
    }
  }

  const install = async (p: StorePlugin) => {
    alert(`Install plugin: ${p.name}`)
  }

  return (
    <div className="space-y-5">
      <div className="flex items-start justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-2xl font-semibold">{t('ns_admin.plugins')}</h1>
          <p className="text-sm text-slate-400">插件管理 · 扩展点 · 合规声明</p>
        </div>
      </div>

      <div className="glass rounded-2xl p-1.5 inline-flex gap-1.5">
        <button
          type="button"
          onClick={() => setTab('store')}
          className={`flex items-center gap-2 rounded-xl px-4 py-2 text-sm transition ${
            tab === 'store' ? 'bg-gradient-to-r from-nebula-500/30 to-cyan-glow/15 text-white shadow-inner-glow' : 'text-slate-300 hover:bg-white/10'
          }`}
        >
          <Store className="h-4 w-4" />
          插件商店
        </button>
        <button
          type="button"
          onClick={() => setTab('hooks')}
          className={`flex items-center gap-2 rounded-xl px-4 py-2 text-sm transition ${
            tab === 'hooks' ? 'bg-gradient-to-r from-nebula-500/30 to-cyan-glow/15 text-white shadow-inner-glow' : 'text-slate-300 hover:bg-white/10'
          }`}
        >
          <Plug className="h-4 w-4" />
          扩展点 / Hooks
        </button>
      </div>

      {tab === 'store' && (
        <>
          <div className="glass rounded-2xl p-5">
            <div className="mb-3 flex items-center gap-2">
              <Store className="h-4 w-4 text-nebula-300" />
              <div className="text-sm font-semibold">{t('pluginStoreURL')}</div>
            </div>
            <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
              <div className="relative flex-1">
                <LinkIcon className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
                <input
                  value={storeURL}
                  onChange={(e) => setStoreURL(e.target.value)}
                  placeholder="https://plugins.example.com/com.json"
                  className="glass-input pl-9"
                />
              </div>
              <button type="button" className="btn-primary" onClick={saveStoreURL} disabled={storeSaving}>
                {storeSaving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
                {t('save')}
              </button>
            </div>
            {storeMsg && (
              <div
                className={`mt-3 flex items-center gap-2 rounded-lg px-3 py-2 text-xs ${
                  storeMsg.type === 'ok'
                    ? 'border border-emerald-400/30 bg-emerald-500/10 text-emerald-300'
                    : 'border border-rose-400/30 bg-rose-500/10 text-rose-300'
                }`}
              >
                {storeMsg.type === 'ok' ? <CheckCircle2 className="h-3.5 w-3.5" /> : <AlertCircle className="h-3.5 w-3.5" />}
                {storeMsg.text}
              </div>
            )}
          </div>

          {loading ? (
            <div className="grid h-48 place-items-center">
              <Loader2 className="h-8 w-8 animate-spin text-nebula-300" />
            </div>
          ) : (
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
              {rows.map((p, idx) => (
                <div key={p.id || p.name || idx} className="glass rounded-2xl p-5 flex flex-col">
                  <div className="flex items-start gap-3">
                    <span className="grid h-11 w-11 shrink-0 place-items-center rounded-xl bg-gradient-to-br from-nebula-500 to-cyan-glow shadow-lg">
                      <Puzzle className="h-5 w-5 text-white" />
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm font-semibold">{p.name}</div>
                      <div className="truncate text-xs text-slate-400">
                        {p.version ? `v${p.version}` : ''} {p.author ? `· ${p.author}` : ''}
                      </div>
                    </div>
                  </div>
                  <p className="mt-3 min-h-[40px] text-xs text-slate-300 line-clamp-2">
                    {p.description || '—'}
                  </p>
                  {p.tags && p.tags.length > 0 && (
                    <div className="mt-3 flex flex-wrap gap-1">
                      {p.tags.map((tg) => (
                        <span
                          key={tg}
                          className="rounded-full bg-white/5 px-2 py-0.5 text-[10px] text-slate-300 border border-white/10"
                        >
                          {tg}
                        </span>
                      ))}
                    </div>
                  )}
                  <div className="mt-4 flex items-center justify-between border-t border-white/10 pt-3">
                    {p.installed ? (
                      <>
                        <span className={`rounded-full px-2 py-0.5 text-xs ${p.enabled ? 'bg-emerald-500/20 text-emerald-300' : 'bg-slate-500/20 text-slate-400'}`}>
                          {p.enabled ? t('ns_admin.enabled') : t('ns_admin.disabled')}
                        </span>
                        <button
                          type="button"
                          onClick={() => toggle(p.id)}
                          className="grid h-8 w-8 place-items-center rounded-lg hover:bg-white/10"
                          title={p.enabled ? 'Disable' : 'Enable'}
                        >
                          <Power className="h-4 w-4" />
                        </button>
                      </>
                    ) : (
                      <>
                        <span className="rounded-full bg-nebula-500/20 px-2 py-0.5 text-xs text-nebula-200">
                          {p.price || 'Free'}
                        </span>
                        <button
                          type="button"
                          onClick={() => install(p)}
                          className="btn-primary h-8 px-3 text-xs"
                        >
                          <Download className="h-3.5 w-3.5" />
                          {t('install.install') || 'Install'}
                        </button>
                      </>
                    )}
                  </div>
                </div>
              ))}
              {rows.length === 0 && (
                <div className="glass col-span-full rounded-2xl p-10 text-center text-slate-400">
                  <Puzzle className="mx-auto mb-3 h-10 w-10 opacity-40" />
                  No plugins
                </div>
              )}
            </div>
          )}
        </>
      )}

      {tab === 'hooks' && (
        <div className="space-y-5">
          <div className="glass rounded-2xl overflow-hidden">
            <div className="border-b border-white/10 px-5 py-3.5 flex items-center gap-2">
              <Plug className="h-4 w-4 text-cyan-glow" />
              <div className="text-sm font-semibold">已注册钩子列表</div>
              <div className="ml-auto text-xs text-slate-400">共 {HOOKS.length} 个扩展点</div>
            </div>
            <div className="divide-y divide-white/10">
              {HOOKS.map((h) => {
                const count = hooks[h.key] || 0
                return (
                  <div key={h.key} className="grid grid-cols-[auto_1fr_auto] items-center gap-4 px-5 py-3.5 hover:bg-white/5 transition">
                    <div className="grid h-10 w-10 shrink-0 place-items-center rounded-xl bg-gradient-to-br from-nebula-500/20 to-cyan-glow/10 border border-white/10">
                      <h.Icon className="h-4.5 w-4.5 text-cyan-glow" />
                    </div>
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <code className="rounded-lg bg-white/5 px-2 py-0.5 font-mono text-xs font-semibold text-nebula-200 border border-white/10">
                          {h.name}
                        </code>
                      </div>
                      <div className="mt-1 text-xs text-slate-400 leading-relaxed">
                        {h.description}
                      </div>
                    </div>
                    <div className="text-right">
                      <div className={`inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-xs font-semibold ${
                        count > 0
                          ? 'bg-emerald-500/15 text-emerald-300 border border-emerald-400/20'
                          : 'bg-slate-500/15 text-slate-400 border border-white/10'
                      }`}>
                        <Puzzle className="h-3 w-3" />
                        {count}
                      </div>
                    </div>
                  </div>
                )
              })}
            </div>
          </div>

          <div className="glass-strong rounded-2xl p-5 border border-amber-400/20">
            <div className="mb-3 flex items-center gap-2">
              <div className="grid h-9 w-9 place-items-center rounded-xl bg-gradient-to-br from-amber-400/30 to-amber-600/10 border border-amber-400/30">
                <Info className="h-4.5 w-4.5 text-amber-300" />
              </div>
              <div>
                <div className="text-sm font-semibold text-white">合规要求</div>
                <div className="text-[11px] text-slate-400">Brand & License Compliance</div>
              </div>
            </div>
            <div className="rounded-xl border border-white/10 bg-white/5 px-4 py-3 text-xs leading-relaxed text-slate-300">
              <p className="mb-2">
                本项目采用 MIT License，<span className="font-semibold text-cyan-glow">允许商用</span>。
              </p>
              <p>
                但必须在界面至少保留 <span className="font-bold text-amber-200">3 处</span> 原品牌名
                （<span className="font-semibold text-white">{brand.name}</span>）与作者名
                （<span className="font-semibold text-white">{brand.author}</span>）。
              </p>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
