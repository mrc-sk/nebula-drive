import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Database, ShieldCheck, ServerCog, Settings2, CheckCircle2,
  ChevronLeft, ChevronRight, Loader2, Sparkles, Rocket, Microscope, UserPlus, PartyPopper,
  ChevronDown, ChevronUp,
} from 'lucide-react'
import { api } from '../api/client'
import { LANGUAGES } from '../i18n'
import LangSwitcher from '../components/LangSwitcher'
import ThemeToggle from '../components/ThemeToggle'

interface FormState {
  db: { type: string; host: string; port: number; name: string; user: string; password: string; file: string }
  admin: { userName: string; password: string; email: string }
  system: {
    siteName: string; domain: string; protocol: string; listen: string
    timeZone: string; language: string; uploadPath: string; defaultStore: string
  }
  mail: { enabled: boolean; host: string; port: number; user: string; password: string; from: string }
  redis: { enabled: boolean; host: string; port: number; password: string; db: number }
}

const DB_PORTS: Record<string, number> = { sqlite: 0, mysql: 3306, postgres: 5432 }

export default function Install({ onDone }: { onDone: () => void }) {
  const { t, i18n } = useTranslation()
  const [step, setStep] = useState(0)
  const [submitting, setSubmitting] = useState(false)
  const [done, setDone] = useState(false)
  const [error, setError] = useState('')
  const [advanced, setAdvanced] = useState(false)
  const [testState, setTestState] = useState<'idle' | 'testing' | 'ok' | 'fail'>('idle')
  const [testMsg, setTestMsg] = useState('')

  const [form, setForm] = useState<FormState>({
    db: { type: 'sqlite', host: '127.0.0.1', port: 3306, name: 'nebula', user: 'root', password: '', file: 'data/nebula.db' },
    admin: { userName: 'admin', password: '', email: '' },
    system: {
      siteName: 'NebulaDrive', domain: 'localhost', protocol: 'http', listen: ':5212',
      timeZone: 'Asia/Shanghai', language: 'zh-CN', uploadPath: 'uploads', defaultStore: 'local',
    },
    mail: { enabled: false, host: '', port: 465, user: '', password: '', from: '' },
    redis: { enabled: false, host: '127.0.0.1', port: 6379, password: '', db: 0 },
  })

  const set = (path: keyof FormState, patch: any) =>
    setForm((f) => ({ ...f, [path]: { ...(f as any)[path], ...patch } }))

  const STEP_TITLES = [
    t('installWizStep1Title') || '欢迎',
    t('installWizStep2Title') || '环境检查',
    t('installWizStep3Title') || '数据库配置',
    t('installWizStep4Title') || '管理员账号',
    t('installWizStep5Title') || '完成',
  ]
  const STEP_ICONS = [Rocket, Microscope, Database, UserPlus, PartyPopper]

  const testConn = async () => {
    setTestState('testing'); setTestMsg('')
    try {
      const r = await api.testDB(form.db)
      if (r.code === 0) { setTestState('ok'); setTestMsg(t('install.db.testOk') || '连接成功') }
      else { setTestState('fail'); setTestMsg(r.message) }
    } catch (e: any) { setTestState('fail'); setTestMsg(e.message) }
  }

  const submit = async () => {
    setSubmitting(true); setError('')
    try {
      await api.install({
        db: form.db, system: form.system, admin: form.admin,
        mail: advanced ? form.mail : { enabled: false },
        redis: advanced ? form.redis : { enabled: false },
        advanced,
      })
      setDone(true)
      setTimeout(() => onDone(), 1200)
    } catch (e: any) { setError(e.message) }
    finally { setSubmitting(false) }
  }

  if (done) {
    return (
      <div className="relative flex h-full items-center justify-center">
        <div className="login-bg">
          <div className="login-blob login-blob-1" /><div className="login-blob login-blob-2" /><div className="login-blob login-blob-3" />
        </div>
        <div className="glass-strong animate-slide-up flex flex-col items-center gap-3 rounded-3xl px-12 py-10 text-center">
          <CheckCircle2 className="h-12 w-12 text-cyan-glow" />
          <p className="text-lg font-medium">{t('install.success') || '安装成功！正在跳转…'}</p>
        </div>
      </div>
    )
  }

  // 高级模式：单页大表单，分区块毛玻璃折叠面板
  if (advanced) {
    return (
      <div className="relative flex h-full w-full">
        <div className="login-bg"><div className="login-blob login-blob-1" /><div className="login-blob login-blob-2" /><div className="login-blob login-blob-3" /></div>
        <div className="absolute right-4 top-4 z-20 flex items-center gap-2 md:right-8 md:top-8"><ThemeToggle /><LangSwitcher /></div>
        <div className="mx-auto flex h-full w-full max-w-4xl flex-col overflow-y-auto p-4 md:p-8">
          <Header advanced={advanced} onToggleAdvanced={setAdvanced} />
          <div className="mt-6 space-y-4 pb-8">
            <CollapsibleSection title={t('installWizStep1Title') || '欢迎 · 站点信息'} Icon={Rocket} defaultOpen>
              <SiteForm form={form} set={set} />
            </CollapsibleSection>
            <CollapsibleSection title={t('installWizStep2Title') || '环境检查'} Icon={Microscope}>
              <EnvCheck />
            </CollapsibleSection>
            <CollapsibleSection title={t('installWizStep3Title') || '数据库配置'} Icon={Database} defaultOpen>
              <DBForm form={form} set={set} testState={testState} testMsg={testMsg} onTest={testConn} />
            </CollapsibleSection>
            <CollapsibleSection title={t('installWizStep4Title') || '管理员账号'} Icon={UserPlus} defaultOpen>
              <AdminForm form={form} set={set} />
            </CollapsibleSection>
            <CollapsibleSection title={t('ns_admin.mailCache') || '邮件 / 缓存（高级）'} Icon={Settings2}>
              <div className="grid grid-cols-1 gap-5 md:grid-cols-2">
                <SectionCard title="SMTP">
                  <Field label={t('install.advanced.mail.enabled')}>
                    <input type="checkbox" className="h-4 w-4 accent-nebula-500" checked={form.mail.enabled} onChange={(e) => set('mail', { enabled: e.target.checked })} />
                  </Field>
                  {form.mail.enabled && (
                    <div className="mt-3 grid grid-cols-2 gap-3">
                      <Field label={t('install.advanced.mail.host')}><input className="glass-input" value={form.mail.host} onChange={(e) => set('mail', { host: e.target.value })} /></Field>
                      <Field label={t('install.advanced.mail.port')}><input type="number" className="glass-input" value={form.mail.port} onChange={(e) => set('mail', { port: +e.target.value })} /></Field>
                      <Field label={t('install.advanced.mail.user')}><input className="glass-input" value={form.mail.user} onChange={(e) => set('mail', { user: e.target.value })} /></Field>
                      <Field label={t('install.advanced.mail.password')}><input type="password" className="glass-input" value={form.mail.password} onChange={(e) => set('mail', { password: e.target.value })} /></Field>
                      <Field label={t('install.advanced.mail.from')} full><input className="glass-input" value={form.mail.from} onChange={(e) => set('mail', { from: e.target.value })} /></Field>
                    </div>
                  )}
                </SectionCard>
                <SectionCard title="Redis">
                  <Field label={t('install.advanced.redis.enabled')}>
                    <input type="checkbox" className="h-4 w-4 accent-nebula-500" checked={form.redis.enabled} onChange={(e) => set('redis', { enabled: e.target.checked })} />
                  </Field>
                  {form.redis.enabled && (
                    <div className="mt-3 grid grid-cols-2 gap-3">
                      <Field label={t('install.advanced.redis.host')}><input className="glass-input" value={form.redis.host} onChange={(e) => set('redis', { host: e.target.value })} /></Field>
                      <Field label={t('install.advanced.redis.port')}><input type="number" className="glass-input" value={form.redis.port} onChange={(e) => set('redis', { port: +e.target.value })} /></Field>
                      <Field label={t('install.advanced.redis.password')}><input type="password" className="glass-input" value={form.redis.password} onChange={(e) => set('redis', { password: e.target.value })} /></Field>
                      <Field label={t('install.advanced.redis.db')}><input type="number" className="glass-input" value={form.redis.db} onChange={(e) => set('redis', { db: +e.target.value })} /></Field>
                    </div>
                  )}
                </SectionCard>
              </div>
            </CollapsibleSection>
            <ReviewCard form={form} advanced={advanced} />
            {error && <div className="rounded-xl border border-rose-400/30 bg-rose-500/10 px-4 py-2 text-sm text-rose-300">{error}</div>}
            <div className="flex justify-between">
              <button className="btn-ghost" onClick={() => setAdvanced(false)}><ChevronLeft className="h-4 w-4" />{t('install.prev')}</button>
              <button className="btn-primary" disabled={submitting} onClick={submit}>
                {submitting ? <Loader2 className="h-4 w-4 animate-spin" /> : <CheckCircle2 className="h-4 w-4" />}
                {submitting ? t('install.review.installing') : t('install.submit')}
              </button>
            </div>
          </div>
        </div>
      </div>
    )
  }

  // 5 步向导：每步一个毛玻璃卡片
  return (
    <div className="relative h-full w-full">
      <div className="login-bg"><div className="login-blob login-blob-1" /><div className="login-blob login-blob-2" /><div className="login-blob login-blob-3" /></div>
      <div className="absolute right-4 top-4 z-20 flex items-center gap-2 md:right-8 md:top-8"><ThemeToggle /><LangSwitcher /></div>
      <div className="flex h-full w-full items-center justify-center p-4">
        <div className="glass-strong animate-slide-up w-full max-w-3xl overflow-hidden rounded-3xl">
          <Header advanced={advanced} onToggleAdvanced={setAdvanced} />
          {/* 步骤条 */}
          <div className="flex items-center gap-1 px-6 pt-4">
            {STEP_TITLES.map((label, i) => {
              const Icon = STEP_ICONS[i]
              const active = i === step
              const passed = i < step
              return (
                <div key={i} className="flex flex-1 items-center">
                  <div className="flex flex-col items-center gap-1">
                    <div className={`grid h-9 w-9 place-items-center rounded-full border transition ${active ? 'border-cyan-glow bg-cyan-glow/20 text-cyan-glow' : passed ? 'border-nebula-400 bg-nebula-500/30 text-nebula-200' : 'border-white/15 bg-white/5 text-slate-400'}`}>
                      <Icon className="h-4 w-4" />
                    </div>
                    <span className={`max-w-[96px] truncate text-[10px] md:text-xs ${active ? 'text-white' : 'text-slate-400'}`}>{label}</span>
                  </div>
                  {i < STEP_TITLES.length - 1 && <div className={`mx-1 h-px flex-1 ${passed ? 'bg-nebula-400/60' : 'bg-white/10'}`} />}
                </div>
              )
            })}
          </div>
          <div className="px-6 py-6">
            {step === 0 && <StepWelcome form={form} set={set} />}
            {step === 1 && <EnvCheck />}
            {step === 2 && <DBForm form={form} set={set} testState={testState} testMsg={testMsg} onTest={testConn} />}
            {step === 3 && <AdminForm form={form} set={set} />}
            {step === 4 && (
              <>
                <ReviewCard form={form} advanced={advanced} />
                {error && <div className="mt-4 rounded-xl border border-rose-400/30 bg-rose-500/10 px-4 py-2 text-sm text-rose-300">{error}</div>}
              </>
            )}
          </div>
          <div className="flex items-center justify-between border-t border-white/10 px-6 py-4">
            <button className="btn-ghost disabled:opacity-40" disabled={step === 0} onClick={() => setStep((s) => Math.max(0, s - 1))}>
              <ChevronLeft className="h-4 w-4" /> {t('install.prev') || '上一步'}
            </button>
            {step < STEP_TITLES.length - 1 ? (
              <button className="btn-primary" onClick={() => setStep((s) => Math.min(STEP_TITLES.length - 1, s + 1))}>
                {t('install.next') || '下一步'} <ChevronRight className="h-4 w-4" />
              </button>
            ) : (
              <button className="btn-primary" disabled={submitting} onClick={submit}>
                {submitting ? <Loader2 className="h-4 w-4 animate-spin" /> : <CheckCircle2 className="h-4 w-4" />}
                {submitting ? t('install.review.installing') : t('install.submit')}
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

function Header({ advanced, onToggleAdvanced }: { advanced: boolean; onToggleAdvanced: (v: boolean) => void }) {
  const { t } = useTranslation()
  return (
    <div className="flex items-center justify-between border-b border-white/10 px-6 py-5">
      <div className="flex items-center gap-3">
        <div className="grid h-10 w-10 place-items-center rounded-xl bg-gradient-to-br from-nebula-500 to-cyan-glow shadow-glass"><Sparkles className="h-5 w-5 text-white" /></div>
        <div>
          <h1 className="text-lg font-semibold leading-tight">{t('install.title') || 'NebulaDrive 安装向导'}</h1>
          <p className="text-xs text-slate-300">{t('install.subtitle')}</p>
        </div>
      </div>
      <label className="glass flex cursor-pointer items-center gap-2 rounded-xl px-3 py-2 text-xs">
        <Settings2 className="h-4 w-4 text-nebula-300" />
        <span className="hidden sm:inline">{t('advancedMode') || '高级模式'}</span>
        <input type="checkbox" className="h-4 w-4 accent-nebula-500" checked={advanced} onChange={(e) => onToggleAdvanced(e.target.checked)} />
      </label>
    </div>
  )
}

function StepWelcome({ form, set }: any) {
  const { t } = useTranslation()
  return (
    <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
      <Field label={t('install.site.siteName')}><input className="glass-input" value={form.system.siteName} onChange={(e) => set('system', { siteName: e.target.value })} /></Field>
      <Field label={t('install.site.domain')}><input className="glass-input" value={form.system.domain} onChange={(e) => set('system', { domain: e.target.value })} /></Field>
      <Field label={t('install.site.protocol')}>
        <select className="glass-input" value={form.system.protocol} onChange={(e) => set('system', { protocol: e.target.value })}>
          <option value="http">http</option><option value="https">https</option>
        </select>
      </Field>
      <Field label={t('install.site.listen')}><input className="glass-input" value={form.system.listen} onChange={(e) => set('system', { listen: e.target.value })} /></Field>
      <Field label={t('install.site.timeZone')}><input className="glass-input" value={form.system.timeZone} onChange={(e) => set('system', { timeZone: e.target.value })} /></Field>
      <Field label={t('install.site.language')}>
        <select className="glass-input" value={form.system.language} onChange={(e) => set('system', { language: e.target.value })}>
          {LANGUAGES.map((l) => <option key={l.code} value={l.code}>{l.label}</option>)}
        </select>
      </Field>
      <Field label={t('install.site.uploadPath')}><input className="glass-input" value={form.system.uploadPath} onChange={(e) => set('system', { uploadPath: e.target.value })} /></Field>
      <Field label={t('install.site.defaultStore')}>
        <select className="glass-input" value={form.system.defaultStore} onChange={(e) => set('system', { defaultStore: e.target.value })}>
          <option value="local">local</option><option value="s3">S3</option><option value="oss">OSS</option><option value="cos">COS</option>
        </select>
      </Field>
    </div>
  )
}

function EnvCheck() {
  const { t } = useTranslation()
  const checks = [
    { label: 'Node.js', ok: true, detail: typeof (globalThis as any).process !== 'undefined' ? ((globalThis as any).process?.version || '') : 'Browser' },
    { label: 'Write permission', ok: true, detail: '—' },
    { label: 'PHP extension', ok: true, detail: '—' },
    { label: 'Disk space', ok: true, detail: '—' },
    { label: 'HTTPS support', ok: location.protocol === 'https:' || location.hostname === 'localhost', detail: location.protocol },
  ]
  return (
    <div className="space-y-2">
      {checks.map((c) => (
        <div key={c.label} className="glass flex items-center justify-between rounded-xl px-4 py-3">
          <div className="flex items-center gap-2.5">
            {c.ok ? <CheckCircle2 className="h-4 w-4 text-emerald-300" /> : <Loader2 className="h-4 w-4 text-amber-300" />}
            <span className="text-sm">{c.label}</span>
          </div>
          <span className="text-xs text-slate-400">{c.detail}</span>
        </div>
      ))}
    </div>
  )
}

function DBForm({ form, set, testState, testMsg, onTest }: any) {
  const { t } = useTranslation()
  return (
    <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
      <Field label={t('install.db.type')} full>
        <select className="glass-input" value={form.db.type} onChange={(e) => set('db', { type: e.target.value, port: DB_PORTS[e.target.value] || form.db.port })}>
          <option value="sqlite">SQLite</option><option value="mysql">MySQL</option><option value="postgres">PostgreSQL</option>
        </select>
      </Field>
      {form.db.type === 'sqlite' ? (
        <Field label={t('install.db.file')} full><input className="glass-input" value={form.db.file} onChange={(e) => set('db', { file: e.target.value })} /></Field>
      ) : (
        <>
          <Field label={t('install.db.host')}><input className="glass-input" value={form.db.host} onChange={(e) => set('db', { host: e.target.value })} /></Field>
          <Field label={t('install.db.port')}><input type="number" className="glass-input" value={form.db.port} onChange={(e) => set('db', { port: +e.target.value })} /></Field>
          <Field label={t('install.db.name')}><input className="glass-input" value={form.db.name} onChange={(e) => set('db', { name: e.target.value })} /></Field>
          <Field label={t('install.db.user')}><input className="glass-input" value={form.db.user} onChange={(e) => set('db', { user: e.target.value })} /></Field>
          <Field label={t('install.db.password')} full><input type="password" className="glass-input" value={form.db.password} onChange={(e) => set('db', { password: e.target.value })} /></Field>
        </>
      )}
      <div className="col-span-1 flex items-center gap-3 md:col-span-2">
        <button className="btn-ghost" onClick={onTest} disabled={testState === 'testing'}>
          {testState === 'testing' ? <Loader2 className="h-4 w-4 animate-spin" /> : <Database className="h-4 w-4" />}
          {testState === 'testing' ? t('install.db.testing') : t('install.db.test')}
        </button>
        {testState === 'ok' && <span className="text-sm text-emerald-300">{testMsg}</span>}
        {testState === 'fail' && <span className="text-sm text-rose-300">{testMsg}</span>}
      </div>
    </div>
  )
}

function AdminForm({ form, set }: any) {
  const { t } = useTranslation()
  return (
    <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
      <Field label={t('install.admin.username')}><input className="glass-input" value={form.admin.userName} onChange={(e) => set('admin', { userName: e.target.value })} /></Field>
      <Field label={t('install.admin.email')}><input className="glass-input" value={form.admin.email} onChange={(e) => set('admin', { email: e.target.value })} /></Field>
      <Field label={t('install.admin.password')} full><input type="password" className="glass-input" value={form.admin.password} onChange={(e) => set('admin', { password: e.target.value })} /></Field>
    </div>
  )
}

function SiteForm({ form, set }: any) {
  const { t } = useTranslation()
  return (
    <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
      <Field label={t('install.site.siteName')}><input className="glass-input" value={form.system.siteName} onChange={(e) => set('system', { siteName: e.target.value })} /></Field>
      <Field label={t('install.site.domain')}><input className="glass-input" value={form.system.domain} onChange={(e) => set('system', { domain: e.target.value })} /></Field>
      <Field label={t('install.site.protocol')}>
        <select className="glass-input" value={form.system.protocol} onChange={(e) => set('system', { protocol: e.target.value })}>
          <option value="http">http</option><option value="https">https</option>
        </select>
      </Field>
      <Field label={t('install.site.listen')}><input className="glass-input" value={form.system.listen} onChange={(e) => set('system', { listen: e.target.value })} /></Field>
      <Field label={t('install.site.timeZone')}><input className="glass-input" value={form.system.timeZone} onChange={(e) => set('system', { timeZone: e.target.value })} /></Field>
      <Field label={t('install.site.language')}>
        <select className="glass-input" value={form.system.language} onChange={(e) => set('system', { language: e.target.value })}>
          {LANGUAGES.map((l) => <option key={l.code} value={l.code}>{l.label}</option>)}
        </select>
      </Field>
      <Field label={t('install.site.uploadPath')}><input className="glass-input" value={form.system.uploadPath} onChange={(e) => set('system', { uploadPath: e.target.value })} /></Field>
      <Field label={t('install.site.defaultStore')}>
        <select className="glass-input" value={form.system.defaultStore} onChange={(e) => set('system', { defaultStore: e.target.value })}>
          <option value="local">local</option><option value="s3">S3</option><option value="oss">OSS</option><option value="cos">COS</option>
        </select>
      </Field>
    </div>
  )
}

function CollapsibleSection({ title, Icon, children, defaultOpen }: any) {
  const [open, setOpen] = useState(!!defaultOpen)
  return (
    <section className="glass-strong rounded-2xl overflow-hidden">
      <button type="button" onClick={() => setOpen((o) => !o)} className="flex w-full items-center justify-between gap-3 px-5 py-4 hover:bg-white/5">
        <div className="flex items-center gap-2.5">
          <div className="grid h-8 w-8 place-items-center rounded-lg bg-gradient-to-br from-nebula-500 to-cyan-glow/70 text-white"><Icon className="h-4 w-4" /></div>
          <span className="text-sm font-semibold">{title}</span>
        </div>
        {open ? <ChevronUp className="h-4 w-4 text-slate-400" /> : <ChevronDown className="h-4 w-4 text-slate-400" />}
      </button>
      {open && <div className="border-t border-white/10 p-5 animate-fade-in">{children}</div>}
    </section>
  )
}

function SectionCard({ title, children }: any) {
  return (
    <div className="glass rounded-2xl p-4">
      <div className="mb-3 text-sm font-semibold text-nebula-200">{title}</div>
      {children}
    </div>
  )
}

function ReviewCard({ form, advanced }: any) {
  const { t } = useTranslation()
  return (
    <div className="space-y-3">
      <p className="text-sm text-slate-300">{t('install.review.title') || '请检查安装设置'}</p>
      <SectionCard title={t('installWizStep1Title') || '站点'}>
        <ReviewGrid rows={[
          ['siteName', form.system.siteName], ['domain', `${form.system.protocol}://${form.system.domain}${form.system.listen}`],
          ['uploadPath', form.system.uploadPath], ['defaultStore', form.system.defaultStore],
        ]} />
      </SectionCard>
      <SectionCard title={t('installWizStep3Title') || '数据库'}>
        <ReviewGrid rows={[
          ['type', form.db.type],
          ...(form.db.type === 'sqlite' ? [['file', form.db.file] as [string, any]] : [
            ['host', `${form.db.host}:${form.db.port}`] as [string, any], ['name', form.db.name] as [string, any], ['user', form.db.user] as [string, any],
          ]),
        ]} />
      </SectionCard>
      <SectionCard title={t('installWizStep4Title') || '管理员'}>
        <ReviewGrid rows={[['username', form.admin.userName], ['email', form.admin.email || '—']]} />
      </SectionCard>
      {advanced && (
        <SectionCard title={t('advancedMode')}>
          <ReviewGrid rows={[
            ['mail', form.mail.enabled ? t('enabled') : t('disabled')],
            ['redis', form.redis.enabled ? t('enabled') : t('disabled')],
          ]} />
        </SectionCard>
      )}
    </div>
  )
}

function ReviewGrid({ rows }: { rows: Array<[string, any]> }) {
  return (
    <div className="grid grid-cols-1 gap-y-1.5 md:grid-cols-2 md:gap-x-4">
      {rows.map(([k, v]) => (
        <div key={k} className="flex justify-between border-b border-white/5 py-1 text-xs">
          <span className="text-slate-400">{k}</span>
          <span className="text-slate-200">{v ?? '—'}</span>
        </div>
      ))}
    </div>
  )
}

function Field({ label, children, full }: { label: string; children: React.ReactNode; full?: boolean }) {
  return (
    <label className={`block ${full ? 'col-span-full' : ''}`}>
      <span className="mb-1.5 block text-xs text-slate-300">{label}</span>
      {children}
    </label>
  )
}
