import { lazy, Suspense, useEffect, useState, useRef, useCallback } from 'react'
import { Routes, Route, Navigate, useLocation } from 'react-router-dom'
import { X, Info } from 'lucide-react'
import { api } from './api/client'
import { useAuthStore } from './store/auth'
import { useUserPrefsStore } from './store/user'

// 路由级代码分割：把各页面拆成独立 chunk，首屏（登录 / 文件浏览）不必加载
// 后台管理与 echarts 等重依赖；首屏主 chunk 体积从单包 ~3.15MB 大幅下降。
const Install = lazy(() => import('./pages/Install'))
const Login = lazy(() => import('./pages/Login'))
const Files = lazy(() => import('./pages/Files'))
const Share = lazy(() => import('./pages/Share'))
const OAuthAuthorize = lazy(() => import('./pages/OAuthAuthorize'))
const AdminLayout = lazy(() => import('./layouts/AdminLayout'))
const Dashboard = lazy(() => import('./pages/admin/Dashboard'))
const Users = lazy(() => import('./pages/admin/Users'))
const Groups = lazy(() => import('./pages/admin/Groups'))
const Policies = lazy(() => import('./pages/admin/Policies'))
const Settings = lazy(() => import('./pages/admin/Settings'))
const Tasks = lazy(() => import('./pages/admin/Tasks'))
const Plugins = lazy(() => import('./pages/admin/Plugins'))
const About = lazy(() => import('./pages/admin/About'))

export interface BrandInfo {
  name: string
  author: string
  version: string
}

const DEFAULT_BRAND: BrandInfo = {
  name: 'NebulaDrive',
  author: 'NebulaDrive Team',
  version: '1.0.0',
}

let globalBrand: BrandInfo = DEFAULT_BRAND
export const getBrand = (): BrandInfo => globalBrand

const THEME_COLORS: Record<string, string> = {
  purple: '#a855f7',
  cyan: '#22d3ee',
  green: '#10b981',
  orange: '#f97316',
  pink: '#ec4899',
  blue: '#3b82f6',
}
const THEME_ORDER = ['purple', 'cyan', 'green', 'orange', 'pink', 'blue']
const THEME_LABELS: Record<string, string> = {
  purple: '紫',
  cyan: '青',
  green: '绿',
  orange: '橙',
  pink: '粉',
  blue: '蓝',
}

