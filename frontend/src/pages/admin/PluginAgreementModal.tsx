import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  AlertTriangle, Check, ChevronDown, Loader2, ScrollText, ShieldAlert, X,
} from 'lucide-react'
import { api, type Agreement, type AgreementRecord } from '../../api/client'

interface Props {
  open: boolean
  agreement: Agreement | null
  onAccepted: (rec: AgreementRecord) => void
  onClose: () => void
}

/**
 * 插件协议弹窗。
 *
 * 设计要点：
 * - **必须滚动到底**才能勾选同意。协议是要用户真读的东西，
 *   一打开就能点同意等于没要求同意。
 * - 勾选后仍需点「同意并继续」，避免误触。
 * - 协议版本升级时后端会返回新的 version，用户重新走一遍即可。
 */
export default function PluginAgreementModal({ open, agreement, onAccepted, onClose }: Props) {
  const { t } = useTranslation()
  const [scrolledToEnd, setScrolledToEnd] = useState(false)
  const [checked, setChecked] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [err, setErr] = useState('')
  const bodyRef = useRef<HTMLDivElement>(null)

  // 每次打开都重置状态：复用同一个弹窗实例时不能残留上次的勾选
  useEffect(() => {
    if (open) {
      setScrolledToEnd(false)
      setChecked(false)
      setErr('')
      setSubmitting(false)
    }
  }, [open, agreement?.version])

  // 协议很短时内容不满一屏，滚动事件不会触发 —— 直接放行
  useEffect(() => {
    if (!open || !agreement) return
    const el = bodyRef.current
    if (!el) return
    if (el.scrollHeight <= el.clientHeight + 4) {
      setScrolledToEnd(true)
    }
  }, [open, agreement])

  if (!open || !agreement) return null

  const onScroll = () => {
    const el = bodyRef.current
    if (!el) return
    // 留 8px 容差：某些浏览器 scrollTop 是小数，卡在底部差一点就点不了
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 8) {
      setScrolledToEnd(true)
    }
  }

  const accept = async () => {
    if (!checked || submitting) return
    setSubmitting(true)
    setErr('')
    try {
      const res = await api.admin.acceptPluginAgreement(agreement.version)
      if (res?.code === 0 && res.data) {
        onAccepted(res.data)
      } else {
        setErr(res?.message || t('pluginAgreement.acceptFailed'))
      }
    } catch (e: any) {
      setErr(e?.message || t('pluginAgreement.acceptFailed'))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/70 backdrop-blur-sm" onClick={onClose} />
      <div className="glass-strong relative z-10 flex max-h-[88vh] w-full max-w-3xl flex-col overflow-hidden rounded-2xl border border-white/15 shadow-2xl">
        {/* 头部 */}
        <div className="flex items-start gap-3 border-b border-white/10 px-6 py-4">
          <div className="grid h-10 w-10 shrink-0 place-items-center rounded-xl bg-gradient-to-br from-nebula-500/30 to-cyan-glow/10 border border-white/10">
            <ScrollText className="h-5 w-5 text-cyan-glow" />
          </div>
          <div className="min-w-0 flex-1">
            <h2 className="text-base font-semibold text-white">{agreement.title}</h2>
            <p className="mt-0.5 text-xs text-slate-400">
              {t('pluginAgreement.version')} v{agreement.version}
              {agreement.updated && ` · ${agreement.updated}`}
            </p>
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

        {/* 协议正文 */}
        <div
          ref={bodyRef}
          onScroll={onScroll}
          className="min-h-0 flex-1 overflow-y-auto px-6 py-4 text-sm leading-relaxed"
        >
          <p className="mb-4 rounded-lg border border-amber-400/25 bg-amber-500/10 px-3 py-2 text-xs text-amber-200">
            {agreement.required}
          </p>

          {agreement.sections.map((sec, i) => (
            <section key={i} className="mb-5 last:mb-0">
              <h3
                className={`mb-2 flex items-center gap-1.5 text-sm font-semibold ${
                  sec.critical ? 'text-amber-300' : 'text-white'
                }`}
              >
                {sec.critical && <ShieldAlert className="h-4 w-4 shrink-0" />}
                {sec.heading}
              </h3>
              {sec.paras.map((p, j) => (
                <p key={j} className="mb-2 text-slate-300">
                  {/* 协议正文里的 **强调** 渲染成加粗 */}
                  {p.split(/(\*\*[^*]+\*\*)/g).map((seg, k) =>
                    seg.startsWith('**') && seg.endsWith('**') ? (
                      <strong key={k} className="font-semibold text-white">{seg.slice(2, -2)}</strong>
                    ) : (
                      <span key={k}>{seg}</span>
                    ),
                  )}
                </p>
              ))}
              {sec.bullets && sec.bullets.length > 0 && (
                <ul className="mb-2 space-y-1.5">
                  {sec.bullets.map((b, j) => (
                    <li key={j} className="flex gap-2 text-slate-300">
                      <span className="mt-[7px] h-1 w-1 shrink-0 rounded-full bg-slate-500" />
                      <span>
                        {b.split(/(\*\*[^*]+\*\*)/g).map((seg, k) =>
                          seg.startsWith('**') && seg.endsWith('**') ? (
                            <strong key={k} className="font-semibold text-white">{seg.slice(2, -2)}</strong>
                          ) : (
                            <span key={k}>{seg}</span>
                          ),
                        )}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          ))}

          {!scrolledToEnd && (
            <div className="sticky bottom-0 -mx-6 mt-4 flex items-center justify-center gap-1.5 bg-gradient-to-t from-slate-900/95 to-transparent px-6 pb-2 pt-6 text-xs text-slate-400">
              <ChevronDown className="h-4 w-4 animate-bounce" />
              {t('pluginAgreement.scrollToEnd')}
            </div>
          )}
        </div>

        {/* 底部操作 */}
        <div className="border-t border-white/10 px-6 py-4">
          {err && (
            <div className="mb-3 flex items-start gap-2 rounded-lg border border-rose-400/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>{err}</span>
            </div>
          )}

          <label
            className={`flex items-start gap-2.5 text-xs ${
              scrolledToEnd ? 'cursor-pointer text-slate-300' : 'cursor-not-allowed text-slate-500'
            }`}
          >
            <input
              type="checkbox"
              checked={checked}
              disabled={!scrolledToEnd || submitting}
              onChange={(e) => setChecked(e.target.checked)}
              className="mt-0.5 h-4 w-4 shrink-0 rounded border-white/20 bg-white/5 accent-cyan-400 disabled:opacity-40"
            />
            <span>
              {scrolledToEnd
                ? t('pluginAgreement.iHaveRead')
                : t('pluginAgreement.pleaseScrollFirst')}
            </span>
          </label>

          <div className="mt-4 flex items-center justify-end gap-2">
            <button type="button" onClick={onClose} className="btn-ghost" disabled={submitting}>
              {t('common.cancel')}
            </button>
            <button
              type="button"
              onClick={accept}
              disabled={!checked || submitting}
              className="btn-primary"
            >
              {submitting ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <Check className="h-4 w-4" />
              )}
              {t('pluginAgreement.accept')}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
