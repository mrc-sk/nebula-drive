import { create } from 'zustand'
import {
  api,
  type PluginSidebarItem,
  type PluginThemeDecl,
  type PluginUIData,
} from '../api/client'

/**
 * 插件 UI 扩展点的前端状态。
 *
 * 后端 /api/plugins/ui 把「插件声明的侧边栏 / 主题 / 设置面板 / 注册型能力」
 * 聚合成一份数据。前端只做声明式渲染，不执行插件代码：
 *   - 侧边栏 → 动态 NavLink
 *   - 插件页面 → iframe 指向 /ext/<plugin>/<page>（宿主反代）
 *   - 主题 → CSS 变量覆盖 + 可选 <link> 样式表
 *   - 设置面板 → 按 SettingField 生成表单
 *
 * 插件系统默认关闭，接口可能返回空数据或直接失败 —— 一律静默降级，
 * 绝不能因为插件没开就把主界面搞白屏。
 */

const STYLE_ID = 'nebula-plugin-theme-style'
const LINK_ID = 'nebula-plugin-theme-link'
const ACTIVE_THEME_KEY = 'nebula.pluginTheme'

/** 后台切换插件总开关 / 启停插件后广播，让前端重新拉一次 UI 声明。 */
export const PLUGIN_UI_CHANGED = 'nebula:plugins-changed'

function emptyData(): PluginUIData {
  return {
    protocolVersion: 2,
    globalEnabled: false,
    sidebar: { main: [], admin: [] },
    public: [],
    themes: {},
    settings: {},
    providers: [],
    variables: {},
    dynamic: {},
  }
}

/**
 * 后端按「域名 → 绑定 IP → 默认 127.0.0.1」生成的是绝对 URL。
 * 浏览器里 127.0.0.1 指向用户自己的机器，直接用必然打不开。
 * 这里把 loopback / 同源绝对 URL 收敛成同源相对路径，让请求落到当前站点
 * 的 /ext/* 反代上；真正的外部 https 页面保持原样。
 */
export function normalizePageURL(raw: string): string {
  if (!raw) return ''
  if (raw.startsWith('/') && !raw.startsWith('//')) return raw
  try {
    const u = new URL(raw, window.location.href)
    const h = u.hostname.toLowerCase()
    const loopback =
      h === '127.0.0.1' || h === 'localhost' || h === '0.0.0.0' ||
      h === '::1' || h === '[::1]'
    if (loopback || u.origin === window.location.origin) {
      return u.pathname + u.search + u.hash
    }
    return u.toString()
  } catch {
    return raw
  }
}

/** 站内 iframe 容器路由，避免把插件 URL 直接塞进地址栏。 */
export function pluginPagePath(plugin: string, page: string): string {
  return `/ext-page/${encodeURIComponent(plugin)}/${encodeURIComponent(page)}`
}

/** 外部/新标签页判定：插件声明 newtab，或 URL 指向别的站点。 */
export function shouldOpenNewTab(item: PluginSidebarItem): boolean {
  if (item.embed === 'newtab') return true
  const url = normalizePageURL(item.url || '')
  return /^https?:\/\//i.test(url)
}

function normalizeItems(items: any): PluginSidebarItem[] {
  if (!Array.isArray(items)) return []
  return items.map((it) => ({
    ...it,
    url: normalizePageURL(it?.url || ''),
  }))
}

function normalizeData(raw: any): PluginUIData {
  const d = raw || {}
  return {
    protocolVersion: Number(d.protocolVersion) || 2,
    globalEnabled: !!d.globalEnabled,
    sidebar: {
      main: normalizeItems(d.sidebar?.main),
      admin: normalizeItems(d.sidebar?.admin),
    },
    public: normalizeItems(d.public),
    themes: d.themes && typeof d.themes === 'object' ? d.themes : {},
    settings: d.settings && typeof d.settings === 'object' ? d.settings : {},
    providers: Array.isArray(d.providers) ? d.providers : [],
    variables: d.variables && typeof d.variables === 'object' ? d.variables : {},
    dynamic: d.dynamic && typeof d.dynamic === 'object' ? d.dynamic : {},
  }
}

// ---- 主题应用：只碰两个专属 DOM 节点，卸载时精确清理 ----

