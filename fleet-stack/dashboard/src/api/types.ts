export interface FleetBus {
  id: string
  cams: number[] // live camera numbers
  lastSeen: number // unix seconds
}

export interface FleetTotals {
  busesOnline: number
  busesSeen: number
  camsOnline: number
}

export interface FleetSummary {
  buses: FleetBus[]
  totals: FleetTotals
  updatedAt: number
}

export interface CamDetail {
  cam: number
  path: string
  ready: boolean
  tracks: string[]
  bytesReceived: number
  readers: number
}

export interface BusDetail {
  id: string
  cams: CamDetail[]
}

export type BusStatus = 'online' | 'partial' | 'offline'

export interface RecordingSegment {
  start: string // ISO timestamp
  duration: number // seconds
}
