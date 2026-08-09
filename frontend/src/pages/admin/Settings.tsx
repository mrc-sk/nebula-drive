import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, Home, Mail, Shield, Database, Globe, Palette, Puzzle, HelpCircle, ExternalLink, CheckSquare, Square, Upload, Image as ImageIcon, Check, AlertCircle } from 'lucide-react'
import { api } from '../../api/client'
import { LANGUAGES } from '../../i18n'

const TABS = [
  { key: 'basic', label: 'basic', Icon: Home },
  { key: 'mail', label: 'mailCache', Icon: Mail },
  { key: 'security', label: 'security', Icon: Shield },
  { key: 'storage', label: 'uploadWebdav', Icon: Database },
  { key: 'webdav', label: 'webdav', Icon: Globe },
  { key: 'appearance', label: 'appearance', Icon: Palette },
  { key: 'plugins', label: 'plugins', Icon: Puzzle },
] as const

type TabKey = (typeof TABS)[number]['key']

const WEBDAV_METHODS = ['GET', 'HEAD', 'POST', 'PUT', 'DELETE', 'MKCOL', 'COPY', 'MOVE', 'PROPFIND', 'PROPPATCH', 'LOCK', 'UNLOCK']

const PRESET_COLORS = [
  { name: 'purple', value: '#a855f7' },
  { name: 'cyan', value: '#22d3ee' },
  { name: 'green', value: '#10b981' },
  { name: 'orange', value: '#f97316' },
  { name: 'pink', value: '#ec4899' },
  { name: 'blue', value: '#3b82f6' },
]

function fileToDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(reader.error || new Error('read failed'))
    reader.readAsDataURL(file)
  })
}

