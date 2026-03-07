import { useQuery } from '@tanstack/vue-query'

export interface ClubCoordinates {
  name: string
  latitude: number | null
  longitude: number | null
}

export interface MapMarker {
  id: string
  club_id: string
  marker_type: string
  label: string
  lat: number
  lng: number
  sort_order: number
  created_at: string
}

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
