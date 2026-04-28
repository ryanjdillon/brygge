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
  boat_id?: string
  boat_name?: string
  boat_length_m?: number
  boat_beam_m?: number
}

export interface FingerProperties {
  kind: 'finger'
  position: number
}

export type SlipFeature = Feature<Point, SlipProperties>
export type FingerFeature = Feature<LineString, FingerProperties>

export interface HarborLayoutResponse {
  type: 'FeatureCollection'
  mode: HarborMode
  features: Array<SlipFeature | FingerFeature>
}

export interface HarborLayoutPutPayload {
  type: 'FeatureCollection'
  features: Array<Partial<SlipFeature> | Partial<FingerFeature>>
  deleted_finger_ids?: string[]
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
