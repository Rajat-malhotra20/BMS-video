import Hls from 'hls.js'
import { useEffect, useRef, useState } from 'react'
import { startWhep, type WhepSession } from '../lib/whep'

type TileState = 'connecting' | 'live-webrtc' | 'live-hls' | 'offline'

interface Props {
  path: string // e.g. bus_1_2
  label: string
  ready: boolean // from /api/bus poll — stream currently published
  onClick?: () => void
  large?: boolean
}

const FIRST_FRAME_TIMEOUT_MS = 3000

export default function VideoTile({ path, label, ready, onClick, large }: Props) {
  const videoRef = useRef<HTMLVideoElement>(null)
  const [state, setState] = useState<TileState>('connecting')

  useEffect(() => {
    const video = videoRef.current
    if (!video || !ready) {
      setState('offline')
      return
    }

    let cancelled = false
    let whep: WhepSession | null = null
    let hls: Hls | null = null
    let retryTimer: number | undefined
    let attempt = 0
    const abort = new AbortController()

    const cleanupPlayers = () => {
      whep?.close()
      whep = null
      hls?.destroy()
      hls = null
    }

    const scheduleRetry = () => {
      if (cancelled) return
      cleanupPlayers()
      setState('connecting')
      attempt += 1
      const delay = Math.min(1000 * 2 ** attempt, 15000)
      retryTimer = window.setTimeout(start, delay)
    }

    const startHls = () => {
      if (cancelled) return
      cleanupPlayers()
      const src = `/live/${path}/index.m3u8`
      if (video.canPlayType('application/vnd.apple.mpegurl')) {
        video.src = src
        video.play().catch(() => {})
        setState('live-hls')
        return
      }
      hls = new Hls({ lowLatencyMode: true })
      hls.loadSource(src)
      hls.attachMedia(video)
      hls.on(Hls.Events.FRAG_BUFFERED, () => !cancelled && setState('live-hls'))
      hls.on(Hls.Events.ERROR, (_e, data) => {
        if (data.fatal) scheduleRetry()
      })
      video.play().catch(() => {})
    }

    const start = async () => {
      if (cancelled) return
      setState('connecting')
      const frameTimer = window.setTimeout(startHls, FIRST_FRAME_TIMEOUT_MS)
      try {
        whep = await startWhep(path, video, abort.signal)
        video.onplaying = () => {
          window.clearTimeout(frameTimer)
          if (!cancelled) {
            attempt = 0
            setState('live-webrtc')
          }
        }
        whep.pc.onconnectionstatechange = () => {
          const s = whep?.pc.connectionState
          if (s === 'failed' || s === 'disconnected') scheduleRetry()
        }
        video.play().catch(() => {})
      } catch {
        window.clearTimeout(frameTimer)
        startHls()
      }
    }

    start()
    return () => {
      cancelled = true
      abort.abort()
      window.clearTimeout(retryTimer)
      cleanupPlayers()
      video.onplaying = null
    }
  }, [path, ready])

  const badge =
    state === 'live-webrtc' ? '● LIVE' :
    state === 'live-hls' ? '● LIVE (HLS)' :
    state === 'connecting' ? '… connecting' : 'offline'

  const badgeColor =
    state === 'live-webrtc' || state === 'live-hls'
      ? 'text-live' : state === 'connecting' ? 'text-warn' : 'text-down'

  return (
    <div
      onClick={onClick}
      className={`relative overflow-hidden rounded-lg border border-edge bg-surface-2 ${
        onClick ? 'cursor-pointer hover:border-accent' : ''
      } ${large ? 'col-span-3' : ''}`}
    >
      <video
        ref={videoRef}
        muted
        playsInline
        autoPlay
        className="aspect-video w-full bg-black object-contain"
      />
      <div className="absolute left-2 top-2 rounded bg-black/60 px-2 py-0.5 text-xs font-medium">
        {label}
      </div>
      <div className={`absolute right-2 top-2 rounded bg-black/60 px-2 py-0.5 text-xs font-semibold ${badgeColor}`}>
        {badge}
      </div>
      {state === 'offline' && (
        <div className="absolute inset-0 grid place-items-center text-sm text-ink-dim">
          no signal
        </div>
      )}
    </div>
  )
}
