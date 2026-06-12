import type { BusStatus, FleetBus } from '../api/types'

export const CAMS_PER_BUS = 3

export function busStatus(bus: FleetBus): BusStatus {
  if (bus.cams.length === 0) return 'offline'
  if (bus.cams.length >= CAMS_PER_BUS) return 'online'
  return 'partial'
}

export type StatusFilter = 'all' | BusStatus

export function filterBuses(
  buses: FleetBus[],
  search: string,
  filter: StatusFilter,
): FleetBus[] {
  const q = search.trim().toLowerCase()
  return buses.filter(b => {
    if (q && !b.id.toLowerCase().includes(q)) return false
    if (filter !== 'all' && busStatus(b) !== filter) return false
    return true
  })
}
