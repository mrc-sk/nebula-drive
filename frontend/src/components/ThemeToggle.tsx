import { useEffect, useRef, useState } from 'react'
import { Sun, Moon, Monitor, Check } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useThemeStore, type Theme } from '../store/theme'

export default function ThemeToggle() {
  const { t } = useTranslation()
  const theme = useThemeStore((s) => s.theme)
  const resolved = useThemeStore((s) => s.resolvedTheme)
  const setTheme = useThemeStore((s) => s.setTheme)
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const onClick = (e: MouseEvent) => {
      if (!ref.current) return
      if (!ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [])

  const items: { key: Theme; label: string; Icon: any }[] = [
    { key: 'light', label: t('themeLight'), Icon: Sun },
    { key: 'dark', label: t('themeDark'), Icon: Moon },
    { key: 'auto', label: t('themeAuto'), Icon: Monitor },
  ]

  const active = resolved === 'dark' ? Moon : Sun

  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="glass grid h-10 w-10 place-items-center rounded-full transition hover:scale-105 active:scale-95"
        title={t('theme')}
        aria-label={t('ns_theme.toggle')}
      >
        {active === Moon ? (
          <Sun className="h-5 w-5 text-amber-300" />
        ) : (
          <Moon className="h-5 w-5 text-nebula-500" />
        )}
      </button>

      {open && (
        <div className="glass-strong animate-fade-in absolute right-0 z-50 mt-2 w-40 overflow-hidden rounded-xl p-1">
          {items.map(({ key, label, Icon }) => {
            const selected = theme === key
            return (
              <button
                key={key}
                type="button"
                onClick={() => {
                  setTheme(key)
                  setOpen(false)
                }}
                className={`flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm transition ${
                  selected ? 'bg-nebula-500/30 text-white' : 'text-slate-200 hover:bg-white/10'
                }`}
              >
                <Icon className="h-4 w-4" />
                <span className="flex-1 text-left">{label}</span>
                {selected && <Check className="h-4 w-4 text-cyan-glow" />}
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}
