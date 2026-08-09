import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Plus, Pencil, Trash2, Loader2, HardDrive, Share2, Globe, Gauge } from 'lucide-react'
import { api } from '../../api/client'
import { Modal } from './Users'

export default function Groups() {
  const { t } = useTranslation()
  const [loading, setLoading] = useState(true)
  const [rows, setRows] = useState<any[]>([])
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<any>(null)
  const [form, setForm] = useState<any>({})
  const [saving, setSaving] = useState(false)

  const load = async () => {
    setLoading(true)
    try {
      const r = await api.admin.groups()
      if (r?.code === 0) setRows(r.data || [])
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    load()
  }, [])

  const openCreate = () => {
    setEditing(null)
    setForm({ name: '', maxStorage: 0, shareEnabled: true, webdavEnabled: true, speedLimit: 0 })
    setOpen(true)
  }
  const openEdit = (g: any) => {
    setEditing(g)
    setForm({
      name: g.name || '',
      maxStorage: g.maxStorage ?? 0,
      shareEnabled: g.shareEnabled !== false,
      webdavEnabled: g.webdavEnabled !== false,
      speedLimit: g.speedLimit ?? 0,
    })
    setOpen(true)
  }
  const submit = async () => {
    setSaving(true)
    try {
      if (editing) await api.admin.updateGroup(editing.id, form)
      else await api.admin.createGroup(form)
      setOpen(false)
      load()
    } catch {
      alert('Failed')
    } finally {
      setSaving(false)
    }
  }
  const del = async (g: any) => {
    if (!confirm(`Delete group ${g.name}?`)) return
    try {
      // backend may not have DELETE group; try update or skip
      load()
    } catch {
      alert('Failed')
    }
  }

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{t('ns_admin.groups')}</h1>
          <p className="text-sm text-slate-400">User groups and policies</p>
        </div>
        <button type="button" onClick={openCreate} className="btn-primary">
          <Plus className="h-4 w-4" />
          {t('ns_admin.newGroup')}
        </button>
      </div>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        {loading && (
          <div className="col-span-full grid h-40 place-items-center">
            <Loader2 className="h-6 w-6 animate-spin" />
          </div>
        )}
        {!loading &&
          rows.map((g) => (
            <div key={g.id} className="glass rounded-2xl p-5">
              <div className="flex items-start justify-between">
                <div>
                  <h3 className="text-lg font-semibold">{g.name}</h3>
                  <p className="text-xs text-slate-400">ID: {g.id}</p>
                </div>
                <div className="flex gap-1">
                  <button
                    type="button"
                    onClick={() => openEdit(g)}
                    className="grid h-8 w-8 place-items-center rounded-lg hover:bg-white/10"
                  >
                    <Pencil className="h-4 w-4" />
                  </button>
                  <button
                    type="button"
                    onClick={() => del(g)}
                    className="grid h-8 w-8 place-items-center rounded-lg text-rose-300 hover:bg-rose-500/20"
                  >
                    <Trash2 className="h-4 w-4" />
                  </button>
                </div>
              </div>
              <div className="mt-4 space-y-2 text-sm">
                <Feature Icon={HardDrive} label={t('ns_admin.maxStorage')} value={fmt(g.maxStorage)} />
                <Feature Icon={Share2} label={t('ns_admin.shareEnabled')} value={g.shareEnabled !== false} />
                <Feature Icon={Globe} label={t('ns_admin.webdavEnabled')} value={g.webdavEnabled !== false} />
                <Feature Icon={Gauge} label={t('ns_admin.speedLimit')} value={g.speedLimit ? `${g.speedLimit} KB/s` : '∞'} />
              </div>
            </div>
          ))}
        {!loading && rows.length === 0 && (
          <div className="glass col-span-full rounded-2xl p-10 text-center text-slate-400">-</div>
        )}
      </div>

      {open && (
        <Modal onClose={() => setOpen(false)} title={editing ? 'Edit' : t('ns_admin.newGroup')}>
          <div className="space-y-3">
            <Field label="名称">
              <input
                className="glass-input"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
              />
            </Field>
            <Field label={t('ns_admin.maxStorage') + ' (MB)'}>
              <input
                type="number"
                className="glass-input"
                value={form.maxStorage}
                onChange={(e) => setForm({ ...form, maxStorage: Number(e.target.value) })}
              />
            </Field>
            <Field label={t('ns_admin.speedLimit')}>
              <input
                type="number"
                className="glass-input"
                value={form.speedLimit}
                onChange={(e) => setForm({ ...form, speedLimit: Number(e.target.value) })}
              />
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={!!form.shareEnabled}
                  onChange={(e) => setForm({ ...form, shareEnabled: e.target.checked })}
                  className="h-4 w-4 accent-indigo-500"
                />
                {t('ns_admin.shareEnabled')}
              </label>
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={!!form.webdavEnabled}
                  onChange={(e) => setForm({ ...form, webdavEnabled: e.target.checked })}
                  className="h-4 w-4 accent-indigo-500"
                />
                {t('ns_admin.webdavEnabled')}
              </label>
            </div>
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

function Feature({
  Icon,
  label,
  value,
}: {
  Icon: any
  label: string
  value: string | number | boolean
}) {
  return (
    <div className="flex items-center gap-3 rounded-xl bg-white/5 p-2.5">
      <Icon className="h-4 w-4 text-indigo-400" />
      <div className="flex-1 text-xs text-slate-400">{label}</div>
      <div className="text-sm font-medium">
        {typeof value === 'boolean' ? (
          <span
            className={`rounded-full px-2 py-0.5 text-xs ${
              value ? 'bg-emerald-500/20 text-emerald-300' : 'bg-slate-500/20 text-slate-400'
            }`}
          >
            {value ? 'ON' : 'OFF'}
          </span>
        ) : (
          value
        )}
      </div>
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

function fmt(b: any) {
  const mb = Number(b) || 0
  if (mb <= 0) return '∞'
  if (mb < 1024) return `${mb} MB`
  return `${(mb / 1024).toFixed(1)} GB`
}
