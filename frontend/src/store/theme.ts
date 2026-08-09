import { create } from 'zustand'

export type Theme = 'dark' | 'light' | 'auto'

interface ThemeState {
  theme: Theme
  resolvedTheme: 'dark' | 'light'
  setTheme: (t: Theme) => void
  toggleAuto: () => void
}

const THEME_KEY = 'nebula.theme'

function resolveTheme(theme: Theme): 'dark' | 'light' {
  if (theme === 'auto') {
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  }
  return theme
}

function applyResolvedTheme(resolved: 'dark' | 'light') {
  const root = document.documentElement
  if (resolved === 'dark') root.classList.add('dark')
  else root.classList.remove('dark')
}

function readInitialTheme(): Theme {
  const saved = localStorage.getItem(THEME_KEY) || localStorage.getItem('theme')
  if (saved === 'light' || saved === 'dark' || saved === 'auto') return saved
  return 'auto'
}

const initialTheme = readInitialTheme()
let mqListener: (() => void) | null = null

function setupAutoListener(getState: () => ThemeState) {
  if (typeof window === 'undefined') return
  const mq = window.matchMedia('(prefers-color-scheme: dark)')
  mqListener = () => {
    const s = getState()
    if (s.theme === 'auto') {
      const resolved = resolveTheme('auto')
      applyResolvedTheme(resolved)
      s.resolvedTheme = resolved
    }
  }
  if (mq.addEventListener) {
    mq.addEventListener('change', mqListener)
  } else if ((mq as any).addListener) {
    ;(mq as any).addListener(mqListener)
  }
}

const initialResolved = typeof window !== 'undefined' ? resolveTheme(initialTheme) : 'dark'
applyResolvedTheme(initialResolved)

export const useThemeStore = create<ThemeState>((set, get) => {
  if (typeof window !== 'undefined') {
    setupAutoListener(get as any)
  }
  return {
    theme: initialTheme,
    resolvedTheme: initialResolved,
    setTheme: (t: Theme) => {
      localStorage.setItem(THEME_KEY, t)
      const resolved = resolveTheme(t)
      applyResolvedTheme(resolved)
      set({ theme: t, resolvedTheme: resolved })
    },
    toggleAuto: () => {
      const cur = get().theme
      const next: Theme = cur === 'light' ? 'dark' : cur === 'dark' ? 'auto' : 'light'
      get().setTheme(next)
    },
  }
})
