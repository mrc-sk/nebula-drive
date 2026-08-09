import { create } from 'zustand'
import { api, type AuthUser } from '../api/client'

const TOKEN_KEY = 'nebula_token'

interface AuthState {
  user: AuthUser | null
  token: string | null
  loading: boolean
  setAuth: (user: AuthUser | null, token?: string | null) => void
  clearAuth: () => void
  fetchMe: () => Promise<void>
}

export const useAuthStore = create<AuthState>((set) => ({
  user: null,
  token: localStorage.getItem(TOKEN_KEY),
  loading: !!localStorage.getItem(TOKEN_KEY),
  setAuth: (user, token) => {
    if (token) {
      localStorage.setItem(TOKEN_KEY, token)
      set({ user, token, loading: false })
    } else {
      set({ user, loading: false })
    }
  },
  clearAuth: () => {
    localStorage.removeItem(TOKEN_KEY)
    set({ user: null, token: null, loading: false })
  },
  fetchMe: async () => {
    const token = localStorage.getItem(TOKEN_KEY)
    if (!token) {
      set({ user: null, token: null, loading: false })
      return
    }
    set({ loading: true })
    try {
      const res = await api.me()
      if (res && res.code === 0 && res.data) {
        set({ user: res.data, token, loading: false })
      } else {
        localStorage.removeItem(TOKEN_KEY)
        set({ user: null, token: null, loading: false })
      }
    } catch {
      // 401 或其它错误：视为未登录，清除本地凭证
      localStorage.removeItem(TOKEN_KEY)
      set({ user: null, token: null, loading: false })
    }
  },
}))
