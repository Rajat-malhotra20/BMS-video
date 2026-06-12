import { describe, expect, it } from 'vitest'
import { busStatus, filterBuses, CAMS_PER_BUS } from './fleet'
import type { FleetBus } from '../api/types'

const bus = (id: string, cams: number[]): FleetBus => ({ id, cams, lastSeen: 0 })

describe('busStatus', () => {
  it('all cams live -> online', () => {
    expect(busStatus(bus('1', [1, 2, 3]))).toBe('online')
  })
  it('some cams live -> partial', () => {
    expect(busStatus(bus('1', [1]))).toBe('partial')
  })
  it('no cams live -> offline', () => {
    expect(busStatus(bus('1', []))).toBe('offline')
  })
  it('CAMS_PER_BUS is 3', () => {
    expect(CAMS_PER_BUS).toBe(3)
  })
})

describe('filterBuses', () => {
  const buses = [bus('1', [1, 2, 3]), bus('12', [1]), bus('2', [])]
  it('search matches id substring', () => {
    expect(filterBuses(buses, '1', 'all').map(b => b.id)).toEqual(['1', '12'])
  })
  it('status filter online', () => {
    expect(filterBuses(buses, '', 'online').map(b => b.id)).toEqual(['1'])
  })
  it('status filter offline', () => {
    expect(filterBuses(buses, '', 'offline').map(b => b.id)).toEqual(['2'])
  })
  it('empty search + all returns everything', () => {
    expect(filterBuses(buses, '', 'all')).toHaveLength(3)
  })
})
