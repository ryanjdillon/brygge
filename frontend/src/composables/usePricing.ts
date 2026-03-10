import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useI18n } from 'vue-i18n'
import { useApi } from '@/composables/useApi'
import type { components } from '@/types/api'

export type PriceItem = components['schemas']['PriceItem']

export interface PricingCategory {
  key: string
  label: string
  items: PriceItem[]
}

const categoryKeys: Record<string, string> = {
  moloandel: 'admin.pricing.categoryMoloandel',
  slip_fee: 'admin.pricing.categorySlipFee',
  seasonal_rental: 'admin.pricing.categorySeasonalRental',
  guest: 'admin.pricing.categoryGuest',
  bobil: 'admin.pricing.categoryBobil',
  room_hire: 'admin.pricing.categoryRoomHire',
  service: 'admin.pricing.categoryService',
  other: 'admin.pricing.categoryOther',
}

const unitKeys: Record<string, string> = {
  once: 'admin.pricing.unitOnce',
  year: 'admin.pricing.unitYear',
  season: 'admin.pricing.unitSeason',
  day: 'admin.pricing.unitDay',
  night: 'admin.pricing.unitNight',
  hour: 'admin.pricing.unitHour',
}

export function usePricing() {
  const { fetchApi } = useApi()
  const { t } = useI18n()

  function categoryLabel(key: string): string {
    return categoryKeys[key] ? t(categoryKeys[key]) : key
  }

  function unitLabel(unit: string): string {
    return unitKeys[unit] ? t(unitKeys[unit]) : `/${unit}`
  }

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
      label: categoryLabel(key),
      items,
    }))
  })

  return { ...query, items, categories, unitLabel }
}
