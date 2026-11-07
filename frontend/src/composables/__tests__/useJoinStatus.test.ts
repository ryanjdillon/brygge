import { describe, it, expect, vi } from 'vitest'
import { ref } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useJoinStatus } from '@/composables/useJoinStatus'

vi.mock('@/lib/apiClient', () => ({ useApiClient: () => ({ GET: vi.fn() }), unwrap: vi.fn() }))

const PRICE = { amount: 650, currency: 'NOK', name: 'Kontingent', description: 'Kontingent' }

function answer(data: unknown, isError = false) {
  vi.mocked(useQuery).mockReturnValueOnce({
    data: ref(data),
    isLoading: ref(false),
    isError: ref(isError),
    isFetching: ref(false),
    refetch: vi.fn(),
  } as unknown as ReturnType<typeof useQuery>)
  return useJoinStatus()
}

describe('useJoinStatus', () => {
  it('passes on an open status with its price and year', () => {
    const s = answer({ open: true, price: PRICE, year: 2031, missing: [] })
    expect(s.open.value).toBe(true)
    expect(s.price.value).toEqual(PRICE)
    expect(s.year.value).toBe(2031)
  })

  it('is closed, with no price, when the club does not take joins', () => {
    const s = answer({ open: false, price: PRICE, missing: ['price_item_unset'] })
    expect(s.open.value).toBe(false)
    expect(s.price.value).toBeNull()
  })

  it('counts a failed request as closed', () => {
    const s = answer({ open: true, price: PRICE }, true)
    expect(s.open.value).toBe(false)
    expect(s.price.value).toBeNull()
    expect(s.isError.value).toBe(true)
  })

  it('is closed until the status has loaded and falls back to this year', () => {
    const s = answer(undefined)
    expect(s.open.value).toBe(false)
    expect(s.year.value).toBe(new Date().getFullYear())
  })

  // One shared key and a few minutes' staleTime: every entry point on a page
  // reads the same cached answer, so a page makes one status request.
  it('shares one cached query across entry points', () => {
    vi.mocked(useQuery).mockClear()
    answer({ open: true, price: PRICE })
    answer({ open: true, price: PRICE })
    const calls = vi.mocked(useQuery).mock.calls
    expect(calls).toHaveLength(2)
    for (const [opts] of calls) {
      const o = opts as unknown as { queryKey: unknown; staleTime: number }
      expect(o.queryKey).toEqual(['join-status'])
      expect(o.staleTime).toBe(5 * 60 * 1000)
    }
  })

  it('passes on whether the status is being fetched', () => {
    const s = answer({ open: false, price: null }, true)
    expect(s.isFetching.value).toBe(false)
  })
})
