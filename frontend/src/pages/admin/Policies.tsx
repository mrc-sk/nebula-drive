import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Plus, Pencil, Trash2, Loader2, Star, MoreHorizontal, Database, X, CheckCircle2, AlertCircle } from 'lucide-react'
import { api } from '../../api/client'
import { Modal } from './Users'

const TYPES = ['local', 'S3', 'OSS', 'COS']

export default function Policies() {
  const { t } = useTranslation()
  const [loading, setLoading] = useState(true)
  const [rows, setRows] = useState<any[]>([])
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<any>(null)
  const [form, setForm] = useState<any>({})
  const [saving, setSaving] = useState(false)
  const [kebabId, setKebabId] = useState<any>(null)
  const kebabRef = useRef<HTMLDivElement>(null)
  const [testState, setTestState] = useState<{ id: any; state: 'idle' | 'testing' | 'ok' | 'fail'; msg: string }>({ id: null, state: 'idle', msg: '' })

  const load = async () => {
    setLoading(true)
    try {
      const r = await api.admin.policies()
      if (r?.code === 0) setRows(r.data || [])
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    load()
  }, [])

  useEffect(() => {
    const onClick = (e: MouseEvent) => {
      if (kebabRef.current && !kebabRef.current.contains(e.target as Node)) {
        setKebabId(null)
      }
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [])

  const openCreate = () => {
    setEditing(null)
    setForm({ name: '', type: 'local', config: '{}', isDefault: false })
    setOpen(true)
  }
  const openEdit = (p: any) => {
    setEditing(p)
    let cfg = p.config
    if (typeof cfg === 'object') cfg = JSON.stringify(cfg, null, 2)
    else if (!cfg) cfg = '{}'
    setForm({
      name: p.name || '',
      type: p.type || 'local',
      config: cfg,
      isDefault: !!p.isDefault,
    })
    setOpen(true)
    setKebabId(null)
  }
  const submit = async () => {
    let cfg: any = form.config
    if (typeof cfg === 'string') {
      try {
        cfg = JSON.parse(cfg)
      } catch {
        alert('Invalid JSON')
        return
      }
    }
    setSaving(true)
    try {
      const body = { name: form.name, type: form.type, config: cfg, isDefault: !!form.isDefault }
      if (editing) await api.admin.updatePolicy(editing.id, body)
      else await api.admin.createPolicy(body)
      setOpen(false)
      load()
    } catch {
      alert('Failed')
    } finally {
      setSaving(false)
    }
  }
  const del = async (p: any) => {
    if (!confirm(`Delete policy ${p.name}?`)) return
    try {
      await api.admin.deletePolicy(p.id)
      load()
    } catch {
      alert('Failed')
    }
    setKebabId(null)
  }
  const testConn = async (p: any) => {
    setTestState({ id: p.id, state: 'testing', msg: '' })
    try {
      const r = await api.admin.testPolicy(p.id, {})
      if (r?.code === 0) setTestState({ id: p.id, state: 'ok', msg: t('ns_admin.testConn') + ' OK' })
      else setTestState({ id: p.id, state: 'fail', msg: r?.message || 'Failed' })
    } catch (e: any) {
      setTestState({ id: p.id, state: 'fail', msg: e?.message || 'Failed' })
    } finally {
      setTimeout(() => setTestState({ id: null, state: 'idle', msg: '' }), 2200)
    }
    setKebabId(null)
  }

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{t('ns_admin.policies')}</h1>
          <p className="text-sm text-slate-400">Storage backend policies</p>
        </div>
        <button type="button" onClick={openCreate} className="btn-primary">
          <Plus className="h-4 w-4" />
          {t('ns_admin.newPolicy')}
        </button>
      </div>

      <div className="glass overflow-hidden rounded-2xl">
        <table className="min-w-full text-sm">
          <thead className="border-b border-white/10 bg-white/5 text-left text-xs uppercase text-slate-400">
            <tr>
              <th className="px-4 py-3">ID</th>
              <th className="px-4 py-3">{t('install.db.type') || 'Type'}</th>
              <th className="px-4 py-3">名称</th>
              <th className="px-4 py-3">{t('ns_admin.default')}</th>
              <th className="px-4 py-3">状态</th>
              <th className="px-4 py-3 text-right">操作</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-white/5">
            {loading && (
              <tr>
                <td colSpan={6} className="py-10 text-center">
                  <Loader2 className="mx-auto h-6 w-6 animate-spin" />
                </td>
              </tr>
            )}
            {!loading &&
              rows.map((p) => (
                <tr key={p.id} className="hover:bg-white/5">
                  <td className="px-4 py-3 text-slate-400">#{p.id}</td>
                  <td className="px-4 py-3">
                    <span className="rounded-full bg-sky-500/20 px-2 py-0.5 text-xs text-sky-300">{p.type}</span>
                  </td>
                  <td className="px-4 py-3 font-medium">
                    <div className="flex items-center gap-2">
                      <Database className="h-4 w-4 text-nebula-300" />
                      {p.name}
                    </div>
                  </td>
                  <td className="px-4 py-3">
                    {p.isDefault ? (
                      <Star className="h-4 w-4 fill-amber-400 text-amber-400" />
                    ) : (
                      <span className="text-slate-500">-</span>
                    )}
                  </td>
                  <td className="px-4 py-3">
                    {testState.id === p.id && testState.state !== 'idle' && (
                      <span
                        className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs ${
                          testState.state === 'ok'
                            ? 'bg-emerald-500/20 text-emerald-300'
                            : testState.state === 'fail'
                            ? 'bg-rose-500/20 text-rose-300'
                            : 'bg-sky-500/20 text-sky-300'
                        }`}
                      >
                        {testState.state === 'testing' && <Loader2 className="h-3 w-3 animate-spin" />}
                        {testState.state === 'ok' && <CheckCircle2 className="h-3 w-3" />}
                        {testState.state === 'fail' && <AlertCircle className="h-3 w-3" />}
                        {testState.msg}
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-3">
                    <div className="relative flex justify-end" ref={kebabId === p.id ? kebabRef : undefined}>
                      <button
                        type="button"
                        onClick={() => setKebabId(kebabId === p.id ? null : p.id)}
                        className="grid h-8 w-8 place-items-center rounded-lg hover:bg-white/10"
                      >
                        <MoreHorizontal className="h-4 w-4" />
                      </button>
                      {kebabId === p.id && (
                        <div className="glass-strong kebab-pop absolute right-0 top-10 z-20 w-44 overflow-hidden rounded-xl p-1 shadow-glass-lg">
                          <button
                            type="button"
                            onClick={() => openEdit(p)}
                            className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm text-slate-200 hover:bg-white/10"
                          >
                            <Pencil className="h-4 w-4" />
                            {t('edit')}
                          </button>
                          <button
                            type="button"
                            onClick={() => testConn(p)}
                            disabled={testState.state === 'testing'}
                            className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm text-slate-200 hover:bg-white/10 disabled:opacity-50"
                          >
                            <Database className="h-4 w-4" />
                            {t('ns_admin.testConn')}
                          </button>
                          <div className="my-1 h-px bg-white/10" />
                          <button
                            type="button"
                            onClick={() => del(p)}
                            className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm text-rose-300 hover:bg-white/10"
                          >
                            <Trash2 className="h-4 w-4" />
                            {t('delete')}
                          </button>
                        </div>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            {!loading && rows.length === 0 && (
              <tr>
                <td colSpan={6} className="py-10 text-center text-slate-400">
                  -
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {open && (
        <Modal onClose={() => setOpen(false)} title={editing ? 'Edit' : t('ns_admin.newPolicy')}>
          <div className="space-y-3">
            <Field label="名称">
              <input
                className="glass-input"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
              />
            </Field>
            <Field label={t('ns_admin.type')}>
              <select
                className="glass-input"
                value={form.type}
                onChange={(e) => setForm({ ...form, type: e.target.value })}
              >
                {TYPES.map((tp) => (
                  <option key={tp} value={tp}>
                    {tp}
                  </option>
                ))}
              </select>
            </Field>
            <Field label={t('ns_admin.configJSON')}>
              <textarea
                rows={6}
                className="glass-input font-mono text-xs"
                value={form.config}
                onChange={(e) => setForm({ ...form, config: e.target.value })}
              />
            </Field>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={!!form.isDefault}
                onChange={(e) => setForm({ ...form, isDefault: e.target.checked })}
                className="h-4 w-4 accent-nebula-500"
              />
              {t('ns_admin.default')}
            </label>
          </div>
          <div className="mt-5 flex justify-end gap-2">
            <button type="button" onClick={() => setOpen(false)} className="btn-ghost">
              {t('common.cancel')}
            </button>
            <button type="button" onClick={submit} disabled={saving} className="btn-primary">
              {saving && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('common.save')}
            </button>
          </div>
        </Modal>
      )}
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <div className="mb-1 text-xs text-slate-400">{label}</div>
      {children}
    </label>
  )
}
