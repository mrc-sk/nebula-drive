import { useEffect, useRef, useState } from 'react'
import { Globe, Check } from 'lucide-react'
import { LANGUAGES, type LangCode } from '../i18n'
import i18n from 'i18next'

export default function LangSwitcher() {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const [current, setCurrent] = useState<string>(() => {
    const saved = localStorage.getItem('nebula.lang') || localStorage.getItem('lang') || i18n.language
    const found = LANGUAGES.find((l) => saved.startsWith(l.code))
    return found ? found.code : 'zh-CN'
  })

  useEffect(() => {
    const onClick = (e: MouseEvent) => {
      if (!ref.current) return
      if (!ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [])

  const changeLang = (code: LangCode) => {
    setCurrent(code)
    i18n.changeLanguage(code)
    localStorage.setItem('nebula.lang', code)
    localStorage.setItem('lang', code)
    setOpen(false)
  }

  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="glass grid h-10 w-10 place-items-center rounded-full transition hover:scale-105 active:scale-95"
        title="Language"
        aria-label="Language switcher"
      >
        <Globe className="h-5 w-5 text-nebula-300" />
      </button>

      {open && (
        <div className="glass-strong animate-fade-in absolute right-0 z-50 mt-2 w-44 overflow-hidden rounded-xl p-1">
          {LANGUAGES.map((l) => {
            const selected = current === l.code
            return (
              <button
                key={l.code}
                type="button"
                onClick={() => changeLang(l.code)}
                className={`flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm transition ${
                  selected ? 'bg-nebula-500/30 text-white font-medium' : 'text-slate-200 hover:bg-white/10'
                }`}
              >
                <Globe className="h-4 w-4 opacity-60" />
                <span className="flex-1 text-left">{l.label}</span>
                {selected && <Check className="h-4 w-4 text-cyan-glow" />}
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}
