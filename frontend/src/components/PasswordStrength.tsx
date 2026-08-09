import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, X } from 'lucide-react'

export interface PasswordChecks {
  length: boolean
  upper: boolean
  digit: boolean
  special: boolean
}

export function evaluatePassword(pwd: string): { score: 0 | 1 | 2 | 3; checks: PasswordChecks } {
  const checks: PasswordChecks = {
    length: pwd.length >= 8,
    upper: /[A-Z]/.test(pwd),
    digit: /[0-9]/.test(pwd),
    special: /[^A-Za-z0-9]/.test(pwd),
  }
  const passed = [checks.length, checks.upper, checks.digit, checks.special].filter(Boolean).length
  let score: 0 | 1 | 2 | 3 = 0
  if (pwd.length === 0) score = 0
  else if (passed <= 1) score = 1
  else if (passed <= 2) score = 2
  else if (passed <= 3) score = 2
  else score = 3
  return { score, checks }
}

export default function PasswordStrength({ password }: { password: string }) {
  const { t } = useTranslation()
  const { score, checks } = useMemo(() => evaluatePassword(password), [password])
  if (!password) return null

  const labels = [t('weak'), t('medium'), t('strong')]
  const colors = ['#ef4444', '#f59e0b', '#10b981']
  const level = score >= 1 ? score - 1 : 0
  const label = labels[level]
  const color = colors[level]

  const items: { key: keyof PasswordChecks; label: string }[] = [
    { key: 'length', label: t('minLength') },
    { key: 'upper', label: t('requireUpper') },
    { key: 'digit', label: t('requireDigit') },
    { key: 'special', label: t('requireSpecial') },
  ]

  return (
    <div className="mt-2 space-y-2">
      <div className="flex items-center gap-2">
        <div className="flex h-1.5 flex-1 overflow-hidden rounded-full bg-white/10">
          {[0, 1, 2].map((i) => (
            <div
              key={i}
              className="h-full flex-1 transition-all"
              style={{
                backgroundColor: i < score ? color : 'rgba(255,255,255,0.12)',
                marginRight: i < 2 ? '3px' : 0,
              }}
            />
          ))}
        </div>
        <span className="shrink-0 text-[11px] font-medium" style={{ color }}>
          {t('passwordStrength')}: {label}
        </span>
      </div>
      <div className="flex flex-wrap gap-x-3 gap-y-1">
        {items.map((it) => {
          const ok = checks[it.key]
          return (
            <span
              key={it.key}
              className={`inline-flex items-center gap-1 text-[11px] ${ok ? 'text-emerald-300' : 'text-slate-400'}`}
            >
              {ok ? <Check className="h-3 w-3" /> : <X className="h-3 w-3" />}
              {it.label}
            </span>
          )
        })}
      </div>
    </div>
  )
}
