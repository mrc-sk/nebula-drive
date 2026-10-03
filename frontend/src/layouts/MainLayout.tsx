import { useEffect, useRef, useState } from 'react'
import { Outlet, NavLink, useNavigate, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import {
  Menu,
  X,
  Home,
  FolderOpen,
  Share2,
  Download,
  Trash2,
  ShieldCheck,
  ChevronDown,
  ChevronsDownUp,
  ChevronsUpDown,
  LogOut,
  User,
  Settings as SettingsIcon,
  LayoutDashboard,
  Users,
  Users2,
  Database,
  Settings,
  ListTodo,
  Puzzle,
  Info,
} from 'lucide-react'
import { LANGUAGES } from '../i18n'
import { useThemeStore } from '../store/theme'
import ThemeToggle from '../components/ThemeToggle'
import LangSwitcher from '../components/LangSwitcher'
import NotificationBell from '../components/NotificationBell'
import ProfileSettings from '../components/ProfileSettings'
import { useAuthStore } from '../store/auth'
import { useUserPrefsStore } from '../store/user'
import { api } from '../api/client'
import { getBrand } from '../App'

export interface MainLayoutProps {
  mode?: 'app' | 'admin'
  toolbar?: React.ReactNode
  breadcrumb?: { name: string; onClick?: () => void }[]
  allExpanded?: boolean
  onToggleAllExpand?: (next: boolean) => void
  children?: React.ReactNode
}

const APP_MENU = [
  { to: '/files', key: 'home', Icon: Home, labelKey: 'home' },
  { to: '/files', key: 'files', Icon: FolderOpen, labelKey: 'files' },
  { to: '/share-list', key: 'shares', Icon: Share2, labelKey: 'shares' },
  { to: '/tasks', key: 'tasksOffline', Icon: Download, labelKey: 'tasksOffline' },
  { to: '/trash', key: 'recycle', Icon: Trash2, labelKey: 'recycle' },
] as const

const ADMIN_SUB = [
  { to: '/admin/dashboard', key: 'dashboard', Icon: LayoutDashboard, labelKey: 'dashboard' },
  { to: '/admin/users', key: 'users', Icon: Users, labelKey: 'users' },
  { to: '/admin/groups', key: 'groups', Icon: Users2, labelKey: 'groups' },
  { to: '/admin/policies', key: 'policies', Icon: Database, labelKey: 'policies' },
  { to: '/admin/plugins', key: 'plugins', Icon: Puzzle, labelKey: 'plugins' },
  { to: '/admin/settings', key: 'settings', Icon: Settings, labelKey: 'settings' },
  { to: '/admin/about', key: 'about', Icon: Info, labelKey: 'about' },
] as const

export default function MainLayout(props: MainLayoutProps) {
  const { mode = 'app', toolbar, breadcrumb, allExpanded, onToggleAllExpand, children } = props
  const { t } = useTranslation()
  const nav = useNavigate()
  const loc = useLocation()
  const user = useAuthStore((s) => s.user)
  const clearAuth = useAuthStore((s) => s.clearAuth)
  const prefs = useUserPrefsStore()
  const [collapsed, setCollapsed] = useState(false)
  const [mobileOpen, setMobileOpen] = useState(false)
  const [isMobile, setIsMobile] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const [prefsOpen, setPrefsOpen] = useState(false)
  const [brand, setBrand] = useState({ name: 'NebulaDrive', author: 'NebulaDrive Team', version: '1.0.0' })
  const userRef = useRef<HTMLDivElement>(null)
  const avatarClickTimes = useRef<number[]>([])

  useEffect(() => {
    setBrand(getBrand())
    let alive = true
    const load = async () => {
      try {
        const r = await (api.admin as any).settings?.()
        if (alive && r?.code === 0 && r.data) {
          const d = r.data || {}
          setBrand({
            name: d['brand.name'] || d.brandName || 'NebulaDrive',
            author: d['brand.author'] || d.brandAuthor || 'NebulaDrive Team',
            version: d['brand.version'] || d.brandVersion || '1.0.0',
          })
        }
      } catch {}
    }
    load()
    return () => { alive = false }
  }, [])

  useEffect(() => {
    const check = () => {
      const m = window.innerWidth < 768
      setIsMobile(m)
      if (m) setCollapsed(true)
    }
    check()
    window.addEventListener('resize', check)
    return () => window.removeEventListener('resize', check)
  }, [])

  useEffect(() => {
    const onClick = (e: MouseEvent) => {
      if (userRef.current && !userRef.current.contains(e.target as Node)) {
        setMenuOpen(false)
        setPrefsOpen(false)
      }
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [])

  const handleLogout = async () => {
    try { await api.logout() } catch { /* ignore */ }
    clearAuth()
    nav('/login', { replace: true })
  }

  const showRedDot = prefs.suggest2FAHint || !prefs.twoFAEnabled
  const initial = (user?.userName || user?.nickname || 'A').charAt(0).toUpperCase()
  const isAdmin = !!user?.isAdmin
  const currentIsAdmin = mode === 'admin' || loc.pathname.startsWith('/admin')

  const sidebar = (
    <aside
      className={`glass-strong relative flex h-full shrink-0 flex-col border-r border-white/10 transition-[width] duration-300 ${
        collapsed ? 'w-16' : 'w-60'
      } ${isMobile ? 'hidden' : ''}`}
    >
      <SidebarHeader collapsed={collapsed} brand={brand} onToggle={() => setCollapsed((v) => !v)} />
      <nav className="mt-2 flex-1 space-y-1 px-2 overflow-y-auto">
        {APP_MENU.map(({ to, Icon, labelKey }) => (
          <NavLink
            key={to + labelKey}
            to={to}
            className={({ isActive }) =>
              `group flex items-center gap-3 rounded-xl px-3 py-2.5 text-sm transition ${
                isActive && !currentIsAdmin
                  ? 'bg-gradient-to-r from-nebula-500/30 to-cyan-glow/15 text-white shadow-inner-glow'
                  : 'text-slate-200 hover:bg-white/10 hover:text-white'
              } ${collapsed ? 'justify-center px-2' : ''}`
            }
            title={collapsed ? t(labelKey) : undefined}
          >
            <Icon className="h-5 w-5 shrink-0" />
            {!collapsed && <span className="truncate">{t(labelKey)}</span>}
          </NavLink>
        ))}
        {isAdmin && (
          <>
            {!collapsed && (
              <div className="mt-4 mb-1 px-3 text-[10px] uppercase tracking-wider text-slate-400">
                {t('admin')}
              </div>
            )}
            {ADMIN_SUB.map(({ to, Icon, labelKey }) => (
              <NavLink
                key={to}
                to={to}
                className={({ isActive }) =>
                  `group flex items-center gap-3 rounded-xl px-3 py-2.5 text-sm transition ${
                    isActive
                      ? 'bg-gradient-to-r from-nebula-500/30 to-cyan-glow/15 text-white shadow-inner-glow'
                      : 'text-slate-200 hover:bg-white/10 hover:text-white'
                  } ${collapsed ? 'justify-center px-2' : ''}`
                }
                title={collapsed ? t(`ns_admin.${labelKey}`) : undefined}
              >
                <Icon className="h-5 w-5 shrink-0" />
                {!collapsed && <span className="truncate">{t(`ns_admin.${labelKey}`)}</span>}
              </NavLink>
            ))}
          </>
        )}
      </nav>
      <div className="border-t border-white/10 p-2">
        {collapsed ? (
          <div className="grid h-9 w-12 place-items-center rounded-xl bg-gradient-to-br from-nebula-500/80 to-cyan-glow/80 text-xs font-extrabold text-white shadow-inner-glow mx-auto">
            ND
          </div>
        ) : (
          <div className="flex items-center gap-2 px-2 py-1.5">
            <div className="grid h-8 w-8 shrink-0 place-items-center rounded-xl bg-gradient-to-br from-nebula-500 to-cyan-glow text-xs font-extrabold text-white shadow-lg">
              ND
            </div>
            <div className="min-w-0">
              <div className="truncate text-xs font-semibold text-white">{brand.name}</div>
              <div className="truncate text-[10px] text-slate-400">v{brand.version}</div>
            </div>
          </div>
        )}
      </div>
    </aside>
  )

  const mobileDrawer = mobileOpen ? (
    <>
      <div
        className="fixed inset-0 z-50 bg-black/50 backdrop-blur-sm md:hidden"
        onClick={() => setMobileOpen(false)}
      />
      <aside className="glass-strong animate-slide-up fixed left-0 top-0 z-50 h-full w-64 md:hidden">
        <SidebarHeader collapsed={false} brand={brand} onToggle={() => setMobileOpen(false)} />
        <nav className="mt-2 space-y-1 px-2 overflow-y-auto h-[calc(100%-80px)]">
          {APP_MENU.map(({ to, Icon, labelKey }) => (
            <NavLink
              key={to + labelKey + 'm'}
              to={to}
              onClick={() => setMobileOpen(false)}
              className={({ isActive }) =>
                `flex items-center gap-3 rounded-xl px-3 py-2.5 text-sm transition ${
                  isActive && !currentIsAdmin
                    ? 'bg-gradient-to-r from-nebula-500/30 to-cyan-glow/15 text-white'
                    : 'text-slate-200 hover:bg-white/10'
                }`
              }
            >
              <Icon className="h-5 w-5" />
              <span>{t(labelKey)}</span>
            </NavLink>
          ))}
          {isAdmin && (
            <>
              <div className="mt-4 mb-1 px-3 text-[10px] uppercase tracking-wider text-slate-400">
                {t('admin')}
              </div>
              {ADMIN_SUB.map(({ to, Icon, labelKey }) => (
                <NavLink
                  key={to + 'm'}
                  to={to}
                  onClick={() => setMobileOpen(false)}
                  className={({ isActive }) =>
                    `flex items-center gap-3 rounded-xl px-3 py-2.5 text-sm transition ${
                      isActive
                        ? 'bg-gradient-to-r from-nebula-500/30 to-cyan-glow/15 text-white'
                        : 'text-slate-200 hover:bg-white/10'
                    }`
                  }
                >
                  <Icon className="h-5 w-5" />
                  <span>{t(`ns_admin.${labelKey}`)}</span>
                </NavLink>
              ))}
            </>
          )}
        </nav>
      </aside>
    </>
  ) : null

  const topbar = (
    <header className="glass sticky top-0 z-20 flex items-center gap-3 border-b border-white/10 px-3 py-3 md:px-6">
      {isMobile ? (
        <button
          type="button"
          onClick={() => setMobileOpen(true)}
          className="glass grid h-10 w-10 place-items-center rounded-xl"
          aria-label="Menu"
        >
          <Menu className="h-5 w-5" />
        </button>
      ) : null}
      {breadcrumb && breadcrumb.length > 0 ? (
        <div className="flex min-w-0 flex-1 flex-wrap items-center text-sm">
          {breadcrumb.map((b, i) => (
            <span key={i} className={`flex items-center ${i < breadcrumb.length - 1 ? 'crumb-sep' : ''}`}>
              <button
                type="button"
                onClick={b.onClick}
                className={`transition hover:text-cyan-glow ${
                  i === breadcrumb.length - 1 ? 'font-semibold text-white' : 'text-slate-300'
                }`}
              >
                {b.name}
              </button>
            </span>
          ))}
          {typeof allExpanded === 'boolean' && onToggleAllExpand ? (
            <button
              type="button"
              onClick={() => onToggleAllExpand(!allExpanded)}
              className="btn-ghost ml-3 py-1.5 text-xs"
            >
              {allExpanded ? (
                <><ChevronsDownUp className="h-3.5 w-3.5" />{t('collapseAll')}</>
              ) : (
                <><ChevronsUpDown className="h-3.5 w-3.5" />{t('expandAll')}</>
              )}
            </button>
          ) : null}
        </div>
      ) : (
        <div className="flex-1" />
      )}
      {toolbar}
      <ThemeToggle />
      <NotificationBell />
      <LangSwitcher />
      <div className="relative" ref={userRef}>
        <button
          type="button"
          onClick={() => {
            const now = Date.now()
            avatarClickTimes.current.push(now)
            avatarClickTimes.current = avatarClickTimes.current.filter((t) => now - t < 4000)
            if (prefs.eggMode && avatarClickTimes.current.length >= 10) {
              prefs.setAvatarHalo(!prefs.avatarHalo)
              avatarClickTimes.current = []
            }
            setMenuOpen((v) => !v); setPrefsOpen(false)
          }}
          className="glass flex items-center gap-2 rounded-xl px-2 py-1.5 transition hover:bg-white/10"
        >
          <span className={`relative grid h-8 w-8 place-items-center rounded-full bg-gradient-to-br from-nebula-500 to-cyan-glow text-sm font-semibold text-white ${prefs.avatarHalo ? 'avatar-halo' : ''}`}>
            {initial}
            {showRedDot && <span className="red-dot" />}
          </span>
          <span className="hidden text-sm md:inline">{user?.userName || 'User'}</span>
          {showRedDot && <ShieldCheck className="h-4 w-4 text-rose-400 md:hidden" />}
          <ChevronDown className="hidden h-3.5 w-3.5 text-slate-400 md:inline" />
        </button>
        {menuOpen && (
          <div className="glass-strong kebab-pop absolute right-0 mt-2 w-52 overflow-hidden rounded-xl p-1 shadow-glass-lg">
            <div className="flex items-center gap-2 border-b border-white/10 px-3 py-2.5">
              <User className="h-4 w-4 text-slate-300 shrink-0" />
              <div className="min-w-0">
                <div className="truncate text-sm">{user?.userName}</div>
                <div className="truncate text-xs text-slate-400">{user?.email || ''}</div>
              </div>
            </div>
            {showRedDot && (
              <div className="mx-2 my-1 rounded-lg border border-rose-400/20 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
                <span className="font-semibold">{t('2fa')}: </span>{t('suggest2FA')}
              </div>
            )}
            <button
              type="button"
              onClick={() => { setPrefsOpen(true); setMenuOpen(false) }}
              className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm text-slate-200 transition hover:bg-white/10"
            >
              <SettingsIcon className="h-4 w-4" />
              {t('profile')}
            </button>
            <button
              type="button"
              onClick={handleLogout}
              className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm text-rose-300 transition hover:bg-white/10"
            >
              <LogOut className="h-4 w-4" />
              {t('logout')}
            </button>
          </div>
        )}
      </div>
      <ProfileSettings open={prefsOpen} onClose={() => setPrefsOpen(false)} />
    </header>
  )

  return (
    <div className="relative flex h-full w-full">
      {sidebar}
      {mobileDrawer}
      <div className="flex h-full min-w-0 flex-1 flex-col">
        {topbar}
        <main className="min-h-0 flex-1 overflow-y-auto px-3 py-4 pb-20 md:px-6 md:pb-6">
          {children ?? <Outlet />}
        </main>
        {isMobile && <MobileTabBar />}
      </div>
    </div>
  )
}

function SidebarHeader({ collapsed, onToggle, brand }: { collapsed: boolean; onToggle: () => void; brand: { name: string; author: string; version: string } }) {
  const egg = useUserPrefsStore((s) => s.eggMode)
  return (
    <div className="flex items-center gap-3 px-3 py-4">
      <button
        type="button"
        onClick={onToggle}
        className="glass grid h-10 w-10 shrink-0 place-items-center rounded-xl hover:bg-white/10"
        aria-label="Toggle sidebar"
      >
        {collapsed ? <Menu className="h-5 w-5" /> : <X className="h-5 w-5" />}
      </button>
      {!collapsed && (
        <div className="flex items-center gap-2 min-w-0">
          <svg
            className={`site-logo h-9 w-9 shrink-0 ${egg ? 'logo-egg' : ''}`}
            viewBox="0 0 64 64"
            fill="none"
          >
            <defs>
              <linearGradient id="lg2" x1="0" y1="0" x2="1" y2="1">
                <stop offset="0%" stopColor="#6366f1" />
                <stop offset="100%" stopColor="#22d3ee" />
              </linearGradient>
            </defs>
            <rect x="2" y="2" width="60" height="60" rx="16" fill="url(#lg2)" opacity="0.95" />
            <path
              d="M20 38c0-6 4-10 12-10 7 0 11 4 12 8.5M20 38h24M20 38c-3 0-5-2-5-5s2-5 5-5M44 38c3 0 5-2 5-5s-2-5-5-5"
              stroke="white"
              strokeWidth="3.2"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
            <circle cx="32" cy="22" r="3.5" fill="white" />
          </svg>
          <div className="min-w-0">
            <div className="truncate text-sm font-semibold">{brand.name} · v{brand.version}</div>
            <div className="truncate text-xs text-slate-400">Cloud storage · Author: {brand.author}</div>
          </div>
        </div>
      )}
    </div>
  )
}

function MobileTabBar() {
  const nav = useNavigate()
  const loc = useLocation()
  const { t } = useTranslation()
  const tabs = [
    { key: 'home', to: '/files', Icon: Home },
    { key: 'files', to: '/files', Icon: FolderOpen },
    { key: 'shares', to: '/share-list', Icon: Share2 },
    { key: 'my', to: '/profile', Icon: User },
  ] as const
  const active = (to: string) => {
    if (to === '/profile') return loc.pathname === to
    if (to === '/files') return loc.pathname.startsWith('/f')
    return loc.pathname.startsWith(to)
  }
  return (
    <nav className="mobile-tabbar md:hidden">
      <div className="grid grid-cols-4 items-center">
        {tabs.map(({ key, to, Icon }) => (
          <button
            key={key}
            type="button"
            onClick={() => nav(to)}
            className={`flex flex-col items-center gap-0.5 py-1 text-[11px] transition ${
              active(to) ? 'text-cyan-glow' : 'text-slate-400'
            }`}
          >
            <Icon className="h-5 w-5" />
            <span>{t(key as any)}</span>
          </button>
        ))}
      </div>
    </nav>
  )
}



