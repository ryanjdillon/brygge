import { computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'

export interface FeatureRequest {
  id: string
  club_id: string
  title: string
  description: string
  status: 'proposed' | 'reviewing' | 'accepted' | 'rejected' | 'done'
  submitted_by: string
  vote_count: number
  user_vote: number | null
  created_at: string
  updated_at: string
}

export interface CreateFeatureRequestInput {
  title: string
  description: string
}

export interface VoteInput {
  value: 1 | -1
}

export function useFeatureRequests(statusFilter?: () => string) {
  const { fetchApi } = useApi()
  const filter = statusFilter ? computed(() => statusFilter()) : computed(() => '')

  return useQuery({
    queryKey: ['feature-requests', filter],
    queryFn: () => {
      const params = filter.value ? `?status=${filter.value}` : ''
      return fetchApi<FeatureRequest[]>(`/api/v1/feature-requests${params}`)
    },
  })
}

export function useFeatureRequest(requestId: () => string) {
  const { fetchApi } = useApi()
  const id = computed(() => requestId())

  return useQuery({
    queryKey: ['feature-requests', id],
    queryFn: () => fetchApi<FeatureRequest>(`/api/v1/feature-requests/${id.value}`),
    enabled: () => !!id.value,
  })
}

export function useCreateFeatureRequest() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (input: CreateFeatureRequestInput) =>
      fetchApi<FeatureRequest>('/api/v1/feature-requests', {
        method: 'POST',
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['feature-requests'] })
    },
  })
}

export function useVote() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ requestId, value }: { requestId: string; value: 1 | -1 }) =>
      fetchApi<{ vote_count: number; user_vote: number }>(`/api/v1/feature-requests/${requestId}/vote`, {
        method: 'POST',
        body: JSON.stringify({ value }),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['feature-requests'] })
    },
  })
}

export function useUpdateFeatureRequestStatus() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ requestId, status }: { requestId: string; status: string }) =>
      fetchApi(`/api/v1/feature-requests/${requestId}/status`, {
        method: 'PUT',
        body: JSON.stringify({ status }),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['feature-requests'] })
    },
  })
}

export function usePromoteToTask() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ requestId, projectId }: { requestId: string; projectId: string }) =>
      fetchApi(`/api/v1/feature-requests/${requestId}/promote`, {
        method: 'POST',
        body: JSON.stringify({ project_id: projectId }),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['feature-requests'] })
      queryClient.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}