export default function App() {
  const [installed, setInstalled] = useState<boolean | null>(null)
  const user = useAuthStore((s) => s.user)
  const loading = useAuthStore((s) => s.loading)
  const fetchMe = useAuthStore((s) => s.fetchMe)

  const [wheelOpen, setWheelOpen] = useState(false)
  const [wheelRotation, setWheelRotation] = useState(0)
  const [wheelSpinning, setWheelSpinning] = useState(false)
  const wheelAnimRef = useRef<number | null>(null)
  const wobbleTimerRef = useRef<number | null>(null)

  const eggMode = useUserPrefsStore((s) => s.eggMode)
  const loc = useLocation()

  const refreshInstall = async () => {
    try {
      const r = await api.installStatus()
      setInstalled(r.installed)
    } catch {
      setInstalled(false)
    }
  }

  useEffect(() => {
    refreshInstall()
  }, [])

  // 加载品牌信息
  useEffect(() => {
    let alive = true
    const load = async () => {
      try {
        const r = await (api.admin as any).settings?.()
        if (alive && r?.code === 0 && r.data) {
          const d = r.data || {}
          globalBrand = {
            name: d['brand.name'] || d.brandName || DEFAULT_BRAND.name,
            author: d['brand.author'] || d.brandAuthor || DEFAULT_BRAND.author,
            version: d['brand.version'] || d.brandVersion || DEFAULT_BRAND.version,
          }
        }
      } catch {
        globalBrand = DEFAULT_BRAND
      }
    }
    load()
    return () => { alive = false }
  }, [])

  // 已安装且本地存在 token 时，恢复登录态
  useEffect(() => {
    if (installed && localStorage.getItem('nebula_token')) {
      fetchMe()
    }
  }, [installed, fetchMe])

  // 彩蛋全局监听：imfeelinglucky + Konami
  const bindEggListeners = useCallback(() => {
    let keyBuffer = ''
    const konamiTarget = 'ArrowUpArrowUpArrowDownArrowDownArrowLeftArrowRightArrowLeftArrowRightba'
    const konamiSeq: number[] = [38, 38, 40, 40, 37, 39, 37, 39, 66, 65]
    let konamiPos = 0

    const onKeyDown = (e: KeyboardEvent) => {
      const state = useUserPrefsStore.getState()
      if (!state.eggMode) {
        konamiPos = 0
        keyBuffer = ''
        return
      }

      // imfeelinglucky
      try {
        const ch = (e.key || '').toLowerCase()
        if (ch.length === 1) {
          keyBuffer = (keyBuffer + ch).slice(-32)
          if (keyBuffer.includes('imfeelinglucky')) {
            keyBuffer = ''
            setWheelOpen(true)
          }
        }
      } catch {}

      // Konami
      try {
        const code = e.keyCode || e.which
        if (konamiSeq[konamiPos] === code) {
          konamiPos++
          if (konamiPos >= konamiSeq.length) {
            konamiPos = 0
            triggerWobble()
          }
        } else if (code === konamiSeq[0]) {
          konamiPos = 1
        } else {
          konamiPos = 0
        }
      } catch {}
    }

    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [])

  const triggerWobble = useCallback(() => {
    document.body.classList.add('text-wobble')
    if (wobbleTimerRef.current) {
      window.clearTimeout(wobbleTimerRef.current)
    }
    wobbleTimerRef.current = window.setTimeout(() => {
      document.body.classList.remove('text-wobble')
      wobbleTimerRef.current = null
    }, 10 * 1000)
  }, [])

  // 路由切换 + eggMode 变化时重绑
  useEffect(() => {
    const unbind = bindEggListeners()
    return unbind
  }, [loc.pathname, eggMode, bindEggListeners])

  useEffect(() => {
    return () => {
      if (wobbleTimerRef.current) window.clearTimeout(wobbleTimerRef.current)
      if (wheelAnimRef.current) cancelAnimationFrame(wheelAnimRef.current)
    }
  }, [])

  const spinWheel = useCallback(() => {
    if (wheelSpinning) return
    setWheelSpinning(true)
    const sectorIdx = Math.floor(Math.random() * 6)
    const sectorCenter = sectorIdx * 60 + 30
    const baseTurns = 5 + Math.floor(Math.random() * 4)
    const targetAngle = -sectorCenter - baseTurns * 360
    const startAngle = wheelRotation
    const duration = 4000 + Math.random() * 4000
    const startTs = performance.now()

    const easeOutCubic = (t: number) => 1 - Math.pow(1 - t, 3)
    const tick = (now: number) => {
      const elapsed = now - startTs
      const t = Math.min(1, elapsed / duration)
      const eased = easeOutCubic(t)
      const current = startAngle + (targetAngle - startAngle) * eased
      setWheelRotation(current)
      if (t < 1) {
        wheelAnimRef.current = requestAnimationFrame(tick)
      } else {
        wheelAnimRef.current = null
        const colorKey = THEME_ORDER[sectorIdx]
        const color = THEME_COLORS[colorKey]
        document.documentElement.style.setProperty('--theme-primary', color)
        document.documentElement.style.setProperty('--nebula-primary', color)
        try {
          const sheet = document.styleSheets[document.styleSheets.length - 1]
          if (sheet && (sheet as any).insertRule) {
            try { (sheet as CSSStyleSheet).insertRule(`.btn-primary { background: linear-gradient(135deg, ${color} 0%, #22d3ee 100%) !important; }`, (sheet as CSSStyleSheet).cssRules.length) } catch {}
            try { (sheet as CSSStyleSheet).insertRule(`.vtab-active { background: linear-gradient(90deg, ${color}40, rgba(34,211,238,0.12)) !important; border-left-color: ${color} !important; }`, (sheet as CSSStyleSheet).cssRules.length) } catch {}
          }
        } catch {}
        setWheelRotation(targetAngle)
        setWheelSpinning(false)
      }
    }
    wheelAnimRef.current = requestAnimationFrame(tick)
  }, [wheelSpinning, wheelRotation])

  const authBusy = !!installed && loading

  return (
    <>
      <AcrylicBackground />
      <Suspense fallback={<FullScreenLoading />}>
        <Routes>
        <Route
          path="/install"
          element={
            installed === null ? (
              <FullScreenLoading />
            ) : installed ? (
              <Navigate to="/" replace />
            ) : (
              <Install onDone={refreshInstall} />
            )
          }
        />
        <Route
          path="/login"
          element={
            installed === null ? (
              <FullScreenLoading />
            ) : !installed ? (
              <Navigate to="/install" replace />
            ) : authBusy ? (
              <FullScreenLoading />
            ) : user ? (
              <Navigate to="/files" replace />
            ) : (
              <Login />
            )
          }
        />
        <Route
          path="/files"
          element={
            installed === null ? (
              <FullScreenLoading />
            ) : !installed ? (
              <Navigate to="/install" replace />
            ) : authBusy ? (
              <FullScreenLoading />
            ) : user ? (
              <Files />
            ) : (
              <Navigate to="/login" replace />
            )
          }
        />
        <Route
          path="/share/:id"
          element={
            installed === null ? <FullScreenLoading /> : !installed ? <Navigate to="/install" replace /> : <Share />
          }
        />
        <Route
          path="/oauth/authorize"
          element={
            installed === null ? <FullScreenLoading /> : !installed ? <Navigate to="/install" replace /> : <OAuthAuthorize />
          }
        />
        <Route
          path="/admin/*"
          element={
            installed === null ? (
              <FullScreenLoading />
            ) : !installed ? (
              <Navigate to="/install" replace />
            ) : authBusy ? (
              <FullScreenLoading />
            ) : !user ? (
              <Navigate to="/login" replace />
            ) : !user.isAdmin ? (
              <Navigate to="/files" replace />
            ) : (
              <AdminLayout />
            )
          }
        >
          <Route index element={<Navigate to="/admin/dashboard" replace />} />
          <Route path="dashboard" element={<Dashboard />} />
          <Route path="users" element={<Users />} />
          <Route path="groups" element={<Groups />} />
          <Route path="policies" element={<Policies />} />
          <Route path="settings" element={<Settings />} />
          <Route path="tasks" element={<Tasks />} />
          <Route path="plugins" element={<Plugins />} />
          <Route path="about" element={<About />} />
          <Route path="*" element={<Navigate to="/admin/dashboard" replace />} />
        </Route>
        <Route
          path="/"
          element={
            installed === null || authBusy ? (
              <FullScreenLoading />
            ) : !installed ? (
              <Navigate to="/install" replace />
            ) : !user ? (
              <Navigate to="/login" replace />
            ) : (
              <Navigate to="/files" replace />
            )
          }
        />
        <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </Suspense>

      {wheelOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4 backdrop-blur-md animate-fade-in">
          <div className="glass-strong animate-slide-up relative w-full max-w-md overflow-hidden rounded-3xl p-6 shadow-glass-lg">
            <button
              type="button"
              onClick={() => { if (!wheelSpinning) setWheelOpen(false) }}
              className="absolute right-3 top-3 grid h-8 w-8 place-items-center rounded-xl text-slate-400 hover:bg-white/10 hover:text-slate-100"
            >
              <X className="h-4 w-4" />
            </button>
            <div className="mb-4 flex flex-col items-center gap-1 text-center">
              <div className="text-lg font-bold text-white">🎉 I'm Feeling Lucky!</div>
              <div className="text-xs text-slate-400">点击 START 随机切换主题色</div>
            </div>
            <div className="relative mx-auto" style={{ width: 320, height: 340 }}>
              <div className="wheel-pointer" />
              <div
                className="wheel-outer absolute left-0 top-5"
                style={{ transform: `rotate(${wheelRotation}deg)` }}
              >
                {THEME_ORDER.map((k, i) => {
                  const angle = i * 60 + 30
                  const rad = (angle * Math.PI) / 180
                  const r = 118
                  const tx = Math.sin(rad) * r
                  const ty = -Math.cos(rad) * r
                  return (
                    <span
                      key={k}
                      className="wheel-sector-label"
                      style={{ transform: `translate(${tx}px, ${ty}px) rotate(${angle}deg) translate(-50%, -50%)` }}
                    >
                      {THEME_LABELS[k]}
                    </span>
                  )
                })}
              </div>
              <button
                type="button"
                className="wheel-start-btn"
                disabled={wheelSpinning}
                onClick={spinWheel}
              >
                START
              </button>
            </div>
            <div className="mt-5 text-center text-[11px] text-slate-400">
              <Info className="inline h-3.5 w-3.5 mr-1 align-text-bottom" />
              6 种主题色：紫 · 青 · 绿 · 橙 · 粉 · 蓝
            </div>
          </div>
        </div>
      )}
    </>
  )
}

function AcrylicBackground() {
  return (
    <>
      <div className="acrylic-bg" />
      <div
        className="acrylic-blob animate-blob-slow"
        style={{ width: 420, height: 420, top: '-8%', left: '8%', background: '#6366f1' }}
      />
      <div
        className="acrylic-blob animate-blob-slower"
        style={{ width: 360, height: 360, bottom: '-6%', right: '10%', background: '#22d3ee' }}
      />
      <div
        className="acrylic-blob animate-blob-slow"
        style={{ width: 300, height: 300, top: '40%', right: '30%', background: '#a855f7' }}
      />
    </>
  )
}

function FullScreenLoading() {
  return (
    <div className="flex h-full items-center justify-center">
      <dot-motion-loader style={{ width: '100px' }} />
    </div>
  )
}
