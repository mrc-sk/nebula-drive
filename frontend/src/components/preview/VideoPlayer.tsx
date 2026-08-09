import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, Film } from 'lucide-react'
import Plyr from 'plyr'
import Hls from 'hls.js'
import 'plyr/dist/plyr.css'

interface VideoPlayerProps {
  src: string
  mimeType?: string
}

function isHlsSource(src: string, mimeType?: string): boolean {
  if (mimeType === 'application/vnd.apple.mpegurl' || mimeType === 'application/x-mpegurl') return true
  return /\.m3u8(\?|$)/i.test(src)
}

export default function VideoPlayer({ src, mimeType }: VideoPlayerProps) {
  const { t } = useTranslation()
  const videoRef = useRef<HTMLVideoElement>(null)
  const plyrRef = useRef<Plyr | null>(null)
  const hlsRef = useRef<Hls | null>(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  useEffect(() => {
    const video = videoRef.current
    if (!video) return
    setLoading(true)
    setErr('')
    let disposed = false
    const hlsType = isHlsSource(src, mimeType)

    try {
      if (hlsType && Hls.isSupported()) {
        const hls = new Hls({ enableWorker: true })
        hlsRef.current = hls
        hls.loadSource(src)
        hls.attachMedia(video)
        hls.on(Hls.Events.MANIFEST_PARSED, () => { if (!disposed) setLoading(false) })
        hls.on(Hls.Events.ERROR, (_e, data) => {
          if (data?.fatal && !disposed) { setErr(data.details || 'HLS error'); setLoading(false) }
        })
      } else {
        video.src = src
        const onReady = () => { if (!disposed) setLoading(false) }
        video.addEventListener('loadedmetadata', onReady, { once: true })
        video.addEventListener('error', () => { if (!disposed) { setErr('video load error'); setLoading(false) } }, { once: true })
      }

      const player = new Plyr(video, {
        speed: { selected: 1, options: [0.5, 0.75, 1, 1.25, 1.5, 2] },
        pip: true,
        autopause: true,
        fullscreen: { enabled: true, fallback: true, iosNative: true },
        controls: [
          'play-large', 'play', 'progress', 'current-time', 'mute', 'volume',
          'settings', 'pip', 'airplay', 'fullscreen',
        ],
        settings: ['speed', 'quality'],
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
      try { hlsRef.current?.destroy() } catch { /* ignore */ }
      hlsRef.current = null
    }
  }, [src, mimeType])

  return (
    <div className="glass-strong flex flex-col items-center justify-center gap-3 rounded-2xl p-4">
      <div className="flex items-center gap-2 text-xs text-slate-300">
        <Film className="h-4 w-4 text-rose-300" />
        <span>{t('videoPlayer')}</span>
        {isHlsSource(src, mimeType) && <span className="rounded bg-cyan-glow/20 px-1.5 py-0.5 text-[10px] text-cyan-glow">HLS</span>}
      </div>
      <div className="relative w-full">
        {(loading || err) && (
          <div className="absolute inset-0 z-10 grid place-items-center rounded-xl bg-black/40">
            {err ? (
              <span className="text-xs text-rose-300">{err}</span>
            ) : (
              <Loader2 className="h-7 w-7 animate-spin text-nebula-300" />
            )}
          </div>
        )}
        <video ref={videoRef} playsInline className="max-h-[70vh] w-full rounded-xl" />
      </div>
    </div>
  )
}
