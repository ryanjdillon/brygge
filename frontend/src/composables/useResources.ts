import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import type { components } from '@/types/api'

export type Resource = components['schemas']['BookingResource']

export function useResources(type: string) {
  const { fetchApi } = useApi()

  const query = useQuery({
    queryKey: ['resources', type],
    queryFn: () => fetchApi<Resource[]>(`/api/v1/resources?type=${type}`),
    staleTime: 5 * 60 * 1000,
  })

  const totalCapacity = computed(() =>
    (query.data.value ?? []).reduce((sum: number, r: Resource) => sum + r.capacity, 0),
  )

  return { ...query, totalCapacity }
}
