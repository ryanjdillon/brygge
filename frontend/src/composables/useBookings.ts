import { computed, type Ref } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import type { components } from '@/types/api'

export type Booking = components['schemas']['Booking']
export type AggregateDay = components['schemas']['DayAvailability']
export type TodayAvailability = components['schemas']['TodayAvailability']
export type HoistSlot = components['schemas']['HoistSlot']
export type HoistSlotsResponse = components['schemas']['HoistSlotsResponse']
export type CreateBookingRequest = components['schemas']['CreateBookingRequest']

export interface BoatDimensions {
  length: number | null
  beam: number | null
  draft: number | null
}

export function useAggregateAvailability(
  type: Ref<string>,
  start: Ref<string>,
  end: Ref<string>,
  dimensions?: Ref<BoatDimensions | undefined>,
) {
  const client = useApiClient()

  return useQuery({
    queryKey: ['availability', type, start, end, dimensions],
    queryFn: async () => {
      const dims = dimensions?.value
      const query: Record<string, string> = {
        type: type.value,
        start: start.value,
        end: end.value,
      }
      if (dims?.length && dims?.beam && dims?.draft) {
        query.length = String(dims.length)
        query.beam = String(dims.beam)
        query.draft = String(dims.draft)
      }
      return unwrap(await client.GET('/api/v1/bookings/availability', {
        params: { query: query as any },
      }))
    },
    enabled: computed(() => !!type.value && !!start.value && !!end.value),
    staleTime: 60 * 1000,
  })
}

export function useTodayAvailability(type: string) {
  const client = useApiClient()

  return useQuery({
    queryKey: ['availability-today', type],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/bookings/availability/today', {
        params: { query: { type } },
      })),
    staleTime: 60 * 1000,
  })
}

export function useHoistSlots(date: Ref<string>) {
  const client = useApiClient()

  return useQuery({
    queryKey: ['hoist-slots', date],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/bookings/hoist/slots', {
        params: { query: { date: date.value } },
      })),
    enabled: computed(() => !!date.value),
    staleTime: 30 * 1000,
  })
}

export function useMyBookings(status?: string) {
  const client = useApiClient()

  return useQuery({
    queryKey: ['my-bookings', status ?? 'all'],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/bookings/me')),
    staleTime: 30 * 1000,
  })
}

export function useCreateBooking() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (req: CreateBookingRequest) =>
      unwrap(await client.POST('/api/v1/bookings', { body: req as any })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['availability'] })
      queryClient.invalidateQueries({ queryKey: ['availability-today'] })
      queryClient.invalidateQueries({ queryKey: ['my-bookings'] })
      queryClient.invalidateQueries({ queryKey: ['hoist-slots'] })
    },
  })
}

export function useCancelBooking() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (bookingId: string) =>
      unwrap(await client.POST('/api/v1/bookings/{bookingID}/cancel', {
        params: { path: { bookingID: bookingId } },
      })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['availability'] })
      queryClient.invalidateQueries({ queryKey: ['availability-today'] })
      queryClient.invalidateQueries({ queryKey: ['my-bookings'] })
    },
  })
}
