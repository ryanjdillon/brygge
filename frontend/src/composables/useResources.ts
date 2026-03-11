import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import type { components } from '@/types/api'

export type Resource = components['schemas']['BookingResource']

export function useResources(type: string) {
  const client = useApiClient()

  const query = useQuery({
    queryKey: ['resources', type],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/bookings/resources', {
        params: { query: { type } } as any,
      })),
    staleTime: 5 * 60 * 1000,
  })

  const totalCapacity = computed(() =>
    (query.data.value ?? []).reduce((sum: number, r: Resource) => sum + r.capacity, 0),
  )

  return { ...query, totalCapacity }
}
