import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import type { components } from '@/types/api'

export type SlipShare = components['schemas']['SlipShare']
export function useMySlipShares() {
  const client = useApiClient()

  return useQuery({
    queryKey: ['my-slip-shares'],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/portal/slip-shares')),
    staleTime: 60 * 1000,
  })
}

export function useMyRebates() {
  const client = useApiClient()

  return useQuery({
    queryKey: ['my-rebates'],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/portal/slip-shares/rebates')),
    staleTime: 60 * 1000,
  })
}

export function useCreateSlipShare() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (req: { available_from: string; available_to: string; notes: string }) =>
      unwrap(await client.POST('/api/v1/portal/slip-shares', { body: req as any })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['my-slip-shares'] })
    },
  })
}

export function useDeleteSlipShare() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (shareId: string) =>
      unwrap(await client.DELETE('/api/v1/portal/slip-shares/{shareID}', {
        params: { path: { shareID: shareId } },
      })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['my-slip-shares'] })
    },
  })
}
