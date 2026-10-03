import { useEffect, useRef, useState, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Bell, Info, CheckCircle2, AlertTriangle, XCircle, CheckCheck, Trash2, X } from 'lucide-react'
import { api } from '../api/client'

type NotifType = 'info' | 'success' | 'warning' | 'error'

interface Notification {
  id: number | string
  type: NotifType
  title: string
  body?: string
  read?: boolean
  createdAt?: string
}

interface Toast extends Notification {
  toastId: number
}

function typeMeta(type: NotifType) {
  switch (type) {
    case 'success': return { Icon: CheckCircle2, cls: 'text-emerald-300', bg: 'bg-emerald-500/15' }
    case 'warning': return { Icon: AlertTriangle, cls: 'text-amber-300', bg: 'bg-amber-500/15' }
    case 'error': return { Icon: XCircle, cls: 'text-rose-300', bg: 'bg-rose-500/15' }
    default: return { Icon: Info, cls: 'text-sky-300', bg: 'bg-sky-500/15' }
  }
}

function fmtTime(s?: string): string {
  if (!s) return ''
  const d = new Date(s)
  if (isNaN(d.getTime())) return s
  return d.toLocaleString()
}

function buildWsUrl(): string {
  const token = localStorage.getItem('nebula_token') || ''
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  let host = window.location.host
  // vite dev server 默认不代理 ws，回退到后端端口
  if (host.endsWith(':5173') || host.endsWith(':5174') || host.endsWith(':5175')) host = 'localhost:5212'
  return `${proto}//${host}/api/ws?token=${encodeURIComponent(token)}`
}

function normalizeNotif(payload: any): Notification {
  const now = Date.now()
  if (!payload) return { id: now, type: 'info', title: '', createdAt: new Date().toISOString() }
  const raw = payload.data || payload.notification || payload
  const type = (['info', 'success', 'warning', 'error'].includes(raw.type) ? raw.type : 'info') as NotifType
  return {
    id: raw.id != null ? raw.id : `${now}-${Math.random().toString(36).slice(2, 7)}`,
    type,
    title: raw.title || raw.message || '',
    body: raw.body || raw.content || (raw.message && raw.title ? raw.message : ''),
    read: false,
    createdAt: raw.createdAt || raw.created_at || raw.time || new Date().toISOString(),
  }
}

