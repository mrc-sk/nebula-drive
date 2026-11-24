import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { Loader2, Eye, EyeOff, Sparkles, Shield, KeyRound, UserPlus, LogIn } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import ThemeToggle from '../components/ThemeToggle'
import LangSwitcher from '../components/LangSwitcher'
import PasswordStrength from '../components/PasswordStrength'
import { api } from '../api/client'
import { useAuthStore } from '../store/auth'
import { useUserPrefsStore } from '../store/user'
import { getBrand } from '../App'

interface BrandInfo { name: string; author: string; version: string }

interface RegMode {
  allowRegister: boolean
  requireInviteCode: boolean
  plans: { id: number; name: string; displayName: string; durationMonths: number; maxStorage: number }[]
}

export default function Login() {
  const { t } = useTranslation()
  const nav = useNavigate()
  const setAuth = useAuthStore((s) => s.setAuth)
  const set2FAHint = useUserPrefsStore((s) => s.set2FAHint)
  const [tab, setTab] = useState<'login' | 'register'>('login')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [remember, setRemember] = useState(true)
  const [showPwd, setShowPwd] = useState(false)
  const [err, setErr] = useState('')
  const [notice, setNotice] = useState('')
  const [loading, setLoading] = useState(false)
  // 注册表单
  const [regEmail, setRegEmail] = useState('')
  const [inviteCode, setInviteCode] = useState('')
  const [regMode, setRegMode] = useState<RegMode | null>(null)
  const [brand, setBrand] = useState<BrandInfo>({
    name: 'NebulaDrive',
    author: 'NebulaDrive Team',
    version: '26-11.4',
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
            version: d['brand.version'] || d.brandVersion || '26-11.4',
          })
        }
      } catch {}
    }
    load()
    // 拉取注册策略：自由注册开启或强制邀请码时，显示「注册」入口；
    // 即使自由注册关闭，只要有邀请码也能注册，所以注册入口始终保留。
    api.registerMode()
      .then((r) => { if (alive && r?.code === 0 && r.data) setRegMode(r.data) })
      .catch(() => {})
    return () => { alive = false }
  }, [])

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!username || !password) return
    if (tab === 'register' && inviteCode.trim() === '' && regMode?.requireInviteCode) {
      setErr('请填写邀请码'); return
    }
    setErr(''); setNotice('')
    setLoading(true)
    try {
      if (tab === 'register') {
        const r = await api.register({
          userName: username, password, email: regEmail.trim(), inviteCode: inviteCode.trim(),
        })
        if (r && r.code === 0) {
          // 注册成功，自动登录
          const lr = await api.login({ username, password, remember })
          if (lr && lr.code === 0 && lr.data && lr.data.token) {
            setAuth(lr.data.user || { userName: username }, lr.data.token)
            nav('/files', { replace: true })
          } else {
            setNotice('注册成功，请登录')
            setTab('login')
          }
        } else {
          setErr((r && r.message) || '注册失败')
        }
      } else {
        const res = await api.login({ username, password, remember })
        if (res && res.code === 0 && res.data && res.data.token) {
          setAuth(res.data.user || { userName: username }, res.data.token)
          if (res.data.suggest2FAHint) set2FAHint(true)
          nav('/files', { replace: true })
        } else {
          setErr((res && res.message) || '登录失败')
        }
      }
    } catch (e: any) {
      setErr(e?.message || (tab === 'register' ? '注册失败' : '登录失败'))
    } finally {
      setLoading(false)
    }
  }

  const switchTab = (next: 'login' | 'register') => {
    setTab(next); setErr(''); setNotice('')
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

        {/* 登录 / 注册 切换 */}
        <div className="mb-5 grid grid-cols-2 gap-1 rounded-xl border border-white/10 bg-white/5 p-1">
          <button
            type="button"
            onClick={() => switchTab('login')}
            className={`flex items-center justify-center gap-1.5 rounded-lg py-2 text-sm font-medium transition ${
              tab === 'login' ? 'bg-white/15 text-white shadow' : 'text-slate-300 hover:text-white'
            }`}
          >
            <LogIn className="h-4 w-4" /> {t('login')}
          </button>
          <button
            type="button"
            onClick={() => switchTab('register')}
            className={`flex items-center justify-center gap-1.5 rounded-lg py-2 text-sm font-medium transition ${
              tab === 'register' ? 'bg-white/15 text-white shadow' : 'text-slate-300 hover:text-white'
            }`}
          >
            <UserPlus className="h-4 w-4" /> {t('register')}
          </button>
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

          {tab === 'register' && (
            <label className="block">
              <span className="mb-1.5 block text-sm font-medium text-slate-200">邮箱（选填）</span>
              <input
                type="email"
                className="glass-input"
                placeholder="name@example.com"
                value={regEmail}
                onChange={(e) => setRegEmail(e.target.value)}
                autoComplete="email"
              />
            </label>
          )}

          <label className="block">
            <span className="mb-1.5 block text-sm font-medium text-slate-200">{t('password')}</span>
            <div className="relative">
              <input
                type={showPwd ? 'text' : 'password'}
                className="glass-input pr-11"
                placeholder={t('password')}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete={tab === 'register' ? 'new-password' : 'current-password'}
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

          {tab === 'register' && (
            <label className="block">
              <span className="mb-1.5 flex items-center gap-1.5 text-sm font-medium text-slate-200">
                <KeyRound className="h-3.5 w-3.5 text-cyan-glow" />
                邀请码
                {regMode?.requireInviteCode && <span className="text-red-300">（必填）</span>}
                {!regMode?.requireInviteCode && <span className="text-xs text-slate-400">（选填，有码即可注册）</span>}
              </span>
              <input
                className="glass-input font-mono tracking-wider"
                placeholder="NB-XXXX-XXXX-XXXX"
                value={inviteCode}
                onChange={(e) => setInviteCode(e.target.value.toUpperCase())}
                autoCapitalize="characters"
                spellCheck={false}
              />
            </label>
          )}

          {tab === 'login' && (
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
          )}

          {err && (
            <div className="glass rounded-xl border border-red-400/30 bg-red-500/10 px-3 py-2 text-sm text-red-200">
              {err}
            </div>
          )}
          {notice && (
            <div className="glass rounded-xl border border-emerald-400/30 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-200">
              {notice}
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
                {tab === 'register' ? t('register') : t('loginBtn')}
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
        d="M20 38c0-6 4-10 12-10 7 0 11 4 12 8.5M20 38h24M20 38c-3 0-5-2-5-5s2-5 5-5M44 38c3 0 5-2 5-5s2-5 5-5"
        stroke="white"
        strokeWidth="3.2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <circle cx="32" cy="22" r="3.5" fill="white" />
    </svg>
  )
}
