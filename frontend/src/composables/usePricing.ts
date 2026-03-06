import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'

export interface PriceItem {
  id: string
  category: string
  name: string
  description: string
  amount: number
  currency: string
  unit: string
  installments_allowed: boolean
  max_installments: number
  metadata: Record<string, string>
  sort_order: number
  is_active: boolean
}

export interface PricingCategory {
  key: string
  label: string
  items: PriceItem[]
}

const categoryLabels: Record<string, string> = {
  moloandel: 'Moloandel',
  slip_fee: 'Plassleie',
  seasonal_rental: 'Sesongplass',
  guest: 'Gjesteplasser',
  bobil: 'Bobilparkering',
  room_hire: 'Romutleie',
  service: 'Tjenester',
  other: 'Annet',
}

const unitLabels: Record<string, string> = {
  once: 'engangs',
  year: '/år',
  season: '/sesong',
  day: '/døgn',
  night: '/natt',
  hour: '/time',
}

export function unitLabel(unit: string): string {
  return unitLabels[unit] ?? `/${unit}`
}

export function usePricing() {
  const { fetchApi } = useApi()

  const query = useQuery({
    queryKey: ['pricing'],
    queryFn: () => fetchApi<{ items: PriceItem[] }>('/api/v1/pricing'),
    staleTime: 10 * 60 * 1000,
  })

  const items = computed(() => query.data.value?.items ?? [])

  const categories = computed<PricingCategory[]>(() => {
    const grouped = new Map<string, PriceItem[]>()
    for (const item of items.value) {
      const list = grouped.get(item.category) ?? []
      list.push(item)
      grouped.set(item.category, list)
    }
    return Array.from(grouped.entries()).map(([key, items]) => ({
      key,
      label: categoryLabels[key] ?? key,
      items,
    }))
  })

  return { ...query, items, categories }
}
