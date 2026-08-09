import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, Music } from 'lucide-react'
import Plyr from 'plyr'
import 'plyr/dist/plyr.css'

interface AudioPlayerProps {
  src: string
  mimeType?: string
}

export default function AudioPlayer({ src, mimeType }: AudioPlayerProps) {
  const { t } = useTranslation()
  const audioRef = useRef<HTMLAudioElement>(null)
  const plyrRef = useRef<Plyr | null>(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  useEffect(() => {
    const audio = audioRef.current
    if (!audio) return
    setLoading(true)
    setErr('')
    let disposed = false

    try {
      audio.src = src
      const onReady = () => { if (!disposed) setLoading(false) }
      audio.addEventListener('loadedmetadata', onReady, { once: true })
      audio.addEventListener('error', () => { if (!disposed) { setErr('audio load error'); setLoading(false) } }, { once: true })

      const player = new Plyr(audio, {
        speed: { selected: 1, options: [0.5, 0.75, 1, 1.25, 1.5, 2] },
        controls: ['play', 'progress', 'current-time', 'mute', 'volume', 'settings'],
        settings: ['speed'],
        tooltips: { controls: true, seek: true },
        keyboard: { focused: true, global: false },
      })
      plyrRef.current = player
    } catch (e: any) {
      if (!disposed) { setErr(e?.message || 'init error'); setLoading(false) }
    }

    return () => {
      disposed = true
      try { plyrRef.current?.destroy() } catch { /* ignore */ }
      plyrRef.current = null
    }
  }, [src, mimeType])

  return (
    <div className="glass-strong flex flex-col items-center justify-center gap-4 rounded-2xl p-6">
      <div className="flex items-center gap-2 text-xs text-slate-300">
        <Music className="h-4 w-4 text-amber-300" />
        <span>{t('audioPlayer')}</span>
      </div>
      <div className="grid h-24 w-24 place-items-center rounded-full bg-gradient-to-br from-amber-500/30 to-pink-500/20 shadow-inner-glow">
        {loading ? <Loader2 className="h-7 w-7 animate-spin text-amber-300" /> : <Music className="h-10 w-10 text-amber-200" />}
      </div>
      <div className="w-full max-w-md">
        {err && <div className="mb-2 text-center text-xs text-rose-300">{err}</div>}
        <audio ref={audioRef} className="plyr-audio w-full" />
      </div>
    </div>
  )
}
