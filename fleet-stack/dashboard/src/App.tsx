import { useState } from 'react'
import BusDetail from './components/BusDetail'
import FleetList from './components/FleetList'
import Header from './components/Header'
import { useFleet } from './hooks/useFleet'

export default function App() {
  const { data: fleet, isError } = useFleet()
  const [selectedId, setSelectedId] = useState<string | null>(null)

  return (
    <div className="flex h-full flex-col">
      <Header fleet={fleet} isStale={isError} />
      <div className="flex min-h-0 flex-1">
        <aside className="w-72 shrink-0 border-r border-edge bg-surface-0">
          <FleetList
            buses={fleet?.buses ?? []}
            selectedId={selectedId}
            onSelect={setSelectedId}
          />
        </aside>
        <main className="min-w-0 flex-1">
          {selectedId ? (
            <BusDetail key={selectedId} busId={selectedId} />
          ) : (
            <div className="grid h-full place-items-center text-ink-dim">
              <div className="text-center">
                <div className="mb-2 text-4xl">🛰</div>
                <div className="text-sm">
                  Select a bus to view its cameras
                </div>
              </div>
            </div>
          )}
        </main>
      </div>
    </div>
  )
}
