import { useQuery } from '@tanstack/react-query'
import { getJSON } from '../api/client'
import type { RecordingSegment } from '../api/types'

export function useRecordings(path: string | null) {
  return useQuery({
    queryKey: ['recordings', path],
    queryFn: () =>
      getJSON<RecordingSegment[]>(
        `/playback/list?path=${encodeURIComponent(path!)}`,
      ),
    enabled: path !== null,
    refetchInterval: 15000,
  })
}

export function segmentURL(path: string, seg: RecordingSegment): string {
  const params = new URLSearchParams({
    path,
    start: seg.start,
    duration: String(seg.duration),
    format: 'mp4',
  })
  return `/playback/get?${params}`
}
