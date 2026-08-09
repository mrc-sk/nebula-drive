import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, FileText, AlertCircle, Table, Presentation, Image as ImageIcon } from 'lucide-react'
import mammoth from 'mammoth'
import * as XLSX from 'xlsx'
import JSZip from 'jszip'

interface OfficePreviewProps {
  fileUrl: string
  fileExtension: string
  fileName?: string
}

interface PptxSlide {
  index: number
  texts: string[]
  images: { name: string; url: string }[]
}

type Status = 'loading' | 'done' | 'error'

function normalizeExt(ext: string): string {
  return (ext || '').toLowerCase().replace(/^\./, '')
}

export default function OfficePreview({ fileUrl, fileExtension, fileName }: OfficePreviewProps) {
  const { t } = useTranslation()
  const ext = normalizeExt(fileExtension)
  const [status, setStatus] = useState<Status>('loading')
  const [err, setErr] = useState('')
  const [docHtml, setDocHtml] = useState('')
  const [sheetHtml, setSheetHtml] = useState('')
  const [slides, setSlides] = useState<PptxSlide[]>([])
  const [blobUrls, setBlobUrls] = useState<string[]>([])

  useEffect(() => {
    let alive = true
    const createdUrls: string[] = []
    setStatus('loading')
    setErr('')
    setDocHtml('')
    setSheetHtml('')
    setSlides([])
    setBlobUrls([])

    const isOffice = ['docx', 'xlsx', 'xls', 'pptx'].includes(ext)
    if (!isOffice) {
      // pdf 等非 office 直接由调用方处理，这里兜底
      setStatus('done')
      return () => {}
    }

    ;(async () => {
      try {
        const res = await fetch(fileUrl)
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        const buf = await res.arrayBuffer()
        if (!alive) return

        if (ext === 'docx') {
          const result = await mammoth.convertToHtml({ arrayBuffer: buf })
          if (!alive) return
          setDocHtml(result.value || '')
          setStatus('done')
        } else if (ext === 'xlsx' || ext === 'xls') {
          const wb = XLSX.read(buf, { type: 'array' })
          if (!alive) return
          // 合并所有 sheet
          const parts: string[] = []
          wb.SheetNames.forEach((name) => {
            const ws = wb.Sheets[name]
            const html = XLSX.utils.sheet_to_html(ws, { id: `sheet-${name}`, editable: false })
            parts.push(`<h4 class="sheet-name">${escapeHtml(name)}</h4>${html}`)
          })
          setSheetHtml(parts.join(''))
          setStatus('done')
        } else if (ext === 'pptx') {
          const zip = await JSZip.loadAsync(buf)
          if (!alive) return
          const slideRegex = /^ppt\/slides\/slide(\d+)\.xml$/
          const slideKeys = Object.keys(zip.files)
            .filter((k) => slideRegex.test(k))
            .sort((a, b) => {
              const na = parseInt((a.match(slideRegex) || [])[1] || '0', 10)
              const nb = parseInt((b.match(slideRegex) || [])[1] || '0', 10)
              return na - nb
            })

          const mediaRegex = /^ppt\/media\//
          const mediaKeys = Object.keys(zip.files).filter((k) => mediaRegex.test(k) && !zip.files[k].dir)

          const out: PptxSlide[] = []
          for (let i = 0; i < slideKeys.length; i++) {
            const key = slideKeys[i]
            const xml = await zip.files[key].async('string')
            const idx = parseInt((key.match(slideRegex) || [])[1] || String(i + 1), 10)
            const texts = (xml.match(/<a:t>([^<]*)<\/a:t>/g) || []).map((m) =>
              decodeXmlEntities(m.replace(/<\/?a:t>/g, '')),
            )
            // 该 slide 引用的图片关系
            const relKey = key.replace(/^ppt\/slides\//, 'ppt/slides/_rels/').replace(/\.xml$/, '.xml.rels')
            const relsXml = zip.files[relKey] ? await zip.files[relKey].async('string') : ''
            const relTargets = (relsXml.match(/Target="([^"]+)"/g) || [])
              .map((m) => m.replace(/Target="|"$/g, ''))
              .filter((p) => /\.(png|jpe?g|gif|bmp|webp|svg)$/i.test(p))

            const images: { name: string; url: string }[] = []
            for (const rel of relTargets) {
              const mediaPath = `ppt/slides/${rel}`.replace(/\\/g, '/').replace(/\/\.\.\//g, '/')
              const cleaned = mediaPath.replace(/^ppt\/slides\.\.\//, 'ppt/')
              const file = zip.files[cleaned] || zip.files[mediaPath]
              if (file) {
                const blob = await file.async('blob')
                const url = URL.createObjectURL(blob)
                createdUrls.push(url)
                images.push({ name: cleaned, url })
              }
            }
            out.push({ index: idx, texts, images })
          }

          // 若没有 slide 维度图片关系，回退展示所有 media 图片
          const hasAnyImg = out.some((s) => s.images.length > 0)
          if (!hasAnyImg && mediaKeys.length > 0) {
            const fallback: { name: string; url: string }[] = []
            for (const mk of mediaKeys.slice(0, 12)) {
              const blob = await zip.files[mk].async('blob')
              const url = URL.createObjectURL(blob)
              createdUrls.push(url)
              fallback.push({ name: mk, url })
            }
            if (out.length > 0) {
              out[0].images = fallback
            } else if (fallback.length > 0) {
              out.push({ index: 1, texts: [], images: fallback })
            }
          }

          if (!alive) return
          setSlides(out)
          setBlobUrls(createdUrls)
          setStatus('done')
        }
      } catch (e: any) {
        if (!alive) return
        setErr(e?.message || 'parse error')
        setStatus('error')
      }
    })()

    return () => {
      alive = false
      createdUrls.forEach((u) => URL.revokeObjectURL(u))
    }
  }, [fileUrl, ext])

  return (
    <div className="glass-strong flex flex-col rounded-2xl p-4">
      <div className="mb-3 flex items-center gap-2 text-xs text-slate-300">
        {ext === 'docx' ? <FileText className="h-4 w-4 text-sky-300" />
          : ext === 'xlsx' || ext === 'xls' ? <Table className="h-4 w-4 text-emerald-300" />
          : ext === 'pptx' ? <Presentation className="h-4 w-4 text-orange-300" />
          : <FileText className="h-4 w-4 text-slate-300" />}
        <span>{t('officePreview')}</span>
        {fileName && <span className="truncate text-slate-400">· {fileName}</span>}
      </div>

      <div className="min-h-[200px]">
        {status === 'loading' && (
          <div className="flex flex-col items-center justify-center gap-2 py-12 text-slate-300">
            <Loader2 className="h-7 w-7 animate-spin text-nebula-300" />
            <span className="text-xs">{t('loadingDoc')}</span>
          </div>
        )}

        {status === 'error' && (
          <div className="flex items-center gap-2 rounded-xl border border-rose-400/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-300">
            <AlertCircle className="h-4 w-4 shrink-0" />
            <span>{err}</span>
          </div>
        )}

        {status === 'done' && ext === 'docx' && (
          <div
            className="office-doc max-h-[68vh] overflow-auto rounded-xl bg-white/95 p-6 text-sm leading-relaxed text-slate-800"
            // eslint-disable-next-line react/no-danger
            dangerouslySetInnerHTML={{ __html: docHtml || `<p style="color:#94a3b8">${t('unsupportedFormat')}</p>` }}
          />
        )}

        {status === 'done' && (ext === 'xlsx' || ext === 'xls') && (
          <div className="office-sheet max-h-[68vh] overflow-auto rounded-xl bg-white/95 p-4">
            <div
              // eslint-disable-next-line react/no-danger
              dangerouslySetInnerHTML={{ __html: sheetHtml || `<p style="color:#94a3b8">${t('unsupportedFormat')}</p>` }}
            />
          </div>
        )}

        {status === 'done' && ext === 'pptx' && (
          <div className="max-h-[68vh] space-y-4 overflow-auto">
            {slides.length === 0 ? (
              <div className="py-10 text-center text-sm text-slate-400">{t('unsupportedFormat')}</div>
            ) : (
              slides.map((s) => (
                <div key={s.index} className="rounded-xl border border-white/10 bg-white/5 p-4">
                  <div className="mb-2 flex items-center gap-2 text-xs text-slate-300">
                    <Presentation className="h-3.5 w-3.5 text-orange-300" />
                    <span>Slide {s.index}</span>
                  </div>
                  {s.texts.length > 0 ? (
                    <div className="space-y-1 text-sm text-slate-100">
                      {s.texts.filter(Boolean).map((tx, i) => (
                        <p key={i}>{tx}</p>
                      ))}
                    </div>
                  ) : (
                    <p className="text-xs text-slate-400">—</p>
                  )}
                  {s.images.length > 0 && (
                    <div className="mt-3 flex flex-wrap gap-2">
                      {s.images.map((img, i) => (
                        <a key={i} href={img.url} target="_blank" rel="noreferrer" className="block">
                          <img
                            src={img.url}
                            alt={img.name}
                            className="max-h-40 max-w-full rounded-lg border border-white/10"
                          />
                        </a>
                      ))}
                    </div>
                  )}
                </div>
              ))
            )}
            {blobUrls.length > 0 && (
              <div className="flex items-center gap-1 text-[10px] text-slate-500">
                <ImageIcon className="h-3 w-3" /> {blobUrls.length} media
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
}

function decodeXmlEntities(s: string): string {
  return s
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&apos;/g, "'")
    .replace(/&#(\d+);/g, (_m, c) => String.fromCharCode(parseInt(c, 10)))
}
