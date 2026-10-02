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
      // 后端 /api/auth/me 返回的是 { code, data: { user, suggest2FAHint } }，
      // 用户对象嵌在 data.user 里。这里必须解一层，否则 user.isAdmin 恒为 undefined，
      // 路由守卫会把所有 /admin/* 弹回 /files（登录后首次进入正常、一刷新管理后台就进不去）。
      // 同时兼容历史上可能存在的扁平返回。
      const payload = res?.data as any
      const me: AuthUser | null = payload?.user ?? (payload?.userName ? payload : null)
      if (res && res.code === 0 && me) {
        set({ user: me, token, loading: false })
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