export default function NotificationBell() {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [list, setList] = useState<Notification[]>([])
  const [unread, setUnread] = useState(0)
  const [toasts, setToasts] = useState<Toast[]>([])
  const ref = useRef<HTMLDivElement>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectRef = useRef<number | null>(null)
  const toastIdRef = useRef(0)

  const pushToast = useCallback((n: Notification) => {
    const toastId = ++toastIdRef.current
    setToasts((ts) => [...ts, { ...n, toastId }])
    window.setTimeout(() => {
      setToasts((ts) => ts.filter((x) => x.toastId !== toastId))
    }, 3000)
  }, [])

  const loadList = useCallback(async () => {
    try {
      const r = await api.notifications.list(1, 20)
      if (r?.code === 0 && r.data) {
        // 后端返回 { total, list }（见 controllers.ListNotifications）
        setList(Array.isArray(r.data.list) ? r.data.list : [])
      }
    } catch { /* ignore */ }
  }, [])

  const loadUnread = useCallback(async () => {
    try {
      const r = await api.notifications.unreadCount()
      if (r?.code === 0 && typeof r.data === 'number') setUnread(r.data)
    } catch { /* ignore */ }
  }, [])

  useEffect(() => {
    loadList()
    loadUnread()
    let closed = false
    const connect = () => {
      try {
        const ws = new WebSocket(buildWsUrl())
        wsRef.current = ws
        ws.onmessage = (ev) => {
          let payload: any = null
          try { payload = JSON.parse(ev.data) } catch { payload = { title: String(ev.data) } }
          const n = normalizeNotif(payload)
          setList((prev) => [n, ...prev.filter((x) => x.id !== n.id)].slice(0, 50))
          setUnread((u) => u + 1)
          pushToast(n)
        }
        ws.onclose = () => {
          if (closed) return
          if (reconnectRef.current) window.clearTimeout(reconnectRef.current)
          reconnectRef.current = window.setTimeout(connect, 4000)
        }
        ws.onerror = () => { try { ws.close() } catch { /* ignore */ } }
      } catch { /* ignore */ }
    }
    connect()
    return () => {
      closed = true
      if (reconnectRef.current) window.clearTimeout(reconnectRef.current)
      try { wsRef.current?.close() } catch { /* ignore */ }
    }
  }, [loadList, loadUnread, pushToast])

  useEffect(() => {
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [])

  const markRead = async (n: Notification) => {
    if (n.read) return
    try {
      await api.notifications.markRead(n.id)
      setList((prev) => prev.map((x) => (x.id === n.id ? { ...x, read: true } : x)))
      setUnread((u) => Math.max(0, u - 1))
    } catch { /* ignore */ }
  }

  const markAllRead = async () => {
    try {
      await api.notifications.markAllRead()
      setList((prev) => prev.map((x) => ({ ...x, read: true })))
      setUnread(0)
    } catch { /* ignore */ }
  }

  const removeNotif = async (n: Notification, e: React.MouseEvent) => {
    e.stopPropagation()
    try {
      await api.notifications.remove(n.id)
      setList((prev) => prev.filter((x) => x.id !== n.id))
      if (!n.read) setUnread((u) => Math.max(0, u - 1))
    } catch { /* ignore */ }
  }

  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="glass relative grid h-10 w-10 place-items-center rounded-full transition hover:scale-105 active:scale-95"
        title={t('notifications')}
        aria-label={t('notifications')}
      >
        <Bell className="h-5 w-5 text-nebula-300" />
        {unread > 0 && (
          <span className="absolute -right-1 -top-1 grid h-4 min-w-4 place-items-center rounded-full bg-rose-500 px-1 text-[10px] font-bold text-white shadow-[0_0_8px_rgba(239,68,68,0.7)]">
            {unread > 99 ? '99+' : unread}
          </span>
        )}
      </button>

      {open && (
        <div className="glass-strong animate-fade-in absolute right-0 z-50 mt-2 flex max-h-[70vh] w-80 max-w-[90vw] flex-col overflow-hidden rounded-2xl shadow-glass-lg">
          <div className="flex items-center justify-between border-b border-white/10 px-4 py-3">
            <div className="flex items-center gap-2 text-sm font-semibold">
              <Bell className="h-4 w-4 text-nebula-300" />
              {t('notifications')}
            </div>
            <button
              type="button"
              onClick={markAllRead}
              disabled={unread === 0}
              className="flex items-center gap-1 rounded-lg px-2 py-1 text-xs text-cyan-glow transition hover:bg-white/10 disabled:opacity-40"
              title={t('markAllRead')}
            >
              <CheckCheck className="h-3.5 w-3.5" />
              {t('markAllRead')}
            </button>
          </div>
          <div className="flex-1 overflow-y-auto">
            {list.length === 0 ? (
              <div className="py-10 text-center text-xs text-slate-400">{t('noNotifications')}</div>
            ) : (
              list.map((n) => {
                const { Icon, cls, bg } = typeMeta(n.type)
                return (
                  <div
                    key={n.id}
                    role="button"
                    tabIndex={0}
                    onClick={() => markRead(n)}
                    onKeyDown={(e) => { if (e.key === 'Enter') markRead(n) }}
                    className={`group flex w-full items-start gap-3 border-b border-white/5 px-4 py-3 text-left transition hover:bg-white/5 ${n.read ? 'opacity-60' : 'cursor-pointer'}`}
                  >
                    <span className={`grid h-8 w-8 shrink-0 place-items-center rounded-lg ${bg}`}>
                      <Icon className={`h-4 w-4 ${cls}`} />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="flex items-center gap-2">
                        {!n.read && <span className="h-2 w-2 shrink-0 rounded-full bg-rose-500" />}
                        <span className="truncate text-sm font-medium text-slate-100">{n.title}</span>
                      </span>
                      {n.body && <span className="mt-0.5 line-clamp-2 block text-xs text-slate-400">{n.body}</span>}
                      {n.createdAt && <span className="mt-1 block text-[10px] text-slate-500">{fmtTime(n.createdAt)}</span>}
                    </span>
                    <button
                      type="button"
                      onClick={(e) => removeNotif(n, e)}
                      className="grid h-7 w-7 shrink-0 place-items-center rounded-lg text-slate-500 opacity-0 transition hover:bg-rose-500/15 hover:text-rose-300 group-hover:opacity-100"
                      title={t('deleteNotification')}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  </div>
                )
              })
            )}
          </div>
        </div>
      )}

      {/* 右上角 toast */}
      <div className="pointer-events-none fixed right-4 top-16 z-[60] flex w-80 max-w-[90vw] flex-col gap-2">
        {toasts.map((n) => {
          const { Icon, cls, bg } = typeMeta(n.type)
          return (
            <div key={n.toastId} className="glass-strong pointer-events-auto flex items-start gap-3 rounded-xl p-3 shadow-glass-lg animate-slide-up">
              <span className={`grid h-8 w-8 shrink-0 place-items-center rounded-lg ${bg}`}>
                <Icon className={`h-4 w-4 ${cls}`} />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-medium text-slate-100">{n.title}</span>
                {n.body && <span className="block truncate text-xs text-slate-400">{n.body}</span>}
              </span>
              <button
                type="button"
                onClick={() => setToasts((ts) => ts.filter((x) => x.toastId !== n.toastId))}
                className="grid h-6 w-6 shrink-0 place-items-center rounded-lg text-slate-400 hover:bg-white/10"
              >
                <X className="h-3 w-3" />
              </button>
            </div>
          )
        })}
      </div>
    </div>
  )
}
