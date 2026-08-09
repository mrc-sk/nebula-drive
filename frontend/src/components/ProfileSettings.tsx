import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  X, KeyRound, Plus, Loader2, AlertCircle, Check, Copy, Trash2, Shield, SlidersHorizontal, Lock,
} from 'lucide-react'
import { LANGUAGES } from '../i18n'
import { useThemeStore } from '../store/theme'
import { useUserPrefsStore } from '../store/user'
import { api } from '../api/client'
import PasswordStrength from './PasswordStrength'

const SCOPE_OPTIONS = [
  { key: 'files:read', desc: 'files:read' },
  { key: 'files:write', desc: 'files:write' },
  { key: 'shares:read', desc: 'shares:read' },
  { key: 'shares:write', desc: 'shares:write' },
  { key: 'admin', desc: 'admin' },
]

const EXPIRY_OPTIONS: { value: number; labelKey: string; default?: string }[] = [
  { value: 7, labelKey: 'tokenExpiry', default: '7' },
  { value: 30, labelKey: 'tokenExpiry', default: '30' },
  { value: 90, labelKey: 'tokenExpiry', default: '90' },
  { value: 0, labelKey: 'neverExpires' },
]

interface PatItem {
  id: number | string
  name: string
  prefix?: string
  scopes?: string[]
  createdAt?: string
  lastUsedAt?: string
  expiresAt?: string | null
}