export default function Settings() {
  const { t } = useTranslation()
  const [tab, setTab] = useState<TabKey>('basic')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [data, setData] = useState<Record<string, any>>({})
  const [logoUploading, setLogoUploading] = useState(false)
  const [logoMsg, setLogoMsg] = useState<{ type: 'ok' | 'err'; text: string } | null>(null)
  const logoInputRef = useRef<HTMLInputElement>(null)

  const load = async () => {
    setLoading(true)
    try {
      const r = await api.admin.settings()
      if (r?.code === 0 && r.data) setData(r.data)
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    load()
  }, [])

  const set = (k: string, v: any) => setData({ ...data, [k]: v })

  // 主题色实时预览：写入 CSS 变量 --theme-primary
  useEffect(() => {
    const color = data['brand.theme_color']
    if (color && /^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.test(color)) {
      document.documentElement.style.setProperty('--theme-primary', color)
    }
  }, [data['brand.theme_color']])

  const onLogoPick = async (file?: File) => {
    if (!file) return
    setLogoUploading(true)
    setLogoMsg(null)
    try {
      let stored = false
      try {
        const r = await api.admin.uploadLogo(file)
        if (r?.code === 0) {
          const path = (r.data as any)?.path || (r.data as any)?.url || 'uploaded'
          set('brand.logo', path)
          stored = true
          setLogoMsg({ type: 'ok', text: t('logoUpload') + ' OK' })
        }
      } catch {
        // 上传接口不可用 → base64 存到 brand.logo_data
      }
      if (!stored) {
        const dataUrl = await fileToDataURL(file)
        set('brand.logo_data', dataUrl)
        setLogoMsg({ type: 'ok', text: t('logoUpload') + ' OK (base64)' })
      }
    } catch (e: any) {
      setLogoMsg({ type: 'err', text: e?.message || 'upload failed' })
    } finally {
      setLogoUploading(false)
      if (logoInputRef.current) logoInputRef.current.value = ''
      window.setTimeout(() => setLogoMsg(null), 2500)
    }
  }

  const toggleMethod = (m: string) => {
    const methods: string[] = Array.isArray(data.webdavMethods) ? [...data.webdavMethods] : (data.webdavMethods || 'GET,HEAD,POST,PUT,DELETE').toString().split(',').map((s: string) => s.trim()).filter(Boolean)
    const i = methods.indexOf(m)
    if (i >= 0) methods.splice(i, 1)
    else methods.push(m)
    set('webdavMethods', methods)
  }
  const methodsList: string[] = Array.isArray(data.webdavMethods)
    ? data.webdavMethods
    : (data.webdavMethods || 'GET,HEAD,POST,PUT,DELETE').toString().split(',').map((s: string) => s.trim()).filter(Boolean)

  const save = async () => {
    setSaving(true)
    try {
      const payload = { ...data }
      if (Array.isArray(payload.webdavMethods)) payload.webdavMethods = payload.webdavMethods.join(',')
      await api.admin.saveSettings(payload)
      alert('Saved')
    } catch {
      alert('Failed')
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="grid h-80 place-items-center">
        <Loader2 className="h-8 w-8 animate-spin" />
      </div>
    )
  }

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-2xl font-semibold">{t('ns_admin.settings')}</h1>
        <p className="text-sm text-slate-400">System configuration</p>
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[220px_1fr]">
        <aside className="glass rounded-2xl p-2 lg:sticky lg:top-4">
          <nav className="space-y-1">
            {TABS.map((tb) => {
              const Icon = tb.Icon
              const active = tab === tb.key
              return (
                <button
                  key={tb.key}
                  onClick={() => setTab(tb.key)}
                  className={`flex w-full items-center gap-2.5 rounded-xl px-3 py-2.5 text-sm transition ${
                    active
                      ? 'vtab-active text-white'
                      : 'text-slate-200 hover:bg-white/10'
                  }`}
                >
                  <Icon className="h-4 w-4 shrink-0" />
                  <span className="truncate">{t(`ns_admin.${tb.label}`)}</span>
                </button>
              )
            })}
          </nav>
        </aside>

        <section className="glass rounded-2xl p-6">
          {tab === 'basic' && (
            <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
              <Field label={t('ns_admin.siteName')}>
                <input className="glass-input" value={data.siteName || ''} onChange={(e) => set('siteName', e.target.value)} />
              </Field>
              <Field label={t('ns_admin.domain')}>
                <input className="glass-input" value={data.domain || ''} onChange={(e) => set('domain', e.target.value)} />
              </Field>
              <Field label={t('ns_admin.protocol')}>
                <select className="glass-input" value={data.protocol || 'https'} onChange={(e) => set('protocol', e.target.value)}>
                  <option value="https">HTTPS</option>
                  <option value="http">HTTP</option>
                </select>
              </Field>
              <Field label={t('ns_admin.defaultLang')}>
                <select
                  className="glass-input"
                  value={data.defaultLang || data.language || 'zh-CN'}
                  onChange={(e) => set('defaultLang', e.target.value)}
                >
                  {LANGUAGES.map((l) => (
                    <option key={l.code} value={l.code}>
                      {l.label}
                    </option>
                  ))}
                </select>
              </Field>
              <Field label={t('ns_admin.timezone')} className="md:col-span-2">
                <input className="glass-input" value={data.timezone || 'Asia/Shanghai'} onChange={(e) => set('timezone', e.target.value)} />
              </Field>
            </div>
          )}

          {tab === 'mail' && (
            <div className="space-y-4">
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={!!data.mailEnabled}
                  onChange={(e) => set('mailEnabled', e.target.checked)}
                  className="h-4 w-4 accent-nebula-500"
                />
                启用邮件服务
              </label>
              <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                <Field label={t('ns_admin.smtpHost')}>
                  <input className="glass-input" value={data.smtpHost || ''} onChange={(e) => set('smtpHost', e.target.value)} />
                </Field>
                <Field label={t('ns_admin.smtpPort')}>
                  <input
                    type="number"
                    className="glass-input"
                    value={data.smtpPort || 587}
                    onChange={(e) => set('smtpPort', Number(e.target.value))}
                  />
                </Field>
                <Field label={t('ns_admin.smtpUser')}>
                  <input className="glass-input" value={data.smtpUser || ''} onChange={(e) => set('smtpUser', e.target.value)} />
                </Field>
                <Field label={t('ns_admin.smtpPassword')}>
                  <input
                    type="password"
                    className="glass-input"
                    value={data.smtpPassword || ''}
                    onChange={(e) => set('smtpPassword', e.target.value)}
                  />
                </Field>
                <Field label={t('ns_admin.smtpFrom')} className="md:col-span-2">
                  <input className="glass-input" value={data.smtpFrom || ''} onChange={(e) => set('smtpFrom', e.target.value)} />
                </Field>
              </div>
            </div>
          )}

          {tab === 'security' && (
            <div className="space-y-4">
              <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                <Field label="登录失败锁定次数">
                  <input type="number" className="glass-input" value={data.loginMaxAttempts || 5} onChange={(e) => set('loginMaxAttempts', Number(e.target.value))} />
                </Field>
                <Field label="最小密码长度">
                  <input type="number" className="glass-input" value={data.minPasswordLen || 6} onChange={(e) => set('minPasswordLen', Number(e.target.value))} />
                </Field>
                <Field label="会话超时 (分钟)" className="md:col-span-2">
                  <input type="number" className="glass-input" value={data.sessionTTL || 1440} onChange={(e) => set('sessionTTL', Number(e.target.value))} />
                </Field>
                <label className="flex items-center gap-2 text-sm">
                  <input type="checkbox" className="h-4 w-4 accent-nebula-500" checked={!!data.twoFARequired} onChange={(e) => set('twoFARequired', e.target.checked)} />
                  强制管理员开启 2FA
                </label>
                <label className="flex items-center gap-2 text-sm">
                  <input type="checkbox" className="h-4 w-4 accent-nebula-500" checked={!!data.registerEnabled} onChange={(e) => set('registerEnabled', e.target.checked)} />
                  允许用户注册
                </label>
              </div>

              {/* 密码策略 */}
              <div className="border-t border-white/10 pt-4">
                <div className="mb-2 text-sm font-semibold text-slate-200">{t('passwordPolicy')}</div>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <Field label={t('minLength')}>
                    <input type="number" className="glass-input" value={data.passwordMinLen || 8} onChange={(e) => set('passwordMinLen', Number(e.target.value))} />
                  </Field>
                  <div className="flex flex-col justify-center gap-3">
                    <ToggleField label={t('requireUpper')} checked={!!data.passwordRequireUpper} onChange={(v) => set('passwordRequireUpper', v)} />
                    <ToggleField label={t('requireDigit')} checked={!!data.passwordRequireDigit} onChange={(v) => set('passwordRequireDigit', v)} />
                    <ToggleField label={t('requireSpecial')} checked={!!data.passwordRequireSpecial} onChange={(v) => set('passwordRequireSpecial', v)} />
                  </div>
                </div>
              </div>

              {/* 验证码 */}
              <div className="border-t border-white/10 pt-4">
                <div className="mb-2 text-sm font-semibold text-slate-200">{t('captcha')}</div>
                <div className="space-y-3">
                  <ToggleField label={t('captchaEnabled')} checked={!!data.captchaEnabled} onChange={(v) => set('captchaEnabled', v)} />
                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <Field label={t('hcaptchaSiteKey')}>
                      <input className="glass-input" value={data.hcaptchaSiteKey || ''} onChange={(e) => set('hcaptchaSiteKey', e.target.value)} />
                    </Field>
                    <Field label={t('hcaptchaSecret')}>
                      <input type="password" className="glass-input" value={data.hcaptchaSecret || ''} onChange={(e) => set('hcaptchaSecret', e.target.value)} />
                    </Field>
                  </div>
                </div>
              </div>

              {/* 上传安全 */}
              <div className="border-t border-white/10 pt-4">
                <div className="mb-2 text-sm font-semibold text-slate-200">{t('uploadSecurity')}</div>
                <div className="space-y-3">
                  <Field label={t('allowedExtensions')}>
                    <textarea rows={3} className="glass-input font-mono text-xs" placeholder="jpg,png,mp4,pdf,docx,zip" value={data.allowedExtensions || ''} onChange={(e) => set('allowedExtensions', e.target.value)} />
                  </Field>
                  <ToggleField label={t('enableMagicCheck')} checked={!!data.enableMagicCheck} onChange={(v) => set('enableMagicCheck', v)} />
                </div>
              </div>

              {/* IP 封禁 */}
              <div className="border-t border-white/10 pt-4">
                <div className="mb-2 text-sm font-semibold text-slate-200">{t('ipBanning')}</div>
                <div className="space-y-3">
                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <Field label={t('autoBanThreshold')}>
                      <input type="number" className="glass-input" value={data.autoBanThreshold || 10} onChange={(e) => set('autoBanThreshold', Number(e.target.value))} />
                    </Field>
                    <Field label={t('banDuration')}>
                      <input type="number" className="glass-input" value={data.banDuration || 24} onChange={(e) => set('banDuration', Number(e.target.value))} />
                    </Field>
                  </div>
                  <Field label={t('manualIpBlacklist')}>
                    <textarea rows={4} className="glass-input font-mono text-xs" placeholder={'192.168.1.100\n10.0.0.0/24'} value={data.manualIpBlacklist || ''} onChange={(e) => set('manualIpBlacklist', e.target.value)} />
                  </Field>
                </div>
              </div>

              {/* 回收站 & 文件版本 */}
              <div className="border-t border-white/10 pt-4">
                <div className="mb-2 text-sm font-semibold text-slate-200">{t('trashRetention')} / {t('maxVersions')}</div>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <Field label={t('trashRetention')}>
                    <input type="number" className="glass-input" value={data.trashRetention ?? 30} onChange={(e) => set('trashRetention', Number(e.target.value))} />
                  </Field>
                  <Field label={t('maxVersions')}>
                    <input type="number" className="glass-input" value={data.maxVersions ?? 10} onChange={(e) => set('maxVersions', Number(e.target.value))} />
                  </Field>
                </div>
              </div>

              <div className="border-t border-white/10 pt-4">
                <div className="mb-2 text-sm font-semibold text-slate-200">分享安全设置</div>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <label className="flex items-center justify-between rounded-xl bg-white/5 px-4 py-3">
                    <div>
                      <div className="text-sm">share.default_with_pwd</div>
                      <div className="text-[11px] text-slate-400">新建分享时默认启用访问密码</div>
                    </div>
                    <input
                      type="checkbox"
                      className="h-4 w-4 accent-nebula-500"
                      checked={!!data['share.default_with_pwd']}
                      onChange={(e) => set('share.default_with_pwd', e.target.checked)}
                    />
                  </label>
                  <label className="flex items-center justify-between rounded-xl bg-white/5 px-4 py-3">
                    <div>
                      <div className="text-sm">share.default_with_extract</div>
                      <div className="text-[11px] text-slate-400">新建分享时默认启用 4 位提取码</div>
                    </div>
                    <input
                      type="checkbox"
                      className="h-4 w-4 accent-nebula-500"
                      checked={data['share.default_with_extract'] !== false}
                      onChange={(e) => set('share.default_with_extract', e.target.checked)}
                    />
                  </label>
                </div>
              </div>
            </div>
          )}

          {tab === 'storage' && (
            <div className="space-y-4">
              <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                <Field label={t('ns_admin.maxFileSize')}>
                  <input
                    type="number"
                    className="glass-input"
                    value={data.maxFileSize || 0}
                    onChange={(e) => set('maxFileSize', Number(e.target.value))}
                  />
                </Field>
                <Field label={t('ns_admin.uploadDir')}>
                  <input className="glass-input" value={data.uploadDir || ''} onChange={(e) => set('uploadDir', e.target.value)} />
                </Field>
                <Field label={t('ns_admin.defaultPolicy')}>
                  <input className="glass-input" value={data.defaultPolicy || ''} onChange={(e) => set('defaultPolicy', e.target.value)} />
                </Field>
                <Field label="并发上传数">
                  <input type="number" className="glass-input" value={data.uploadConcurrency || 5} onChange={(e) => set('uploadConcurrency', Number(e.target.value))} />
                </Field>
              </div>
            </div>
          )}

          {tab === 'webdav' && (
            <div className="space-y-5">
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={!!data.webdavEnabled}
                  onChange={(e) => set('webdavEnabled', e.target.checked)}
                  className="h-4 w-4 accent-nebula-500"
                />
                {t('ns_admin.webdavEnabled')}
              </label>
              <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                <Field label={t('webdavPath')}>
                  <input className="glass-input" value={data.webdavPath || '/dav'} onChange={(e) => set('webdavPath', e.target.value)} />
                </Field>
                <Field label="并发数">
                  <input
                    type="number"
                    className="glass-input"
                    value={data.webdavConcurrency || 0}
                    onChange={(e) => set('webdavConcurrency', Number(e.target.value))}
                  />
                </Field>
              </div>
              <div>
                <div className="mb-2 flex items-center justify-between">
                  <div className="text-xs font-medium text-slate-300">{t('webdavMethods')}</div>
                </div>
                <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
                  {WEBDAV_METHODS.map((m) => {
                    const checked = methodsList.includes(m)
                    return (
                      <label
                        key={m}
                        className={`flex cursor-pointer items-center gap-2 rounded-xl px-3 py-2 text-xs transition ${
                          checked ? 'bg-nebula-500/20 text-white' : 'bg-white/5 hover:bg-white/10 text-slate-200'
                        } border border-white/10`}
                      >
                        {checked ? <CheckSquare className="h-3.5 w-3.5 text-cyan-glow" /> : <Square className="h-3.5 w-3.5 text-slate-400" />}
                        <span className="font-mono">{m}</span>
                      </label>
                    )
                  })}
                </div>
              </div>
              <a
                href="https://www.rfc-editor.org/rfc/rfc4918"
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1.5 text-xs text-cyan-glow hover:text-cyan-200"
              >
                <HelpCircle className="h-3.5 w-3.5" />
                {t('webdavHelp')}
                <ExternalLink className="h-3 w-3" />
              </a>
            </div>
          )}

          {tab === 'appearance' && (
            <div className="space-y-6">
              <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                <Field label={t('siteName')}>
                  <input className="glass-input" value={data['brand.name'] || ''} onChange={(e) => set('brand.name', e.target.value)} placeholder="NebulaDrive" />
                </Field>
                <Field label={t('logoUpload')}>
                  <div className="space-y-2">
                    <div className="flex gap-3">
                      <div className="glass flex h-20 w-20 items-center justify-center rounded-xl border border-dashed border-white/20 overflow-hidden">
                        {data['brand.logo_data'] ? (
                          <img src={data['brand.logo_data']} alt="Logo preview" className="h-full max-w-full object-contain" />
                        ) : data['brand.logo'] ? (
                          <ImageIcon className="h-8 w-8 text-slate-400" />
                        ) : (
                          <Upload className="h-8 w-8 text-slate-400" />
                        )}
                      </div>
                      <div className="flex flex-1 flex-col justify-center">
                        <button
                          type="button"
                          onClick={() => logoInputRef.current?.click()}
                          disabled={logoUploading}
                          className="btn-ghost w-fit"
                        >
                          {logoUploading ? (
                            <Loader2 className="h-4 w-4 animate-spin" />
                          ) : (
                            <Upload className="h-4 w-4" />
                          )}
                          {t('logoUpload')}
                        </button>
                        <input
                          ref={logoInputRef}
                          type="file"
                          accept="image/*"
                          className="hidden"
                          onChange={(e) => onLogoPick(e.target.files?.[0])}
                        />
                      </div>
                    </div>
                    {logoMsg && (
                      <div
                        className={`flex items-center gap-2 rounded-lg px-3 py-1.5 text-xs ${
                          logoMsg.type === 'ok'
                            ? 'border border-emerald-400/30 bg-emerald-500/10 text-emerald-300'
                            : 'border border-rose-400/30 bg-rose-500/10 text-rose-300'
                        }`}
                      >
                        {logoMsg.type === 'ok' ? (
                          <Check className="h-3.5 w-3.5" />
                        ) : (
                          <AlertCircle className="h-3.5 w-3.5" />
                        )}
                        {logoMsg.text}
                      </div>
                    )}
                  </div>
                </Field>
              </div>

              <Field label={t('themeColor')}>
                <div className="mb-2 text-xs text-slate-400">{t('presetColors')}</div>
                <div className="mb-4 flex flex-wrap gap-2">
                  {PRESET_COLORS.map((c) => (
                    <button
                      key={c.name}
                      type="button"
                      onClick={() => set('brand.theme_color', c.value)}
                      className={`h-8 w-8 rounded-full transition ${
                        data['brand.theme_color'] === c.value
                          ? 'ring-2 ring-white ring-offset-2 ring-offset-black/50'
                          : 'hover:scale-110'
                      }`}
                      style={{ backgroundColor: c.value }}
                      title={c.name}
                    />
                  ))}
                </div>
                <div className="mb-2 text-xs text-slate-400">{t('customColor')}</div>
                <input
                  type="color"
                  value={data['brand.theme_color'] || '#a855f7'}
                  onChange={(e) => set('brand.theme_color', e.target.value)}
                  className="h-10 w-full rounded-lg border border-white/20 bg-white/5 p-0"
                />
              </Field>

              <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                <Field label={t('icpNumber')}>
                  <input
                    className="glass-input"
                    value={data['site.icp'] || ''}
                    onChange={(e) => set('site.icp', e.target.value)}
                    placeholder="京ICP备12345678号"
                  />
                </Field>
                <Field label={t('footerText')}>
                  <input
                    className="glass-input"
                    value={data['site.footer_text'] || ''}
                    onChange={(e) => set('site.footer_text', e.target.value)}
                    placeholder="© 2023 NebulaDrive. All rights reserved."
                  />
                </Field>
              </div>

              <Field label="默认主题">
                <select
                  className="glass-input"
                  value={data.defaultTheme || 'auto'}
                  onChange={(e) => set('defaultTheme', e.target.value)}
                >
                  <option value="light">{t('themeLight')}</option>
                  <option value="dark">{t('themeDark')}</option>
                  <option value="auto">{t('themeAuto')}</option>
                </select>
              </Field>

              <Field label="自定义 CSS" className="md:col-span-2">
                <textarea rows={4} className="glass-input font-mono text-xs" value={data.customCSS || ''} onChange={(e) => set('customCSS', e.target.value)} />
              </Field>
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" className="h-4 w-4 accent-nebula-500" checked={!!data.showPoweredBy} onChange={(e) => set('showPoweredBy', e.target.checked)} />
                页脚显示 "Powered by NebulaDrive"
              </label>
            </div>
          )}

          {tab === 'plugins' && (
            <div className="space-y-4">
              <Field label={t('pluginStoreURL')}>
                <input
                  className="glass-input"
                  value={data['plugin.store.url'] || data.pluginStoreURL || ''}
                  onChange={(e) => set('plugin.store.url', e.target.value)}
                  placeholder="https://plugins.example.com/com.json"
                />
              </Field>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <label className="flex items-center gap-2 text-sm">
                  <input type="checkbox" className="h-4 w-4 accent-nebula-500" checked={!!data.pluginAutoUpdate} onChange={(e) => set('pluginAutoUpdate', e.target.checked)} />
                  插件自动更新
                </label>
                <label className="flex items-center gap-2 text-sm">
                  <input type="checkbox" className="h-4 w-4 accent-nebula-500" checked={!!data.pluginAllowInstall} onChange={(e) => set('pluginAllowInstall', e.target.checked)} />
                  允许在线安装插件
                </label>
              </div>
            </div>
          )}

          <div className="mt-8 flex justify-end border-t border-white/10 pt-5">
            <button type="button" onClick={save} disabled={saving} className="btn-primary">
              {saving && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('ns_admin.save')}
            </button>
          </div>
        </section>
      </div>
    </div>
  )
}

function Field({ label, children, className = '' }: { label: string; children: React.ReactNode; className?: string }) {
  return (
    <label className={`block ${className}`}>
      <div className="mb-1 text-xs text-slate-400">{label}</div>
      {children}
    </label>
  )
}

function ToggleField({ label, checked, onChange }: { label: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex cursor-pointer items-center justify-between rounded-xl bg-white/5 px-4 py-3">
      <span className="text-sm text-slate-200">{label}</span>
      <button
        type="button"
        onClick={() => onChange(!checked)}
        className={`relative h-6 w-11 shrink-0 rounded-full transition ${checked ? 'bg-nebula-500' : 'bg-white/15'}`}
      >
        <span
          className={`absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition-all ${checked ? 'left-[22px]' : 'left-0.5'}`}
        />
      </button>
    </label>
  )
}
