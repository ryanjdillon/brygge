import { computed, type Ref } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import type { components } from '@/types/api'

export type CalendarEvent = components['schemas']['CalendarEvent']
export type CreateEventPayload = components['schemas']['CreateEventRequest']
export type UpdateEventPayload = Partial<CreateEventPayload>

export function useEvents(filters?: { start?: Ref<string>; end?: Ref<string>; tag?: Ref<string> }) {
  const client = useApiClient()

  const queryKey = computed(() => {
    const key: (string | undefined)[] = ['events']
    if (filters?.start?.value) key.push(filters.start.value)
    if (filters?.end?.value) key.push(filters.end.value)
    if (filters?.tag?.value) key.push(filters.tag.value)
    return key
  })

  return useQuery({
    queryKey,
    queryFn: async () => {
      const query: Record<string, string> = {}
      if (filters?.start?.value) query.start = filters.start.value
      if (filters?.end?.value) query.end = filters.end.value
      if (filters?.tag?.value) query.tag = filters.tag.value
      return unwrap(await client.GET('/api/v1/calendar', {
        params: { query } as any,
      }))
    },
    staleTime: 2 * 60 * 1000,
  })
}

export function useCreateEvent() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (payload: CreateEventPayload) =>
      unwrap(await client.POST('/api/v1/calendar', { body: payload as any })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['events'] })
    },
  })
}

export function useUpdateEvent() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ id, ...payload }: UpdateEventPayload & { id: string }) =>
      unwrap(await client.PUT('/api/v1/calendar/{eventID}', {
        params: { path: { eventID: id } },
        body: payload as any,
      })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['events'] })
    },
  })
}

export function useDeleteEvent() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (id: string) =>
      unwrap(await client.DELETE('/api/v1/calendar/{eventID}', {
        params: { path: { eventID: id } },
      })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['events'] })
    },
  })
}
