import { useEffect, useState } from 'react'
import { AlertTriangle, CheckCircle2, Loader2, Save, SlidersHorizontal, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api, type PluginSettingField } from '../api/client'
import { usePluginUIStore } from '../store/pluginUI'

interface Props {
  open: boolean
  onClose: () => void
}

/**
 * 插件设置面板。
 *
 * 字段完全来自 co.BEGG 的 ui.settings 声明，宿主不认识具体配置项，
 * 只负责渲染控件并把值 POST 给 /api/plugins/ui/settings/:plugin；
 * 真正的校验与落盘由插件在 onUISettingsSave 钩子里自己完成。
 */
export default function PluginSettingsPanel({ open, onClose }: Props) {
  const { t } = useTranslation()
  const settings = usePluginUIStore((s) => s.data?.settings) || {}
  const [values, setValues] = useState<Record<string, Record<string, any>>>({})
  const [busy, setBusy] = useState('')
  const [msg, setMsg] = useState<{ type: 'ok' | 'err'; text: string } | null>(null)

  useEffect(() => {
    if (!open) return
    const init: Record<string, Record<string, any>> = {}
    for (const [plugin, fields] of Object.entries(settings)) {
      init[plugin] = {}
      for (const f of fields || []) {
        init[plugin][f.key] =
          f.type === 'bool' ? (f.default === 'true' || f.default === '1') : (f.default ?? '')
      }
    }
    setValues(init)
    setMsg(null)
  }, [open, settings])

  if (!open) return null

  const groups = Object.entries(settings).filter(([, fields]) => (fields || []).length > 0)
  const flash = (type: 'ok' | 'err', text: string) => {
    setMsg({ type, text })
    setTimeout(() => setMsg(null), 4000)
  }

  const setValue = (plugin: string, key: string, v: any) => {
    setValues((prev) => ({
      ...prev,
      [plugin]: { ...(prev[plugin] || {}), [key]: v },
    }))
  }

  const save = async (plugin: string) => {
    setBusy(plugin)
    try {
      const r = await api.savePluginUISettings(plugin, values[plugin] || {})
      if (r?.code === 0) flash('ok', t('pluginUI.settingsSaved', { name: plugin }))
      else flash('err', r?.message || t('pluginUI.settingsFailed'))
    } catch (e: any) {
      flash('err', e?.message || t('pluginUI.settingsFailed'))
    } finally {
      setBusy('')
    }
  }

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/70 backdrop-blur-sm" onClick={onClose} />
      <div className="glass-strong relative z-10 flex max-h-[86vh] w-full max-w-2xl flex-col overflow-hidden rounded-2xl border border-white/15 shadow-2xl">
        <div className="flex items-start gap-3 border-b border-white/10 px-6 py-4">
          <div className="grid h-10 w-10 shrink-0 place-items-center rounded-xl bg-gradient-to-br from-nebula-500/30 to-cyan-glow/10 border border-white/10">
            <SlidersHorizontal className="h-5 w-5 text-cyan-glow" />
          </div>
          <div className="min-w-0 flex-1">
            <h2 className="text-base font-semibold text-white">{t('pluginUI.settingsTitle')}</h2>
            <p className="mt-0.5 text-xs text-slate-400">{t('pluginUI.settingsHint')}</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="grid h-8 w-8 place-items-center rounded-lg text-slate-400 hover:bg-white/10 hover:text-white"
            title={t('common.close')}
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-6 py-5">
          {msg && (
            <div className={`flex items-start gap-2 rounded-lg border px-3 py-2 text-xs ${
              msg.type === 'ok'
                ? 'border-emerald-400/30 bg-emerald-500/10 text-emerald-300'
                : 'border-rose-400/30 bg-rose-500/10 text-rose-300'
            }`}>
              {msg.type === 'ok'
                ? <CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                : <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />}
              <span className="break-all">{msg.text}</span>
            </div>
          )}

          {groups.length === 0 ? (
            <div className="grid h-32 place-items-center text-sm text-slate-400">
              {t('pluginUI.noSettings')}
            </div>
          ) : groups.map(([plugin, fields]) => (
            <section key={plugin} className="glass rounded-xl p-4">
              <div className="mb-3 flex items-center justify-between gap-2">
                <h3 className="truncate text-sm font-semibold text-white">{plugin}</h3>
                <button
                  type="button"
                  onClick={() => save(plugin)}
                  disabled={busy === plugin}
                  className="btn-primary flex items-center gap-1.5 px-3 py-1.5 text-xs"
                >
                  {busy === plugin
                    ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    : <Save className="h-3.5 w-3.5" />}
                  {t('common.save')}
                </button>
              </div>
              <div className="space-y-3">
                {(fields || []).map((f: PluginSettingField) => (
                  <Field
                    key={f.key}
                    field={f}
                    value={values[plugin]?.[f.key]}
                    onChange={(v) => setValue(plugin, f.key, v)}
                  />
                ))}
              </div>
            </section>
          ))}
        </div>

        <div className="border-t border-white/10 px-6 py-3 text-right">
          <button type="button" onClick={onClose} className="btn-ghost">
            {t('common.close')}
          </button>
        </div>
      </div>
    </div>
  )
}

function Field({
  field, value, onChange,
}: {
  field: PluginSettingField
  value: any
  onChange: (v: any) => void
}) {
  const base = 'glass-input w-full'
  return (
    <div>
      <label className="mb-1 block text-xs font-medium text-slate-300">
        {field.label || field.key}
      </label>
      {field.type === 'bool' ? (
        <label className="flex cursor-pointer items-center gap-2 text-sm text-slate-200">
          <input
            type="checkbox"
            checked={!!value}
            onChange={(e) => onChange(e.target.checked)}
            className="h-4 w-4 rounded border-white/20 bg-white/5 accent-cyan-400"
          />
          <span className="text-xs text-slate-400">{field.key}</span>
        </label>
      ) : field.type === 'select' ? (
        <select
          value={value ?? ''}
          onChange={(e) => onChange(e.target.value)}
          className={base}
        >
          {(field.options || '').split(',').map((o) => o.trim()).filter(Boolean).map((o) => (
            <option key={o} value={o}>{o}</option>
          ))}
        </select>
      ) : field.type === 'textarea' ? (
        <textarea
          value={value ?? ''}
          onChange={(e) => onChange(e.target.value)}
          rows={3}
          className={base}
        />
      ) : (
        <input
          type={field.type === 'password' ? 'password' : field.type === 'number' ? 'number' : 'text'}
          value={value ?? ''}
          onChange={(e) => onChange(field.type === 'number' ? Number(e.target.value) : e.target.value)}
          className={base}
        />
      )}
      {field.help && <p className="mt-1 text-[11px] text-slate-400">{field.help}</p>}
    </div>
  )
}
