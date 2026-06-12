import { useState } from 'react'
import { useRecordings, segmentURL } from '../hooks/useRecordings'

function fmtTime(iso: string): string {
  return new Date(iso).toLocaleTimeString()
}

function CamRecordings({ path, label }: { path: string; label: string }) {
  const { data: segments, isError } = useRecordings(path)
  const [playing, setPlaying] = useState<string | null>(null)

  return (
    <div className="rounded-lg border border-edge bg-surface-1 p-3">
      <div className="mb-2 text-sm font-semibold">{label}</div>
      {isError && <div className="text-xs text-down">recordings unavailable</div>}
      {segments && segments.length === 0 && (
        <div className="text-xs text-ink-dim">no recordings in last 10 min</div>
      )}
      <div className="flex flex-wrap gap-2">
        {segments?.map(seg => {
          const url = segmentURL(path, seg)
          return (
            <div key={seg.start} className="flex items-center gap-1">
              <button
                onClick={() => setPlaying(playing === url ? null : url)}
                className={`rounded px-2 py-1 text-xs transition-colors ${
                  playing === url
                    ? 'bg-accent/20 text-accent'
                    : 'bg-surface-2 text-ink-dim hover:text-ink'
                }`}
              >
                ⏵ {fmtTime(seg.start)} · {Math.round(seg.duration)}s
              </button>
              <a
                href={url}
                download={`${path}_${seg.start}.mp4`}
                className="text-xs text-ink-dim hover:text-accent"
                title="Download"
              >
                ⬇
              </a>
            </div>
          )
        })}
      </div>
      {playing && (
        <video
          key={playing}
          src={playing}
          controls
          autoPlay
          className="mt-3 aspect-video w-full rounded bg-black"
        />
      )}
    </div>
  )
}

export default function RecordingsPanel({ busId, paths }: { busId: string; paths: { cam: number; path: string }[] }) {
  return (
    <div className="flex flex-col gap-3">
      {paths.map(p => (
        <CamRecordings key={p.path} path={p.path} label={`Bus ${busId} — Camera ${p.cam}`} />
      ))}
      {paths.length === 0 && (
        <div className="text-sm text-ink-dim">No cameras known for this bus yet.</div>
      )}
    </div>
  )
}
