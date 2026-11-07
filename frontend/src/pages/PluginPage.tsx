import { useEffect, useMemo, useState } from 'react'
import { useParams } from 'react-router-dom'
import { ExternalLink, Maximize2, Minimize2, RefreshCw, ShieldAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import MainLayout from '../layouts/MainLayout'
import {
  normalizePageURL,
  shouldOpenNewTab,
  usePluginUIStore,
} from '../store/pluginUI'
import type { PluginSidebarItem } from '../api/client'

/**
 * 插件页面容器：把插件自己起的 HTTP 服务页面塞进 iframe。
 *
 * 这里刻意不做任何鉴权注入 —— 按站长的选择，宿主对 /ext/* 完全透传，
 * 插件自己负责登录与权限。前端只负责：
 *   - 从 /api/plugins/ui 的声明里找到对应页面 URL
 *   - 归一化成同源相对路径（后端可能给 127.0.0.1 绝对地址）
 *   - 提供刷新 / 新窗口 / 全屏，以及一条显眼的风险提示
 */
export default function PluginPage() {
  const { plugin = '', page = '' } = useParams()
  const { t } = useTranslation()
  const data = usePluginUIStore((s) => s.data)
  const loaded = usePluginUIStore((s) => s.loaded)
  const refresh = usePluginUIStore((s) => s.refresh)
  const [nonce, setNonce] = useState(0)
  const [fullscreen, setFullscreen] = useState(false)

  useEffect(() => {
    if (!loaded) void refresh()
  }, [loaded, refresh])

  const item: PluginSidebarItem | undefined = useMemo(() => {
    if (!data) return undefined
    const all = [
      ...(data.sidebar.main || []),
      ...(data.sidebar.admin || []),
      ...(data.public || []),
    ]
    return all.find((it) => it.plugin === plugin && it.name === page)
  }, [data, plugin, page])

  const src = item ? normalizePageURL(item.url || '') : ''
  const external = item ? shouldOpenNewTab(item) : false
  const crumbs = [
    { name: t('pluginUI.extensions') },
    { name: item?.title || page },
  ]

  const body = (
    <div className={fullscreen
      ? 'fixed inset-0 z-40 flex h-screen w-screen flex-col bg-[#0b1020] p-3'
      : 'flex h-full min-h-[420px] flex-col'}>
      <div className="mb-2 flex items-start gap-2 rounded-lg border border-amber-400/25 bg-amber-500/10 px-3 py-2 text-[11px] leading-relaxed text-amber-200">
        <ShieldAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
        <span>{t('pluginUI.pageRisk')}</span>
      </div>

      <div className="mb-2 flex items-center gap-2">
        <div className="min-w-0 flex-1 truncate text-xs text-slate-400" title={src}>
          {src || t('pluginUI.pageMissing')}
        </div>
        <button
          type="button"
          onClick={() => setNonce((n) => n + 1)}
          className="btn-ghost flex items-center gap-1.5 px-2.5 py-1.5 text-xs"
          title={t('common.refresh')}
        >
          <RefreshCw className="h-3.5 w-3.5" />
        </button>
        {src && (
          <a
            href={src}
            target="_blank"
            rel="noopener noreferrer"
            className="btn-ghost flex items-center gap-1.5 px-2.5 py-1.5 text-xs"
            title={t('pluginUI.openNewTab')}
          >
            <ExternalLink className="h-3.5 w-3.5" />
          </a>
        )}
        <button
          type="button"
          onClick={() => setFullscreen((v) => !v)}
          className="btn-ghost flex items-center gap-1.5 px-2.5 py-1.5 text-xs"
          title={fullscreen ? t('pluginUI.exitFullscreen') : t('pluginUI.fullscreen')}
        >
          {fullscreen
            ? <Minimize2 className="h-3.5 w-3.5" />
            : <Maximize2 className="h-3.5 w-3.5" />}
        </button>
      </div>

      <div className="glass min-h-0 flex-1 overflow-hidden rounded-xl">
        {!item && loaded ? (
          <div className="grid h-full place-items-center px-6 text-center text-sm text-slate-400">
            {t('pluginUI.pageMissing')}
          </div>
        ) : src ? (
          <iframe
            key={`${plugin}-${page}-${nonce}`}
            src={src}
            title={`${plugin}/${page}`}
            className="h-full w-full border-0"
            // 不设 sandbox：插件页面需要脚本与同源能力，宿主已把 /ext/* 全权交给插件。
            // 这也是「开启插件前必须确认风险」的原因。
            allow="fullscreen; clipboard-read; clipboard-write"
          />
        ) : (
          <div className="grid h-full place-items-center text-sm text-slate-400">
            {t('common.loading')}
          </div>
        )}
      </div>

      {external && (
        <p className="mt-2 text-[11px] text-slate-400">
          {t('pluginUI.externalHint')}
        </p>
      )}
    </div>
  )

  return (
    <>
      <MainLayout breadcrumb={crumbs}>
        {fullscreen ? <div className="h-full" /> : body}
      </MainLayout>
      {fullscreen && body}
    </>
  )
}
