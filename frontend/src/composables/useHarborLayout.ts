import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'

export type HarborMode = 'public' | 'member' | 'admin'

export interface Point {
  type: 'Point'
  coordinates: [number, number]
}

export interface LineString {
  type: 'LineString'
  coordinates: Array<[number, number]>
}

export interface Feature<G, P> {
  type: 'Feature'
  id: string
  geometry: G | null
  properties: P
}

export interface FeatureCollection {
  type: 'FeatureCollection'
  features: Array<Feature<unknown, unknown>>
}

export interface SlipProperties {
  kind: 'slip'
  number: string
  section: string
  status: string
  length_m?: number
  width_m?: number
  assignment_type?: 'permanent' | 'seasonal'
  occupant_last_name?: string
  occupant_id?: string
  occupant_name?: string
  occupant_email?: string
  occupant_phone?: string
  boat_id?: string
  boat_name?: string
  boat_length_m?: number
  boat_beam_m?: number
  boat_manufacturer?: string
  boat_model?: string
}

export interface FingerProperties {
  kind: 'finger'
  position: number
  notes?: string
}

export function formatSlipLabel(
  section: string | null | undefined,
  number: string | null | undefined,
): string {
  const s = (section ?? '').trim()
  const n = (number ?? '').trim()
  if (!n) return s
  if (!s) return n
  return n.toLowerCase().startsWith(s.toLowerCase()) ? n : `${s}${n}`
}

export function lineLengthM(coords: Array<[number, number]>): number {
  const R = 6371000
  let total = 0
  for (let i = 1; i < coords.length; i++) {
    const [lng1, lat1] = coords[i - 1]
    const [lng2, lat2] = coords[i]
    const phi1 = (lat1 * Math.PI) / 180
    const phi2 = (lat2 * Math.PI) / 180
    const dphi = ((lat2 - lat1) * Math.PI) / 180
    const dlam = ((lng2 - lng1) * Math.PI) / 180
    const a =
      Math.sin(dphi / 2) ** 2 +
      Math.cos(phi1) * Math.cos(phi2) * Math.sin(dlam / 2) ** 2
    total += 2 * R * Math.atan2(Math.sqrt(a), Math.sqrt(1 - a))
  }
  return total
}

export type SlipFeature = Feature<Point, SlipProperties>
export type FingerFeature = Feature<LineString, FingerProperties>

export interface Dock {
  id?: string
  slug: string
  name: string
  default_lng: number | null
  default_lat: number | null
  default_zoom: number | null
  position: number
}

export interface HarborLayoutResponse {
  type: 'FeatureCollection'
  mode: HarborMode
  features: Array<SlipFeature | FingerFeature>
  docks: Dock[]
}

export interface HarborLayoutPutPayload {
  type: 'FeatureCollection'
  features: Array<Partial<SlipFeature> | Partial<FingerFeature>>
  deleted_finger_ids?: string[]
  docks?: Dock[]
}

async function fetchLayout(): Promise<HarborLayoutResponse> {
  const res = await fetch('/api/v1/harbor/layout', { credentials: 'include' })
  if (!res.ok) throw new Error('Failed to fetch harbor layout')
  return res.json()
}

async function putLayout(payload: HarborLayoutPutPayload): Promise<void> {
  const res = await fetch('/api/v1/harbor/layout', {
    method: 'PUT',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) throw new Error('Failed to save harbor layout')
}

export function useHarborLayout() {
  return useQuery({
    queryKey: ['harbor', 'layout'],
    queryFn: fetchLayout,
    staleTime: 60 * 1000,
  })
}

export function useUpdateHarborLayout() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: putLayout,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['harbor', 'layout'] })
    },
  })
}

export function isSlip(f: SlipFeature | FingerFeature): f is SlipFeature {
  return f.properties?.kind === 'slip'
}

export function isFinger(f: SlipFeature | FingerFeature): f is FingerFeature {
  return f.properties?.kind === 'finger'
}
