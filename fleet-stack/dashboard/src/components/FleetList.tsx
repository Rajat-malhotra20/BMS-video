import { useVirtualizer } from '@tanstack/react-virtual'
import { memo, useMemo, useRef, useState } from 'react'
import type { FleetBus } from '../api/types'
import { busStatus, CAMS_PER_BUS, filterBuses, type StatusFilter } from '../lib/fleet'

const STATUS_DOT: Record<string, string> = {
  online: 'bg-live',
  partial: 'bg-warn',
  offline: 'bg-down',
}

const FILTERS: { key: StatusFilter; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'online', label: 'Online' },
  { key: 'partial', label: 'Partial' },
  { key: 'offline', label: 'Offline' },
]

const BusRow = memo(function BusRow({
  bus, selected, onSelect,
}: { bus: FleetBus; selected: boolean; onSelect: (id: string) => void }) {
  const status = busStatus(bus)
  return (
    <button
      onClick={() => onSelect(bus.id)}
      className={`flex w-full items-center gap-3 rounded-md px-3 py-2 text-left text-sm transition-colors ${
        selected ? 'bg-surface-2 ring-1 ring-accent' : 'hover:bg-surface-1'
      }`}
    >
      <span className={`h-2.5 w-2.5 shrink-0 rounded-full ${STATUS_DOT[status]}`} />
      <span className="flex-1 truncate font-medium">bus_{bus.id}</span>
      <span className="text-xs text-ink-dim">{bus.cams.length}/{CAMS_PER_BUS}</span>
    </button>
  )
})

interface Props {
  buses: FleetBus[]
  selectedId: string | null
  onSelect: (id: string) => void
}

export default function FleetList({ buses, selectedId, onSelect }: Props) {
  const [search, setSearch] = useState('')
  const [filter, setFilter] = useState<StatusFilter>('all')
  const parentRef = useRef<HTMLDivElement>(null)

  const visible = useMemo(
    () => filterBuses(buses, search, filter),
    [buses, search, filter],
  )

  const virtualizer = useVirtualizer({
    count: visible.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 40,
    overscan: 10,
  })

  return (
    <div className="flex h-full flex-col gap-2 p-3">
      <input
        value={search}
        onChange={e => setSearch(e.target.value)}
        placeholder="Search bus id…"
        className="rounded-md border border-edge bg-surface-1 px-3 py-2 text-sm outline-none placeholder:text-ink-dim focus:border-accent"
      />
      <div className="flex gap-1">
        {FILTERS.map(f => (
          <button
            key={f.key}
            onClick={() => setFilter(f.key)}
            className={`rounded-full px-3 py-1 text-xs transition-colors ${
              filter === f.key
                ? 'bg-accent/20 text-accent'
                : 'text-ink-dim hover:bg-surface-1'
            }`}
          >
            {f.label}
          </button>
        ))}
      </div>
      <div className="text-xs text-ink-dim">{visible.length} buses</div>
      <div ref={parentRef} className="min-h-0 flex-1 overflow-y-auto">
        <div style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
          {virtualizer.getVirtualItems().map(row => {
            const bus = visible[row.index]
            return (
              <div
                key={bus.id}
                style={{
                  position: 'absolute',
                  top: 0,
                  left: 0,
                  width: '100%',
                  transform: `translateY(${row.start}px)`,
                }}
              >
                <BusRow bus={bus} selected={bus.id === selectedId} onSelect={onSelect} />
              </div>
            )
          })}
        </div>
        {visible.length === 0 && (
          <div className="p-6 text-center text-sm text-ink-dim">
            No buses match. Waiting for streams…
          </div>
        )}
      </div>
    </div>
  )
}
