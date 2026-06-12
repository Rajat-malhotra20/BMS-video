import { useQuery } from '@tanstack/react-query'
import { getJSON } from '../api/client'
import type { FleetSummary } from '../api/types'

export const FLEET_POLL_MS = 5000

export function useFleet() {
  return useQuery({
    queryKey: ['fleet'],
    queryFn: () => getJSON<FleetSummary>('/api/fleet'),
    refetchInterval: FLEET_POLL_MS,
    staleTime: FLEET_POLL_MS,
    placeholderData: prev => prev, // keep last data on refetch error
  })
}