export default function ProfileSettings({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t, i18n } = useTranslation()
  const theme = useThemeStore((s) => s.theme)
  const setTheme = useThemeStore((s) => s.setTheme)
  const { eggMode, setEggMode, selectMode, setSelectMode } = useUserPrefsStore()
  const [lang, setLang] = useState(i18n.language.slice(0, 5))
  const [tab, setTab] = useState<'prefs' | 'tokens' | 'security'>('prefs')

  // 修改密码
  const [oldPwd, setOldPwd] = useState('')
  const [newPwd, setNewPwd] = useState('')
  const [pwdBusy, setPwdBusy] = useState(false)
  const [pwdMsg, setPwdMsg] = useState<{ type: 'ok' | 'err'; text: string } | null>(null)

  // PAT state
  const [pats, setPats] = useState<PatItem[]>([])
  const [loadingTokens, setLoadingTokens] = useState(false)
  const [newName, setNewName] = useState('')
  const [expiry, setExpiry] = useState<number>(30)
  const [scopes, setScopes] = useState<string[]>(['files:read', 'files:write'])
  const [creating, setCreating] = useState(false)
  const [tokenErr, setTokenErr] = useState('')
  const [createdToken, setCreatedToken] = useState<{ token: string } | null>(null)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!open) return
    if (tab === 'tokens') loadPats()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, tab])

  if (!open) return null

  const applyLang = (c: string) => {
    setLang(c)
    i18n.changeLanguage(c)
    localStorage.setItem('nebula.lang', c)
    localStorage.setItem('lang', c)
  }

  const loadPats = async () => {
    setLoadingTokens(true)
    try {
      const r = await api.tokens.list()
      if (r?.code === 0 && r.data) setPats(Array.isArray(r.data) ? r.data : [])
    } catch { /* ignore */ } finally {
      setLoadingTokens(false)
    }
  }

  const toggleScope = (s: string) => {
    setScopes((prev) => (prev.includes(s) ? prev.filter((x) => x !== s) : [...prev, s]))
  }

  const createToken = async () => {
    if (!newName.trim()) { setTokenErr(t('tokenName')); return }
    setCreating(true)
    setTokenErr('')
    try {
      const r = await api.tokens.create({
        name: newName.trim(),
        expiresInDays: expiry === 0 ? undefined : expiry,
        scopes,
      })
      if (r?.code === 0 && r.data) {
        setCreatedToken({ token: (r.data as any).token || '' })
        setNewName('')
        loadPats()
      } else {
        setTokenErr(r?.message || 'error')
      }
    } catch (e: any) {
      setTokenErr(e?.message || 'error')
    } finally {
      setCreating(false)
    }
  }

  const revoke = async (id: number | string) => {
    try {
      await api.tokens.revoke(id)
      setPats((prev) => prev.filter((x) => x.id !== id))
    } catch { /* ignore */ }
  }

  const copyToken = () => {
    if (!createdToken) return
    navigator.clipboard?.writeText(createdToken.token)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  const changePassword = async () => {
    setPwdMsg(null)
    if (!oldPwd || !newPwd) {
      setPwdMsg({ type: 'err', text: t('oldPassword') + ' / ' + t('newPassword') })
      return
    }
    if (newPwd.length < 8) {
      setPwdMsg({ type: 'err', text: t('minLength') })
      return
    }
    setPwdBusy(true)
    try {
      const r = await (api as any).changePassword?.(oldPwd, newPwd)
      if (r?.code === 0) {
        setPwdMsg({ type: 'ok', text: t('changePassword') + ' OK' })
        setOldPwd('')
        setNewPwd('')
      } else {
        setPwdMsg({ type: 'err', text: r?.message || 'error' })
      }
    } catch (e: any) {
      setPwdMsg({ type: 'err', text: e?.message || 'error' })
    } finally {
      setPwdBusy(false)
    }
  }

  return (
    <div className="glass-strong kebab-pop absolute right-0 top-14 z-40 w-96 max-w-[92vw] overflow-hidden rounded-2xl shadow-glass-lg">
      <div className="mb-2 flex items-center justify-between border-b border-white/10 px-4 py-3">
        <div className="text-sm font-semibold">{t('profile')}</div>
        <button
          type="button"
          onClick={onClose}
          className="glass grid h-7 w-7 place-items-center rounded-lg hover:bg-white/10"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      <div className="flex gap-1 border-b border-white/10 px-3 py-2">
        <button
          type="button"
          onClick={() => setTab('prefs')}
          className={`flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs transition ${tab === 'prefs' ? 'bg-nebula-500/30 text-white' : 'text-slate-300 hover:bg-white/10'}`}
        >
          <SlidersHorizontal className="h-3.5 w-3.5" />
          {t('profile')}
        </button>
        <button
          type="button"
          onClick={() => setTab('tokens')}
          className={`flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs transition ${tab === 'tokens' ? 'bg-nebula-500/30 text-white' : 'text-slate-300 hover:bg-white/10'}`}
        >
          <KeyRound className="h-3.5 w-3.5" />
          {t('accessToken')}
        </button>
        <button
          type="button"
          onClick={() => setTab('security')}
          className={`flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs transition ${tab === 'security' ? 'bg-nebula-500/30 text-white' : 'text-slate-300 hover:bg-white/10'}`}
        >
          <Lock className="h-3.5 w-3.5" />
          {t('changePassword')}
        </button>
      </div>

      <div className="max-h-[70vh] overflow-y-auto p-4">
        {tab === 'prefs' && (
          <div className="space-y-4">
            <Field label={t('language')}>
              <div className="grid grid-cols-2 gap-2">
                {LANGUAGES.map((l) => (
                  <button
                    key={l.code}
                    type="button"
                    onClick={() => applyLang(l.code)}
                    className={`rounded-lg px-3 py-1.5 text-xs transition ${lang.startsWith(l.code.slice(0, 2)) || l.code === lang ? 'bg-nebula-500/30 text-white' : 'bg-white/5 hover:bg-white/10 text-slate-200'}`}
                  >
                    {l.label}
                  </button>
                ))}
              </div>
            </Field>
            <Field label={t('theme')}>
              <div className="grid grid-cols-3 gap-2">
                {(['light', 'dark', 'auto'] as const).map((m) => (
                  <button
                    key={m}
                    type="button"
                    onClick={() => setTheme(m)}
                    className={`rounded-lg px-2 py-1.5 text-xs transition ${theme === m ? 'bg-nebula-500/30 text-white' : 'bg-white/5 hover:bg-white/10 text-slate-200'}`}
                  >
                    {t(m === 'light' ? 'themeLight' : m === 'dark' ? 'themeDark' : 'themeAuto')}
                  </button>
                ))}
              </div>
            </Field>
            <Field label={t('selectMode')}>
              <select
                value={selectMode}
                onChange={(e) => setSelectMode(e.target.value as any)}
                className="glass-input text-xs"
              >
                <option value="context">{t('selectModeContext')}</option>
                <option value="floating">{t('selectModeFloating')}</option>
                <option value="drawer">{t('selectModeDrawer')}</option>
              </select>
            </Field>
            <label className="flex cursor-pointer items-center justify-between rounded-lg bg-white/5 px-3 py-2">
              <span className="text-xs text-slate-200">彩蛋模式</span>
              <input type="checkbox" className="h-4 w-4 accent-nebula-500" checked={eggMode} onChange={(e) => setEggMode(e.target.checked)} />
            </label>
          </div>
        )}

        {tab === 'tokens' && (
          <div className="space-y-4">
            {/* 创建表单 */}
            <div className="rounded-xl border border-white/10 bg-white/5 p-3">
              <div className="mb-2 flex items-center gap-2 text-xs font-semibold text-slate-200">
                <Plus className="h-3.5 w-3.5 text-cyan-glow" />
                {t('createToken')}
              </div>
              <input
                className="glass-input mb-2 text-xs"
                placeholder={t('tokenName')}
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
              />
              <div className="mb-2">
                <div className="mb-1 text-[11px] text-slate-400">{t('tokenExpiry')}</div>
                <div className="grid grid-cols-4 gap-1.5">
                  {EXPIRY_OPTIONS.map((o) => (
                    <button
                      key={o.value}
                      type="button"
                      onClick={() => setExpiry(o.value)}
                      className={`rounded-lg px-2 py-1.5 text-xs transition ${expiry === o.value ? 'bg-nebula-500/30 text-white' : 'bg-white/5 hover:bg-white/10 text-slate-200'}`}
                    >
                      {o.value === 0 ? t('neverExpires') : `${o.value}d`}
                    </button>
                  ))}
                </div>
              </div>
              <div className="mb-2">
                <div className="mb-1 text-[11px] text-slate-400">{t('appPermissions')}</div>
                <div className="flex flex-wrap gap-1.5">
                  {SCOPE_OPTIONS.map((s) => {
                    const on = scopes.includes(s.key)
                    return (
                      <button
                        key={s.key}
                        type="button"
                        onClick={() => toggleScope(s.key)}
                        className={`rounded-full px-2.5 py-1 text-[11px] font-mono transition ${on ? 'bg-cyan-glow/25 text-cyan-glow border border-cyan-glow/40' : 'bg-white/5 text-slate-300 border border-white/10 hover:bg-white/10'}`}
                      >
                        {s.desc}
                      </button>
                    )
                  })}
                </div>
              </div>
              {tokenErr && (
                <div className="mb-2 flex items-center gap-1.5 rounded-lg border border-rose-400/30 bg-rose-500/10 px-2.5 py-1.5 text-[11px] text-rose-300">
                  <AlertCircle className="h-3 w-3" /><span>{tokenErr}</span>
                </div>
              )}
              <button type="button" onClick={createToken} disabled={creating} className="btn-primary w-full py-2 text-xs">
                {creating ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plus className="h-3.5 w-3.5" />}
                {t('createToken')}
              </button>
            </div>

            {/* 列表 */}
            <div className="space-y-2">
              {loadingTokens ? (
                <div className="grid place-items-center py-6"><Loader2 className="h-5 w-5 animate-spin text-nebula-300" /></div>
              ) : pats.length === 0 ? (
                <div className="py-6 text-center text-xs text-slate-400">—</div>
              ) : (
                pats.map((p) => (
                  <div key={p.id} className="rounded-xl border border-white/10 bg-white/5 p-3">
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate text-sm font-medium text-slate-100">{p.name}</span>
                      <button
                        type="button"
                        onClick={() => revoke(p.id)}
                        className="flex shrink-0 items-center gap-1 rounded-lg px-2 py-1 text-[11px] text-rose-300 transition hover:bg-rose-500/15"
                        title={t('revokeToken')}
                      >
                        <Trash2 className="h-3 w-3" />{t('revokeToken')}
                      </button>
                    </div>
                    <div className="mt-1 font-mono text-[11px] text-cyan-glow">{p.prefix || '••••'}…</div>
                    <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-[10px] text-slate-500">
                      {p.createdAt && <span>created: {fmtDate(p.createdAt)}</span>}
                      {p.lastUsedAt && <span>last used: {fmtDate(p.lastUsedAt)}</span>}
                      <span>{p.expiresAt ? `${t('tokenExpiry')}: ${fmtDate(p.expiresAt)}` : t('neverExpires')}</span>
                    </div>
                  </div>
                ))
              )}
            </div>
          </div>
        )}

        {tab === 'security' && (
          <div className="space-y-4">
            <div className="rounded-xl border border-white/10 bg-white/5 p-3">
              <div className="mb-2 flex items-center gap-2 text-xs font-semibold text-slate-200">
                <Lock className="h-3.5 w-3.5 text-cyan-glow" />
                {t('changePassword')}
              </div>
              <label className="block">
                <span className="mb-1 block text-[11px] text-slate-400">{t('oldPassword')}</span>
                <input
                  type="password"
                  className="glass-input text-xs"
                  value={oldPwd}
                  onChange={(e) => setOldPwd(e.target.value)}
                  autoComplete="current-password"
                />
              </label>
              <label className="mt-2 block">
                <span className="mb-1 block text-[11px] text-slate-400">{t('newPassword')}</span>
                <input
                  type="password"
                  className="glass-input text-xs"
                  value={newPwd}
                  onChange={(e) => setNewPwd(e.target.value)}
                  autoComplete="new-password"
                />
              </label>
              <div className="mt-2">
                <PasswordStrength password={newPwd} />
              </div>
              {pwdMsg && (
                <div
                  className={`mt-2 flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-[11px] ${
                    pwdMsg.type === 'ok'
                      ? 'border border-emerald-400/30 bg-emerald-500/10 text-emerald-300'
                      : 'border border-rose-400/30 bg-rose-500/10 text-rose-300'
                  }`}
                >
                  {pwdMsg.type === 'ok' ? <Check className="h-3 w-3" /> : <AlertCircle className="h-3 w-3" />}
                  <span>{pwdMsg.text}</span>
                </div>
              )}
              <button
                type="button"
                onClick={changePassword}
                disabled={pwdBusy}
                className="btn-primary mt-3 w-full py-2 text-xs"
              >
                {pwdBusy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Lock className="h-3.5 w-3.5" />}
                {t('changePassword')}
              </button>
            </div>
          </div>
        )}
      </div>

      {/* 创建成功 token 弹窗 */}
      {createdToken && (
        <div className="fixed inset-0 z-[70] flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm animate-fade-in" onClick={() => { setCreatedToken(null); setCopied(false) }}>
          <div className="glass-strong animate-slide-up w-full max-w-md rounded-2xl p-5" onClick={(e) => e.stopPropagation()}>
            <div className="mb-3 flex items-center gap-2 text-sm font-semibold">
              <Shield className="h-4 w-4 text-emerald-300" />
              {t('accessToken')}
            </div>
            <div className="mb-3 flex items-start gap-2 rounded-xl border border-amber-400/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-200">
              <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>{t('tokenOnlyOnce')}</span>
            </div>
            <div className="flex gap-2">
              <input className="glass-input font-mono text-xs" value={createdToken.token} readOnly onFocus={(e) => e.target.select()} />
              <button type="button" onClick={copyToken} className="btn-ghost shrink-0">
                {copied ? <Check className="h-4 w-4 text-emerald-300" /> : <Copy className="h-4 w-4" />}
                {t('copyToken')}
              </button>
            </div>
            {copied && <div className="mt-2 text-[11px] text-emerald-300">{t('tokenCopied')}</div>}
            <div className="mt-4 flex justify-end">
              <button type="button" className="btn-primary" onClick={() => { setCreatedToken(null); setCopied(false) }}>{t('ok')}</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function fmtDate(s?: string): string {
  if (!s) return '-'
  const d = new Date(s)
  if (isNaN(d.getTime())) return s
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const da = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${da}`
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-1.5 text-xs font-medium text-slate-300">{label}</div>
      {children}
    </div>
  )
}
