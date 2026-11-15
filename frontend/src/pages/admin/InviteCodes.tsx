import { useEffect, useState } from 'react'
import {
  Plus, Loader2, KeyRound, Copy, Ban, Trash2, CheckCircle2, RefreshCw, Ticket, Clock, Hash,
} from 'lucide-react'
import { api } from '../../api/client'

interface Plan { id: number; name: string; displayName: string; durationMonths: number; maxStorage: number }
interface CodeRow {
  id: number
  code: string
  planId: number
  planName?: string
  maxUses: number
  usedCount: number
  isActive: boolean
  note: string
  expiresAt: string | null
  createdAt: string
}

function fmtStorage(bytes: number): string {
  if (bytes < 0) return '无限'
  const gb = bytes / 1073741824
  return Number.isInteger(gb) ? `${gb} GB` : `${gb.toFixed(1)} GB`
}
function fmtTime(s: string | null): string {
  if (!s) return '永久有效'
  const d = new Date(s)
  return isNaN(d.getTime()) ? s : d.toLocaleString('zh-CN', { hour12: false })
}

export default function InviteCodes() {
  const [plans, setPlans] = useState<Plan[]>([])
  const [rows, setRows] = useState<CodeRow[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState<{ type: 'ok' | 'err'; text: string } | null>(null)
  const [fresh, setFresh] = useState<string[]>([])
  // 生成表单
  const [planId, setPlanId] = useState<number>(0)
  const [count, setCount] = useState(1)
  const [maxUses, setMaxUses] = useState(1)
  const [expireDays, setExpireDays] = useState<number>(30)
  const [note, setNote] = useState('')

  const flash = (type: 'ok' | 'err', text: string) => {
    setMsg({ type, text })
    window.setTimeout(() => setMsg(null), 4000)
  }

  const loadPlans = async () => {
    try {
      const r = await api.admin.plans()
      if (r?.code === 0 && Array.isArray(r.data)) {
        setPlans(r.data)
        setPlanId((cur) => cur || (r.data[0]?.id ?? 0))
      }
    } catch {}
  }
  const loadCodes = async () => {
    setLoading(true)
    try {
      const r = await api.admin.codes(1, 200)
      if (r?.code === 0 && r.data) {
        setRows(r.data.list || [])
        setTotal(r.data.total || 0)
      }
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { loadPlans(); loadCodes() }, [])

  const generate = async () => {
    if (!planId) { flash('err', '请先选择邀请码类型（套餐）'); return }
    if (count < 1 || count > 1000) { flash('err', '生成数量需在 1–1000 之间'); return }
    setBusy(true)
    try {
      // 有效期：按天数换算成 RFC3339；<=0 表示不设过期（永久）
      let expireAt: string | undefined
      if (expireDays > 0) {
        const d = new Date(Date.now() + expireDays * 86400000)
        expireAt = d.toISOString()
      }
      const r = await api.admin.generateCodes({
        planId, count, maxUses: Math.max(0, maxUses), note: note.trim(), expireAt,
      })
      if (r?.code === 0) {
        const codes = (r.data || []) as string[]
        setFresh(codes)
        flash('ok', `已生成 ${codes.length} 个邀请码，可点击复制`)
        await loadCodes()
      } else {
        flash('err', (r as any)?.message || '生成失败')
      }
    } catch (e: any) {
      flash('err', e?.message || '生成失败')
    } finally {
      setBusy(false)
    }
  }

  const copy = async (text: string) => {
    try { await navigator.clipboard.writeText(text); flash('ok', `已复制 ${text}`) }
    catch { flash('err', '复制失败，请手动选择') }
  }
  const copyAll = async () => {
    if (!fresh.length) return
    await copy(fresh.join('\n'))
  }
  const disable = async (id: number) => {
    try { await api.admin.disableCode(id); flash('ok', '已停用'); await loadCodes() }
    catch (e: any) { flash('err', e?.message || '操作失败') }
  }
  const remove = async (id: number) => {
    if (!window.confirm('确定删除这个邀请码？删除后不可恢复。')) return
    try { await api.admin.deleteCode(id); flash('ok', '已删除'); await loadCodes() }
    catch (e: any) { flash('err', e?.message || '删除失败') }
  }

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="flex items-center gap-2 text-lg font-semibold text-white">
            <KeyRound className="h-5 w-5 text-cyan-glow" /> 注册邀请码
          </h2>
          <p className="mt-1 text-xs text-slate-400">
            随机生成、可设使用次数与有效期；邀请码类型 = 套餐档位，决定注册用户的初始配额与有效期。
            即使关闭自由注册，持有效邀请码也可注册。
          </p>
        </div>
        <button className="btn-ghost flex items-center gap-1.5 rounded-lg px-3 py-2 text-sm" onClick={loadCodes}>
          <RefreshCw className="h-4 w-4" /> 刷新
        </button>
      </div>

      {msg && (
        <div className={`flex items-center gap-2 rounded-xl border px-3 py-2 text-sm ${
          msg.type === 'ok'
            ? 'border-emerald-400/30 bg-emerald-500/10 text-emerald-200'
            : 'border-red-400/30 bg-red-500/10 text-red-200'
        }`}>
          {msg.type === 'ok' ? <CheckCircle2 className="h-4 w-4" /> : <Ban className="h-4 w-4" />}
          {msg.text}
        </div>
      )}

      {/* 生成区 */}
      <section className="glass rounded-2xl p-5">
        <h3 className="mb-4 flex items-center gap-2 text-sm font-semibold text-white">
          <Plus className="h-4 w-4" /> 生成新邀请码
        </h3>
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
          <label className="block">
            <span className="mb-1.5 flex items-center gap-1 text-xs font-medium text-slate-300">
              <Ticket className="h-3.5 w-3.5" /> 邀请码类型（套餐）
            </span>
            <select className="glass-input" value={planId} onChange={(e) => setPlanId(Number(e.target.value))}>
              {plans.length === 0 && <option value={0}>加载中…</option>}
              {plans.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.displayName} · {fmtStorage(p.maxStorage)} · {p.durationMonths} 个月
                </option>
              ))}
            </select>
          </label>
          <label className="block">
            <span className="mb-1.5 flex items-center gap-1 text-xs font-medium text-slate-300">
              <Hash className="h-3.5 w-3.5" /> 生成数量
            </span>
            <input type="number" min={1} max={1000} className="glass-input" value={count}
              onChange={(e) => setCount(Number(e.target.value))} />
          </label>
          <label className="block">
            <span className="mb-1.5 flex items-center gap-1 text-xs font-medium text-slate-300">
              <Hash className="h-3.5 w-3.5" /> 每个码可用次数（0 = 不限）
            </span>
            <input type="number" min={0} className="glass-input" value={maxUses}
              onChange={(e) => setMaxUses(Number(e.target.value))} />
          </label>
          <label className="block">
            <span className="mb-1.5 flex items-center gap-1 text-xs font-medium text-slate-300">
              <Clock className="h-3.5 w-3.5" /> 有效期（天，0 = 永久）
            </span>
            <input type="number" min={0} className="glass-input" value={expireDays}
              onChange={(e) => setExpireDays(Number(e.target.value))} />
          </label>
          <label className="block md:col-span-2">
            <span className="mb-1.5 block text-xs font-medium text-slate-300">备注（选填）</span>
            <input className="glass-input" placeholder="如：2026 春季内测 / 渠道 A" value={note}
              onChange={(e) => setNote(e.target.value)} />
          </label>
        </div>
        <div className="mt-4 flex justify-end">
          <button className="btn-primary flex items-center gap-2 rounded-lg px-5 py-2.5 text-sm" onClick={generate} disabled={busy}>
            {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <KeyRound className="h-4 w-4" />}
            随机生成
          </button>
        </div>

        {fresh.length > 0 && (
          <div className="mt-4 rounded-xl border border-emerald-400/20 bg-emerald-500/5 p-4">
            <div className="mb-2 flex items-center justify-between">
              <span className="text-xs font-medium text-emerald-200">本次生成 {fresh.length} 个</span>
              <button className="btn-ghost flex items-center gap-1 rounded-lg px-2.5 py-1.5 text-xs" onClick={copyAll}>
                <Copy className="h-3.5 w-3.5" /> 复制全部
              </button>
            </div>
            <div className="flex max-h-40 flex-wrap gap-2 overflow-auto">
              {fresh.map((c) => (
                <button key={c} onClick={() => copy(c)}
                  className="rounded-lg border border-white/10 bg-white/5 px-3 py-1.5 font-mono text-sm tracking-wider text-white hover:bg-white/10">
                  {c}
                </button>
              ))}
            </div>
          </div>
        )}
      </section>

      {/* 列表 */}
      <section className="glass rounded-2xl p-5">
        <div className="mb-3 flex items-center justify-between">
          <h3 className="text-sm font-semibold text-white">已生成邀请码</h3>
          <span className="text-xs text-slate-400">共 {total} 个</span>
        </div>
        {loading ? (
          <div className="flex items-center justify-center py-10 text-slate-400">
            <Loader2 className="h-5 w-5 animate-spin" />
          </div>
        ) : rows.length === 0 ? (
          <div className="py-10 text-center text-sm text-slate-400">还没有邀请码，先在上方生成一批吧</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="border-b border-white/10 text-xs text-slate-400">
                  <th className="py-2 pr-3 font-medium">邀请码</th>
                  <th className="py-2 pr-3 font-medium">类型</th>
                  <th className="py-2 pr-3 font-medium">次数</th>
                  <th className="py-2 pr-3 font-medium">状态</th>
                  <th className="py-2 pr-3 font-medium">有效期至</th>
                  <th className="py-2 pr-3 font-medium">备注</th>
                  <th className="py-2 font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => (
                  <tr key={r.id} className="border-b border-white/5">
                    <td className="py-2.5 pr-3">
                      <button onClick={() => copy(r.code)} className="flex items-center gap-1.5 font-mono tracking-wider text-white hover:text-cyan-glow">
                        {r.code} <Copy className="h-3 w-3 opacity-50" />
                      </button>
                    </td>
                    <td className="py-2.5 pr-3 text-slate-300">{r.planName || `#${r.planId}`}</td>
                    <td className="py-2.5 pr-3 text-slate-300">
                      {r.usedCount} / {r.maxUses > 0 ? r.maxUses : '∞'}
                    </td>
                    <td className="py-2.5 pr-3">
                      {r.isActive ? (
                        <span className="rounded-full border border-emerald-400/20 bg-emerald-500/10 px-2 py-0.5 text-[11px] text-emerald-300">有效</span>
                      ) : (
                        <span className="rounded-full border border-slate-400/20 bg-slate-500/10 px-2 py-0.5 text-[11px] text-slate-300">已停用</span>
                      )}
                    </td>
                    <td className="py-2.5 pr-3 text-xs text-slate-400">{fmtTime(r.expiresAt)}</td>
                    <td className="py-2.5 pr-3 text-xs text-slate-400">{r.note || '—'}</td>
                    <td className="py-2.5">
                      <div className="flex items-center gap-1">
                        {r.isActive && (
                          <button onClick={() => disable(r.id)} title="停用"
                            className="rounded-lg p-1.5 text-slate-400 hover:bg-white/10 hover:text-amber-300">
                            <Ban className="h-4 w-4" />
                          </button>
                        )}
                        <button onClick={() => remove(r.id)} title="删除"
                          className="rounded-lg p-1.5 text-slate-400 hover:bg-white/10 hover:text-red-300">
                          <Trash2 className="h-4 w-4" />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  )
}
