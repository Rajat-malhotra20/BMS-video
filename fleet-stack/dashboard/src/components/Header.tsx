import type { FleetSummary } from '../api/types'

interface Props {
  fleet: FleetSummary | undefined
  isStale: boolean
}

export default function Header({ fleet, isStale }: Props) {
  return (
    <header className="flex items-center gap-6 border-b border-edge bg-surface-1 px-5 py-3">
      <div className="flex items-center gap-2 text-base font-bold tracking-widest">
        <span className={isStale ? 'text-down' : 'text-live'}>◉</span> FLEET BMS
      </div>
      <div className="flex gap-6 text-sm text-ink-dim">
        <span>
          buses online{' '}
          <strong className="text-ink">{fleet?.totals.busesOnline ?? '—'}</strong>
          <span className="text-ink-dim">/{fleet?.totals.busesSeen ?? '—'}</span>
        </span>
        <span>
          cams online <strong className="text-ink">{fleet?.totals.camsOnline ?? '—'}</strong>
        </span>
      </div>
      <div className="ml-auto text-xs text-ink-dim">
        {isStale
          ? `⚠ data stale since ${fleet ? new Date(fleet.updatedAt * 1000).toLocaleTimeString() : '—'}`
          : '⟳ 5s'}
      </div>
    </header>
  )
}
