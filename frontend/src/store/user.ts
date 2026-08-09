import { create } from 'zustand'

export type SelectMode = 'context' | 'floating' | 'drawer'

interface UserPrefsState {
  logoEgg: boolean
  eggMode: boolean
  selectMode: SelectMode
  suggest2FAHint: boolean
  twoFAEnabled: boolean
  avatarHalo: boolean
  setLogoEgg: (v: boolean) => void
  setEggMode: (v: boolean) => void
  setSelectMode: (m: SelectMode) => void
  set2FAHint: (v: boolean) => void
  setTwoFAEnabled: (v: boolean) => void
  setAvatarHalo: (v: boolean) => void
  load: () => void
  save: () => void
}

const PREFS_KEY = 'nebula.prefs'

const defaultState: Omit<UserPrefsState, 'setLogoEgg' | 'setEggMode' | 'setSelectMode' | 'set2FAHint' | 'setTwoFAEnabled' | 'setAvatarHalo' | 'load' | 'save'> = {
  logoEgg: false,
  eggMode: false,
  selectMode: 'context',
  suggest2FAHint: false,
  twoFAEnabled: false,
  avatarHalo: false,
}

export const useUserPrefsStore = create<UserPrefsState>((set, get) => ({
  ...defaultState,
  load: () => {
    try {
      const raw = localStorage.getItem(PREFS_KEY)
      if (raw) {
        const parsed = JSON.parse(raw)
        const legacyLogoEgg = !!parsed.logoEgg
        set({
          logoEgg: legacyLogoEgg,
          eggMode: parsed.eggMode != null ? !!parsed.eggMode : legacyLogoEgg,
          selectMode: (parsed.selectMode && (parsed.selectMode === 'context' || parsed.selectMode === 'floating' || parsed.selectMode === 'drawer')) ? parsed.selectMode : 'context',
          suggest2FAHint: !!parsed.suggest2FAHint,
          twoFAEnabled: !!parsed.twoFAEnabled,
          avatarHalo: !!parsed.avatarHalo,
        })
      }
    } catch {
    }
  },
  save: () => {
    const s = get()
    localStorage.setItem(
      PREFS_KEY,
      JSON.stringify({
        logoEgg: s.eggMode,
        eggMode: s.eggMode,
        selectMode: s.selectMode,
        suggest2FAHint: s.suggest2FAHint,
        twoFAEnabled: s.twoFAEnabled,
        avatarHalo: s.avatarHalo,
      }),
    )
  },
  setLogoEgg: (v: boolean) => {
    set({ logoEgg: v, eggMode: v })
    get().save()
  },
  setEggMode: (v: boolean) => {
    set({ logoEgg: v, eggMode: v })
    get().save()
  },
  setSelectMode: (m: SelectMode) => {
    set({ selectMode: m })
    get().save()
  },
  set2FAHint: (v: boolean) => {
    set({ suggest2FAHint: v })
    get().save()
  },
  setTwoFAEnabled: (v: boolean) => {
    set({ twoFAEnabled: v })
    get().save()
  },
  setAvatarHalo: (v: boolean) => {
    set({ avatarHalo: v })
    get().save()
  },
}))
