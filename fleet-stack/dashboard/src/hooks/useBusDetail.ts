import { useQuery } from '@tanstack/react-query'
import { getJSON } from '../api/client'
import type { BusDetail } from '../api/types'

export function useBusDetail(busId: string | null) {
  return useQuery({
    queryKey: ['bus', busId],
    queryFn: () => getJSON<BusDetail>(`/api/bus/${busId}`),
    enabled: busId !== null,
    refetchInterval: 5000,
    placeholderData: prev => prev,
  })
}