function removeThemeNodes() {
  document.getElementById(STYLE_ID)?.remove()
  document.getElementById(LINK_ID)?.remove()
}

const CSS_KEY_RE = /^--[A-Za-z0-9_-]+$/

function safeCSSValue(v: string): string | null {
  const s = String(v).trim()
  // 插件声明的 CSS 变量值不允许闭合规则、注入选择器或加载 url()。
  if (!s || /[;{}<>]/.test(s) || /url\s*\(/i.test(s) || s.length > 256) return null
  return s
}

function buildVars(...sources: (Record<string, any> | undefined)[]): string {
  const out: string[] = []
  for (const src of sources) {
    if (!src) continue
    for (const [k, v] of Object.entries(src)) {
      if (!CSS_KEY_RE.test(k)) continue
      const safe = safeCSSValue(String(v))
      if (safe) out.push(`${k}: ${safe};`)
    }
  }
  return out.join('\n  ')
}

function themeCSSURL(plugin: string, css: string): string {
  const c = String(css || '').trim()
  if (!c) return ''
  if (/^https?:\/\//i.test(c)) return normalizePageURL(c)
  if (c.startsWith('/')) return c
  return `/ext/${encodeURIComponent(plugin)}/${c.replace(/^\.\//, '')}`
}

function applyTheme(data: PluginUIData | null, active: string) {
  removeThemeNodes()
  if (!data) return

  const theme: PluginThemeDecl | undefined = active ? data.themes?.[active] : undefined
  const vars = buildVars(data.variables, theme?.variables)
  if (vars) {
    const style = document.createElement('style')
    style.id = STYLE_ID
    style.textContent = `:root {\n  ${vars}\n}`
    document.head.appendChild(style)
  }
  const css = theme?.css ? themeCSSURL(active, theme.css) : ''
  if (css) {
    const link = document.createElement('link')
    link.id = LINK_ID
    link.rel = 'stylesheet'
    link.href = css
    document.head.appendChild(link)
  }
}

function readActiveTheme(): string {
  try {
    return localStorage.getItem(ACTIVE_THEME_KEY) || ''
  } catch {
    return ''
  }
}

interface PluginUIState {
  data: PluginUIData | null
  loading: boolean
  loaded: boolean
  /** '' = 内置主题；否则是插件名（themes 的 key） */
  activeTheme: string
  refresh: () => Promise<void>
  setActiveTheme: (plugin: string) => void
  findItem: (plugin: string, page: string) => PluginSidebarItem | undefined
}

export const usePluginUIStore = create<PluginUIState>((set, get) => ({
  data: null,
  loading: false,
  loaded: false,
  activeTheme: readActiveTheme(),

  refresh: async () => {
    if (get().loading) return
    set({ loading: true })
    let next: PluginUIData = emptyData()
    try {
      const r = await api.pluginsUI()
      if (r?.code === 0 && r.data) next = normalizeData(r.data)
    } catch {
      // 插件系统关闭、接口不存在（老版本后端）或网络异常：静默降级
      next = emptyData()
    }
    // 选中的插件主题可能已经被卸载，自动回退到内置主题
    const active = next.themes?.[get().activeTheme] ? get().activeTheme : ''
    if (active !== get().activeTheme) {
      try { localStorage.setItem(ACTIVE_THEME_KEY, active) } catch { /* ignore */ }
    }
    set({ data: next, loading: false, loaded: true, activeTheme: active })
    applyTheme(next, active)
  },

  setActiveTheme: (plugin: string) => {
    const active = plugin && get().data?.themes?.[plugin] ? plugin : ''
    try { localStorage.setItem(ACTIVE_THEME_KEY, active) } catch { /* ignore */ }
    set({ activeTheme: active })
    applyTheme(get().data, active)
  },

  findItem: (plugin: string, page: string) => {
    const d = get().data
    if (!d) return undefined
    const all = [
      ...(d.sidebar.main || []),
      ...(d.sidebar.admin || []),
      ...(d.public || []),
    ]
    return all.find((it) => it.plugin === plugin && it.name === page)
  },
}))

/** 供后台在启停插件 / 切换总开关后调用，立即刷新侧边栏与主题。 */
export function notifyPluginUIChanged() {
  window.dispatchEvent(new Event(PLUGIN_UI_CHANGED))
}
