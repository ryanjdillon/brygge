import { vi } from 'vitest'
import { ref } from 'vue'

// A stand-in for useJoinStatus: tests set the state with setJoinStatus and
// install it with
//   vi.mock('@/composables/useJoinStatus', async () => (await import('@/test/joinStatus')).joinStatusModule)

type Price = { amount: number; currency: string; name: string; description: string }

const open = ref(false)
const price = ref<Price | null>(null)
const year = ref(2031)
const isLoading = ref(false)
const isError = ref(false)
const isFetching = ref(false)
export const refetchJoinStatus = vi.fn()

export function setJoinStatus(s: { open?: boolean; price?: Price | null; year?: number; isLoading?: boolean; isError?: boolean; isFetching?: boolean } = {}) {
  open.value = s.open ?? false
  price.value = s.price ?? null
  year.value = s.year ?? 2031
  isLoading.value = s.isLoading ?? false
  isError.value = s.isError ?? false
  isFetching.value = s.isFetching ?? false
  refetchJoinStatus.mockReset()
}

export const TEST_JOIN_PRICE: Price = { amount: 650, currency: 'NOK', name: 'Kontingent', description: 'Kontingent' }

export const joinStatusModule = {
  joinStatusKey: ['join-status'] as const,
  useJoinStatus: () => ({ open, price, year, isLoading, isError, isFetching, refetch: refetchJoinStatus }),
}
