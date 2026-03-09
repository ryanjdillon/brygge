import { computed, type Ref } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'

export interface Booking {
  id: string
  resource_id: string
  resource_unit_id?: string
  user_id?: string
  club_id: string
  start_date: string
  end_date: string
  status: string
  guest_name?: string
  guest_email?: string
  guest_phone?: string
  payment_id?: string
  boat_length_m?: number
  boat_beam_m?: number
  boat_draft_m?: number
  notes: string
  created_at: string
  updated_at: string
}

export interface AggregateDay {
  date: string
  total_units: number
  available_units: number
}

export interface TodayAvailability {
  available: number
  total: number
}

export interface HoistSlot {
  start: string
  end: string
  available: boolean
  booked_by?: string
}

export interface HoistSlotsResponse {
  date: string
  slot_duration_minutes: number
  slots: HoistSlot[]
}

export interface CreateBookingRequest {
  resource_type: string
  start_date: string
  end_date: string
  boat_length_m?: number
  boat_beam_m?: number
  boat_draft_m?: number
  season?: string
  guest_name?: string
  guest_email?: string
  guest_phone?: string
  notes?: string
}

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
  const { fetchApi } = useApi()

  return useQuery({
    queryKey: ['availability', type, start, end, dimensions],
    queryFn: () => {
      let url = `/api/v1/bookings/availability?type=${type.value}&start=${start.value}&end=${end.value}`
      const dims = dimensions?.value
      if (dims?.length && dims?.beam && dims?.draft) {
        url += `&length=${dims.length}&beam=${dims.beam}&draft=${dims.draft}`
      }
      return fetchApi<{ dates: AggregateDay[] }>(url)
    },
    enabled: computed(() => !!type.value && !!start.value && !!end.value),
    staleTime: 60 * 1000,
  })
}

export function useTodayAvailability(type: string) {
  const { fetchApi } = useApi()

  return useQuery({
    queryKey: ['availability-today', type],
    queryFn: () => fetchApi<TodayAvailability>(`/api/v1/bookings/availability/today?type=${type}`),
    staleTime: 60 * 1000,
  })
}

export function useHoistSlots(date: Ref<string>) {
  const { fetchApi } = useApi()

  return useQuery({
    queryKey: ['hoist-slots', date],
    queryFn: () => fetchApi<HoistSlotsResponse>(`/api/v1/bookings/hoist/slots?date=${date.value}`),
    enabled: computed(() => !!date.value),
    staleTime: 30 * 1000,
  })
}

export function useMyBookings(status?: string) {
  const { fetchApi } = useApi()

  const url = status ? `/api/v1/bookings/me?status=${status}` : '/api/v1/bookings/me'

  return useQuery({
    queryKey: ['my-bookings', status ?? 'all'],
    queryFn: () => fetchApi<Booking[]>(url),
    staleTime: 30 * 1000,
  })
}

export function useCreateBooking() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (req: CreateBookingRequest) =>
      fetchApi<Booking>('/api/v1/bookings', {
        method: 'POST',
        body: JSON.stringify(req),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['availability'] })
      queryClient.invalidateQueries({ queryKey: ['availability-today'] })
      queryClient.invalidateQueries({ queryKey: ['my-bookings'] })
      queryClient.invalidateQueries({ queryKey: ['hoist-slots'] })
    },
  })
}

export function useCancelBooking() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (bookingId: string) =>
      fetchApi<Booking>(`/api/v1/bookings/${bookingId}/cancel`, { method: 'POST' }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['availability'] })
      queryClient.invalidateQueries({ queryKey: ['availability-today'] })
      queryClient.invalidateQueries({ queryKey: ['my-bookings'] })
    },
  })
}
