import { computed, type Ref } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from './useApi'
import { useFreshTotp } from './useFreshTotp'

const BASE = '/api/v1/admin/broadcasts'

export interface BroadcastSummary {
  id: string
  subject: string
  recipients: string
  source_address: string
  status: string
  total: number
  sent: number
  failed: number
  pending: number
  sent_at: string
  created_at: string
}

export interface BroadcastDelivery {
  id: string
  email: string
  status: string
  attempts: number
  error: string
  sent_at: string | null
}

export interface BroadcastDetail extends BroadcastSummary {
  body_text: string
  body_html: string
  deliveries: BroadcastDelivery[]
}

// useBroadcasts lists the club's bulk sends, newest first. It polls every
// 5s while any broadcast still has pending deliveries, then goes quiet.
export function useBroadcasts() {
  const { fetchApi } = useApi()
  return useQuery({
    queryKey: ['broadcasts'],
    queryFn: () => fetchApi<{ broadcasts: BroadcastSummary[] }>(BASE).then((r) => r.broadcasts ?? []),
    refetchInterval: (query) => {
      const data = query.state.data as BroadcastSummary[] | undefined
      return data?.some((b) => b.pending > 0) ? 5000 : false
    },
  })
}

// useBroadcastDetail fetches one broadcast with its per-recipient rows.
// Polls while it still has pending deliveries.
export function useBroadcastDetail(id: Ref<string | null>) {
  const { fetchApi } = useApi()
  return useQuery({
    queryKey: ['broadcasts', id],
    enabled: computed(() => !!id.value),
    queryFn: () => fetchApi<BroadcastDetail>(`${BASE}/${id.value}`),
    refetchInterval: (query) => {
      const d = query.state.data as BroadcastDetail | undefined
      return d && d.pending > 0 ? 5000 : false
    },
  })
}

// useRetryBroadcast re-queues a broadcast's failed deliveries. The send is
// irreversible, so it goes through the TOTP-aware fetch (the backend gates
// the route on a fresh TOTP).
export function useRetryBroadcast() {
  const qc = useQueryClient()
  const { totpAwareFetch } = useFreshTotp()
  return useMutation({
    mutationFn: async (id: string) => {
      const res = await totpAwareFetch(`${BASE}/${id}/retry`, { method: 'POST' })
      if (!res.ok) throw new Error('retry failed')
      return (await res.json()) as { requeued: number }
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['broadcasts'] })
    },
  })
}
