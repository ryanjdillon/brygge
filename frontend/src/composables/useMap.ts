import { useQuery } from '@tanstack/vue-query'
import type { components } from '@/types/api'

export type ClubCoordinates = components['schemas']['ClubCoordinatesResponse']
export type MapMarker = components['schemas']['MapMarker']

async function fetchClubCoordinates(): Promise<ClubCoordinates> {
  const res = await fetch('/api/v1/map/coordinates')
  if (!res.ok) throw new Error('Failed to fetch club coordinates')
  return res.json()
}

async function fetchMapMarkers(): Promise<MapMarker[]> {
  const res = await fetch('/api/v1/map/markers')
  if (!res.ok) throw new Error('Failed to fetch map markers')
  return res.json()
}

export function useClubCoordinates() {
  return useQuery({
    queryKey: ['map', 'coordinates'],
    queryFn: fetchClubCoordinates,
    staleTime: 30 * 60 * 1000,
  })
}

export function useMapMarkers() {
  return useQuery({
    queryKey: ['map', 'markers'],
    queryFn: fetchMapMarkers,
    staleTime: 5 * 60 * 1000,
  })
}
