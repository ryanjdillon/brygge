import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'

export interface SlipShare {
  id: string
  slip_assignment_id: string
  club_id: string
  available_from: string
  available_to: string
  notes: string
  status: string
  created_at: string
  updated_at: string
}

export interface SlipShareRebate {
  id: string
  slip_share_id: string
  booking_id: string
  nights_rented: number
  rebate_pct: number
  rental_income: number
  rebate_amount: number
  status: string
  created_at: string
}

export function useMySlipShares() {
  const { fetchApi } = useApi()

  return useQuery({
    queryKey: ['my-slip-shares'],
    queryFn: () => fetchApi<SlipShare[]>('/api/v1/portal/slip-shares'),
    staleTime: 60 * 1000,
  })
}

export function useMyRebates() {
  const { fetchApi } = useApi()

  return useQuery({
    queryKey: ['my-rebates'],
    queryFn: () => fetchApi<SlipShareRebate[]>('/api/v1/portal/slip-shares/rebates'),
    staleTime: 60 * 1000,
  })
}

export function useCreateSlipShare() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (req: { available_from: string; available_to: string; notes: string }) =>
      fetchApi<SlipShare>('/api/v1/portal/slip-shares', {
        method: 'POST',
        body: JSON.stringify(req),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['my-slip-shares'] })
    },
  })
}

export function useDeleteSlipShare() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (shareId: string) =>
      fetchApi(`/api/v1/portal/slip-shares/${shareId}`, { method: 'DELETE' }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['my-slip-shares'] })
    },
  })
}
