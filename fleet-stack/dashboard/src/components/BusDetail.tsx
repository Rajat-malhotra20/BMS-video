import { useState } from 'react'
import { useBusDetail } from '../hooks/useBusDetail'
import { CAMS_PER_BUS } from '../lib/fleet'
import RecordingsPanel from './RecordingsPanel'
import VideoTile from './VideoTile'

function fmtBytes(bytes: number): string {
  if (bytes <= 0) return '—'
  const mb = bytes / 1_000_000
  return mb >= 1000 ? `${(mb / 1000).toFixed(1)} GB` : `${mb.toFixed(1)} MB`
}

export default function BusDetail({ busId }: { busId: string }) {
  const { data, isError } = useBusDetail(busId)
  const [tab, setTab] = useState<'live' | 'recordings'>('live')
  const [expanded, setExpanded] = useState<number | null>(null)

  // Always render CAMS_PER_BUS slots so layout is stable.
  const slots = Array.from({ length: CAMS_PER_BUS }, (_, i) => {
    const cam = i + 1
    const detail = data?.cams.find(c => c.cam === cam)
    return { cam, path: detail?.path ?? `bus_${busId}_${cam}`, detail }
  })
  const liveCount = data?.cams.filter(c => c.ready).length ?? 0

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold tracking-wide">BUS_{busId}</h1>
        <span className={`text-sm font-medium ${liveCount > 0 ? 'text-live' : 'text-down'}`}>
          ● {liveCount}/{CAMS_PER_BUS} LIVE
        </span>
      </div>

      <div className="flex gap-1">
        {(['live', 'recordings'] as const).map(t => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`rounded-md px-4 py-1.5 text-sm transition-colors ${
              tab === t ? 'bg-accent/20 text-accent' : 'text-ink-dim hover:bg-surface-1'
            }`}
          >
            {t === 'live' ? '● Live' : '⏪ Last 10 min'}
          </button>
        ))}
      </div>

      {isError && (
        <div className="rounded-md border border-down/40 bg-down/10 p-3 text-sm text-down">
          Bus data unavailable — backend unreachable. Retrying…
        </div>
      )}

      {tab === 'live' && (
        <>
          <div className="grid grid-cols-3 gap-3">
            {expanded !== null && (() => {
              const s = slots[expanded - 1]
              return (
                <VideoTile
                  key={`big-${s.path}`}
                  path={s.path}
                  label={`CAM ${s.cam}`}
                  ready={s.detail?.ready ?? false}
                  large
                  onClick={() => setExpanded(null)}
                />
              )
            })()}
            {slots
              .filter(s => s.cam !== expanded)
              .map(s => (
                <VideoTile
                  key={s.path}
                  path={s.path}
                  label={`CAM ${s.cam}`}
                  ready={s.detail?.ready ?? false}
                  onClick={() => setExpanded(expanded === s.cam ? null : s.cam)}
                />
              ))}
          </div>
          <div className="grid grid-cols-3 gap-3 text-xs text-ink-dim">
            {slots.map(s => (
              <div key={s.path} className="rounded-md border border-edge bg-surface-1 p-2">
                <div className="font-medium text-ink">CAM {s.cam}</div>
                <div>codec: {s.detail?.tracks?.join(', ') || '—'}</div>
                <div>received: {fmtBytes(s.detail?.bytesReceived ?? 0)}</div>
                <div>viewers: {s.detail?.readers ?? 0}</div>
              </div>
            ))}
          </div>
        </>
      )}

      {tab === 'recordings' && (
        <RecordingsPanel
          busId={busId}
          paths={slots.map(s => ({ cam: s.cam, path: s.path }))}
        />
      )}
    </div>
  )
}
