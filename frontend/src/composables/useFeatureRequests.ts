import { computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import type { components } from '@/types/api'

export type FeatureRequest = components['schemas']['FeatureRequest']

export interface CreateFeatureRequestInput {
  title: string
  description: string
}

export interface VoteInput {
  value: 1 | -1
}

export function useFeatureRequests(statusFilter?: () => string) {
  const client = useApiClient()
  const filter = statusFilter ? computed(() => statusFilter()) : computed(() => '')

  return useQuery({
    queryKey: ['feature-requests', filter],
    queryFn: async () => {
      const query = filter.value ? { status: filter.value } : {}
      return unwrap(await client.GET('/api/v1/feature-requests', {
        params: { query } as any,
      }))
    },
  })
}

export function useFeatureRequest(requestId: () => string) {
  const client = useApiClient()
  const id = computed(() => requestId())

  return useQuery({
    queryKey: ['feature-requests', id],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/feature-requests/{requestID}', {
        params: { path: { requestID: id.value } },
      })),
    enabled: () => !!id.value,
  })
}

export function useCreateFeatureRequest() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (input: CreateFeatureRequestInput) =>
      unwrap(await client.POST('/api/v1/feature-requests', { body: input as any })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['feature-requests'] })
    },
  })
}

export function useVote() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ requestId, value }: { requestId: string; value: 1 | -1 }) =>
      unwrap(await client.POST('/api/v1/feature-requests/{requestID}/vote', {
        params: { path: { requestID: requestId } },
        body: { value } as any,
      })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['feature-requests'] })
    },
  })
}

export function useUpdateFeatureRequestStatus() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ requestId, status }: { requestId: string; status: string }) =>
      unwrap(await client.PUT('/api/v1/feature-requests/{requestID}/status', {
        params: { path: { requestID: requestId } },
        body: { status } as any,
      })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['feature-requests'] })
    },
  })
}

export function usePromoteToTask() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ requestId, projectId }: { requestId: string; projectId: string }) =>
      unwrap(await client.POST('/api/v1/feature-requests/{requestID}/promote', {
        params: { path: { requestID: requestId } },
        body: { project_id: projectId } as any,
      })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['feature-requests'] })
      queryClient.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}
