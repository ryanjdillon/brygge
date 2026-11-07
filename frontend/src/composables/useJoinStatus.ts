import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'

export const joinStatusKey = ['join-status'] as const

/**
 * Whether the club takes self-service joins (GET /join/status, public), shared
 * by the join page and every "Bli medlem" entry point. A failed request counts
 * as closed, so an entry point never leads to a form that cannot load; the
 * join page still tells a failure apart through isError to offer a retry.
 */
export function useJoinStatus() {
  const client = useApiClient()
  const query = useQuery({
    queryKey: joinStatusKey,
    queryFn: async () => unwrap(await client.GET('/api/v1/join/status')),
    staleTime: 5 * 60 * 1000,
    retry: false,
  })

  const open = computed(() => !query.isError.value && query.data.value?.open === true)
  const price = computed(() => (open.value ? (query.data.value?.price ?? null) : null))
  // The server's year when it gives one, so labels match the dues it bills.
  const year = computed(() => query.data.value?.year || new Date().getFullYear())

  return {
    open,
    price,
    year,
    isLoading: query.isLoading,
    isError: query.isError,
    isFetching: query.isFetching,
    refetch: query.refetch,
  }
}
