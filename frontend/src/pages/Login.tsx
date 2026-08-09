import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { Loader2, Eye, EyeOff, Sparkles, Shield } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import ThemeToggle from '../components/ThemeToggle'
import LangSwitcher from '../components/LangSwitcher'
import PasswordStrength from '../components/PasswordStrength'
import { api } from '../api/client'
import { useAuthStore } from '../store/auth'
import { useUserPrefsStore } from '../store/user'
import { getBrand } from '../App'

interface BrandInfo { name: string; author: string; version: string }

export default function Login() {
  const { t } = useTranslation()
  const nav = useNavigate()
  const setAuth = useAuthStore((s) => s.setAuth)
  const set2FAHint = useUserPrefsStore((s) => s.set2FAHint)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [remember, setRemember] = useState(true)
  const [showPwd, setShowPwd] = useState(false)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(false)
  const [brand, setBrand] = useState<BrandInfo>({
    name: 'NebulaDrive',
    author: 'NebulaDrive Team',
    version: '1.0.0',
  })

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

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!username || !password) return
    setErr('')
    setLoading(true)
    try {
      const res = await api.login({ username, password, remember })
      if (res && res.code === 0 && res.data && res.data.token) {
        setAuth(res.data.user || { userName: username }, res.data.token)
        if (res.data.suggest2FAHint) set2FAHint(true)
        nav('/files', { replace: true })
      } else {
        setErr((res && res.message) || '登录失败')
      }
    } catch (e: any) {
      setErr(e?.message || '登录失败')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="relative flex h-full w-full items-center justify-center px-4">
      <div className="login-bg">
        <div className="login-blob login-blob-1" />
        <div className="login-blob login-blob-2" />
        <div className="login-blob login-blob-3" />
      </div>

      <div className="absolute right-4 top-4 z-20 flex items-center gap-2 md:right-8 md:top-8">
        <ThemeToggle />
        <LangSwitcher />
      </div>

      <form
        onSubmit={submit}
        className="glass-strong animate-slide-up relative w-full max-w-md rounded-3xl p-8"
      >
        <div className="mb-6 flex flex-col items-center gap-3 text-center">
          <div className="relative">
            <SiteLogo />
          </div>
          <div>
            <div className="text-2xl font-bold tracking-tight text-white">{t('app.name')}</div>
            <div className="mt-1 text-sm text-slate-300">{t('app.tagline')}</div>
          </div>
        </div>

        <div className="space-y-4">
          <label className="block">
            <span className="mb-1.5 block text-sm font-medium text-slate-200">{t('username')}</span>
            <input
              autoFocus
              className="glass-input"
              placeholder={t('username')}
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoComplete="username"
            />
          </label>
          <label className="block">
            <span className="mb-1.5 block text-sm font-medium text-slate-200">{t('password')}</span>
            <div className="relative">
              <input
                type={showPwd ? 'text' : 'password'}
                className="glass-input pr-11"
                placeholder={t('password')}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
              />
              <button
                type="button"
                className="absolute inset-y-0 right-0 flex w-11 items-center justify-center text-slate-300 hover:text-white"
                onClick={() => setShowPwd((v) => !v)}
                tabIndex={-1}
              >
                {showPwd ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
              </button>
            </div>
            <PasswordStrength password={password} />
          </label>

          <div className="flex items-center justify-between pt-1">
            <label className="flex cursor-pointer select-none items-center gap-2 text-sm text-slate-200">
              <input
                type="checkbox"
                className="h-4 w-4 accent-nebula-500"
                checked={remember}
                onChange={(e) => setRemember(e.target.checked)}
              />
              <span>{t('remember7days')}</span>
            </label>
            <span className="flex items-center gap-1 text-xs text-cyan-glow">
              <Shield className="h-3.5 w-3.5" />
              SSL
            </span>
          </div>

          {err && (
            <div className="glass rounded-xl border border-red-400/30 bg-red-500/10 px-3 py-2 text-sm text-red-200">
              {err}
            </div>
          )}

          <button
            type="submit"
            className="btn-primary w-full py-3 text-base"
            disabled={loading || !username || !password}
          >
            {loading ? (
              <Loader2 className="h-5 w-5 animate-spin" />
            ) : (
              <>
                <Sparkles className="h-4 w-4" />
                {t('loginBtn')}
              </>
            )}
          </button>

          <div className="pt-3 border-t border-white/10 text-center">
            <div className="text-xs text-slate-300">
              © {new Date().getFullYear()} <span className="font-semibold text-white">{brand.name}</span> · 作者 <span className="font-semibold text-white">{brand.author}</span> · v<span className="font-mono text-cyan-glow">{brand.version}</span>
            </div>
            <div className="mt-1 text-[10px] text-slate-500">
              允许商用，但必须至少保留 3 处品牌名与作者名
            </div>
          </div>
        </div>
      </form>
    </div>
  )
}

function SiteLogo() {
  const egg = useUserPrefsStore((s) => s.logoEgg)
  return (
    <svg
      className={`site-logo h-14 w-14 ${egg ? 'logo-egg' : ''}`}
      viewBox="0 0 64 64"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
    >
      <defs>
        <linearGradient id="lg" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" stopColor="#6366f1" />
          <stop offset="100%" stopColor="#22d3ee" />
        </linearGradient>
      </defs>
      <rect x="2" y="2" width="60" height="60" rx="18" fill="url(#lg)" opacity="0.95" />
      <path
        d="M20 38c0-6 4-10 12-10 7 0 11 4 12 8.5M20 38h24M20 38c-3 0-5-2-5-5s2-5 5-5M44 38c3 0 5-2 5-5s-2-5-5-5"
        stroke="white"
        strokeWidth="3.2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <circle cx="32" cy="22" r="3.5" fill="white" />
    </svg>
  )
}
