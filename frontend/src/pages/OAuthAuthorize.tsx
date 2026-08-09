import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { ShieldCheck, ShieldX, Loader2, AlertCircle, Check } from 'lucide-react'
import { api } from '../api/client'

function parseScopes(scope?: string | null): string[] {
  if (!scope) return []
  return scope.split(/[\s,]+/).map((s) => s.trim()).filter(Boolean)
}

export default function OAuthAuthorize() {
  const { t } = useTranslation()
  const [params] = useSearchParams()
  const clientId = params.get('client_id') || ''
  const redirectUri = params.get('redirect_uri') || ''
  const scope = params.get('scope') || ''
  const state = params.get('state') || ''
  const scopes = parseScopes(scope)

  const [busy, setBusy] = useState<'authorize' | 'deny' | null>(null)
  const [err, setErr] = useState('')

  const redirect = (url: string) => {
    if (url) window.location.href = url
  }

  const onAuthorize = async () => {
    setBusy('authorize')
    setErr('')
    try {
      const r = await api.oauth.authorize({ clientId, redirectUri, scope, state })
      if (r?.code === 0 && r.data?.code) {
        const sep = redirectUri.includes('?') ? '&' : '?'
        redirect(`${redirectUri}${sep}code=${encodeURIComponent(r.data.code)}${state ? `&state=${encodeURIComponent(state)}` : ''}`)
      } else {
        setErr(r?.message || 'error')
        setBusy(null)
      }
    } catch (e: any) {
      setErr(e?.message || 'error')
      setBusy(null)
    }
  }

  const onDeny = () => {
    setBusy('deny')
    const sep = redirectUri.includes('?') ? '&' : '?'
    redirect(`${redirectUri}${sep}error=access_denied${state ? `&state=${encodeURIComponent(state)}` : ''}`)
  }

  return (
    <div className="relative flex min-h-screen items-center justify-center p-4">
      <div className="glass-strong animate-slide-up w-full max-w-md overflow-hidden rounded-3xl p-8 shadow-glass-lg">
        <div className="mb-6 flex flex-col items-center gap-3 text-center">
          <div className="grid h-16 w-16 place-items-center rounded-2xl bg-gradient-to-br from-nebula-500/40 to-cyan-glow/30 border border-white/15 shadow-inner-glow">
            <ShieldCheck className="h-8 w-8 text-cyan-glow" />
          </div>
          <div>
            <div className="text-xl font-bold">{t('oauthAuthorize')}</div>
            <div className="mt-1 text-sm text-slate-400">{t('appPermissions')}</div>
          </div>
        </div>

        <div className="mb-5 rounded-2xl border border-white/10 bg-white/5 p-4">
          <div className="mb-1 text-xs text-slate-400">Application</div>
          <div className="truncate text-base font-semibold text-slate-100">{clientId || '—'}</div>
          {redirectUri && (
            <div className="mt-2 truncate text-[11px] text-slate-500" title={redirectUri}>
              → {redirectUri}
            </div>
          )}
        </div>

        <div className="mb-6">
          <div className="mb-2 text-xs font-medium text-slate-300">{t('appPermissions')}</div>
          {scopes.length === 0 ? (
            <div className="rounded-xl border border-white/10 bg-white/5 px-3 py-2 text-xs text-slate-400">—</div>
          ) : (
            <div className="space-y-1.5">
              {scopes.map((s) => (
                <div key={s} className="flex items-center gap-2 rounded-xl border border-white/10 bg-white/5 px-3 py-2">
                  <Check className="h-3.5 w-3.5 shrink-0 text-emerald-300" />
                  <span className="font-mono text-xs text-slate-200">{s}</span>
                </div>
              ))}
            </div>
          )}
        </div>

        {err && (
          <div className="mb-4 flex items-center gap-2 rounded-xl border border-rose-400/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
            <AlertCircle className="h-3.5 w-3.5 shrink-0" /><span>{err}</span>
          </div>
        )}

        <div className="flex gap-3">
          <button
            type="button"
            onClick={onDeny}
            disabled={!!busy}
            className="btn-ghost flex-1"
          >
            {busy === 'deny' ? <Loader2 className="h-4 w-4 animate-spin" /> : <ShieldX className="h-4 w-4" />}
            {t('deny')}
          </button>
          <button
            type="button"
            onClick={onAuthorize}
            disabled={!!busy || !clientId || !redirectUri}
            className="btn-primary flex-1"
          >
            {busy === 'authorize' ? <Loader2 className="h-4 w-4 animate-spin" /> : <ShieldCheck className="h-4 w-4" />}
            {t('authorize')}
          </button>
        </div>
      </div>
    </div>
  )
}
