import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'

export interface HarborFinger {
  id: string
  label: string
  x1: number
  y1: number
  x2: number
  y2: number
  width_m: number | null
  position: number
}

export interface HarborSlip {
  id: string
  number: string
  section: string
  status: string
  length_m: number | null
  width_m: number | null
  map_x: number | null
  map_y: number | null
  map_rotation: number
  map_finger_id: string | null
  map_side: 'port' | 'starboard' | null
  assignment_type?: 'permanent' | 'seasonal' | null
  occupant_last_name?: string | null
  occupant_id?: string | null
  occupant_name?: string | null
  occupant_email?: string | null
  boat_id?: string | null
  boat_name?: string | null
  boat_length_m?: number | null
  boat_beam_m?: number | null
}

export interface HarborLayout {
  view_box: [number, number, number, number]
  mode: 'public' | 'member' | 'admin'
  fingers: HarborFinger[]
  slips: HarborSlip[]
}

export interface HarborLayoutUpdateFinger {
  id?: string | null
  label: string
  x1: number
  y1: number
  x2: number
  y2: number
  width_m?: number | null
  position: number
  delete?: boolean
}

export interface HarborLayoutUpdateSlip {
  id: string
  map_x: number | null
  map_y: number | null
  map_rotation?: number | null
  map_finger_id?: string | null
  map_side?: 'port' | 'starboard' | null
}

async function fetchLayout(): Promise<HarborLayout> {
  const res = await fetch('/api/v1/harbor/layout', { credentials: 'include' })
  if (!res.ok) throw new Error('Failed to fetch harbor layout')
  return res.json()
}

async function putLayout(payload: {
  fingers: HarborLayoutUpdateFinger[]
  slips: HarborLayoutUpdateSlip[]
}): Promise<void> {
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
