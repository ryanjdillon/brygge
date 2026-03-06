import { computed, type Ref } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'

export interface CalendarEvent {
  id: string
  club_id: string
  title: string
  description: string
  location: string
  start_time: string
  end_time: string
  tag: string
  is_public: boolean
  created_by: string
  created_at: string
  updated_at: string
}

export interface CreateEventPayload {
  title: string
  description: string
  location: string
  start_time: string
  end_time: string
  tag: string
  is_public: boolean
}

export interface UpdateEventPayload extends Partial<CreateEventPayload> {}

export function useEvents(filters?: { start?: Ref<string>; end?: Ref<string>; tag?: Ref<string> }) {
  const { fetchApi } = useApi()

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
      const params = new URLSearchParams()
      if (filters?.start?.value) params.set('start', filters.start.value)
      if (filters?.end?.value) params.set('end', filters.end.value)
      if (filters?.tag?.value) params.set('tag', filters.tag.value)
      const qs = params.toString()
      return fetchApi<CalendarEvent[]>(`/api/v1/calendar/${qs ? `?${qs}` : ''}`)
    },
    staleTime: 2 * 60 * 1000,
  })
}

export function useEvent(eventId: Ref<string>) {
  const { fetchApi } = useApi()

  return useQuery({
    queryKey: computed(() => ['events', eventId.value]),
    queryFn: () => fetchApi<CalendarEvent>(`/api/v1/calendar/${eventId.value}`),
    enabled: computed(() => !!eventId.value),
  })
}

export function useCreateEvent() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (payload: CreateEventPayload) =>
      fetchApi<CalendarEvent>('/api/v1/calendar/', {
        method: 'POST',
        body: JSON.stringify(payload),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['events'] })
    },
  })
}

export function useUpdateEvent() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ id, ...payload }: UpdateEventPayload & { id: string }) =>
      fetchApi<CalendarEvent>(`/api/v1/calendar/${id}`, {
        method: 'PUT',
        body: JSON.stringify(payload),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['events'] })
    },
  })
}

export function useDeleteEvent() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) =>
      fetchApi(`/api/v1/calendar/${id}`, { method: 'DELETE' }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['events'] })
    },
  })
}
