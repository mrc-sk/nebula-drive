import { useEffect, useRef, useState } from 'react'
import { Palette, Check, RotateCcw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { usePluginUIStore } from '../store/pluginUI'

/**
 * 插件主题选择器。
 *
 * 宿主内置的是 dark/light/auto 三档（ThemeToggle），插件主题走另一条路：
 * 覆盖 CSS 变量 + 可选加载一张插件自带样式表。两者可以同时生效。
 * 只有在至少有一个插件声明了 theme 时才显示这个按钮，不打扰默认用户。
 */
export default function PluginThemePicker() {
  const { t } = useTranslation()
  const data = usePluginUIStore((s) => s.data)
  const active = usePluginUIStore((s) => s.activeTheme)
  const setActive = usePluginUIStore((s) => s.setActiveTheme)
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [])

  const entries = Object.entries(data?.themes || {})
  if (entries.length === 0) return null

  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className={`glass grid h-10 w-10 place-items-center rounded-full transition hover:scale-105 active:scale-95 ${
          active ? 'ring-1 ring-cyan-glow/60' : ''
        }`}
        title={t('pluginUI.themePicker')}
        aria-label={t('pluginUI.themePicker')}
      >
        <Palette className={`h-5 w-5 ${active ? 'text-cyan-glow' : 'text-slate-300'}`} />
      </button>

      {open && (
        <div className="glass-strong animate-fade-in absolute right-0 z-50 mt-2 w-56 overflow-hidden rounded-xl p-1">
          <div className="px-3 py-2 text-[10px] uppercase tracking-wider text-slate-400">
            {t('pluginUI.themePicker')}
          </div>
          <button
            type="button"
            onClick={() => { setActive(''); setOpen(false) }}
            className={`flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm transition ${
              !active ? 'bg-nebula-500/30 text-white' : 'text-slate-200 hover:bg-white/10'
            }`}
          >
            <RotateCcw className="h-4 w-4" />
            <span className="flex-1 text-left">{t('pluginUI.builtinTheme')}</span>
            {!active && <Check className="h-4 w-4 text-cyan-glow" />}
          </button>
          {entries.map(([plugin, theme]) => {
            const selected = active === plugin
            return (
              <button
                key={plugin}
                type="button"
                onClick={() => { setActive(plugin); setOpen(false) }}
                className={`flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm transition ${
                  selected ? 'bg-nebula-500/30 text-white' : 'text-slate-200 hover:bg-white/10'
                }`}
              >
                <Palette className="h-4 w-4" />
                <span className="min-w-0 flex-1 truncate text-left">
                  {theme?.label || theme?.name || plugin}
                  <span className="ml-1 text-[10px] text-slate-400">{plugin}</span>
                </span>
                {selected && <Check className="h-4 w-4 shrink-0 text-cyan-glow" />}
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}
