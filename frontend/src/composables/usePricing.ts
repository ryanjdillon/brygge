import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useI18n } from 'vue-i18n'
import { useApiClient, unwrap } from '@/lib/apiClient'
import type { components } from '@/types/api'

export type PriceItem = components['schemas']['PriceItem'] & { audience?: string }

export interface PricingRow {
  id: string
  name: string
  description: string
  unit: string
  metadata: unknown
  installments_allowed: boolean
  max_installments: number
  // audience === 'all': only allAmount is set
  // audience paired: memberAmount and/or nonMemberAmount are set
  allAmount?: number
  memberAmount?: number
  nonMemberAmount?: number
}

export interface PricingCategory {
  key: string
  label: string
  rows: PricingRow[]
  /** true when any row has member/non-member split */
  hasSplit: boolean
}

const categoryKeys: Record<string, string> = {
  harbor_membership: 'admin.pricing.categoryHarborMembership',
  membership: 'admin.pricing.categoryMembership',
  slip_fee: 'admin.pricing.categorySlipFee',
  seasonal_rental: 'admin.pricing.categorySeasonalRental',
  guest: 'admin.pricing.categoryGuest',
  motorhome: 'admin.pricing.categoryMotorhome',
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
  const client = useApiClient()
  const { t } = useI18n()

  function categoryLabel(key: string): string {
    return categoryKeys[key] ? t(categoryKeys[key]) : key
  }

  function unitLabel(unit: string): string {
    return unitKeys[unit] ? t(unitKeys[unit]) : `/${unit}`
  }

  const query = useQuery({
    queryKey: ['pricing'],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/pricing')),
    staleTime: 10 * 60 * 1000,
  })

  const items = computed(() => (query.data.value?.items ?? []) as PriceItem[])

  const categories = computed<PricingCategory[]>(() => {
    // Group by category first
    const byCat = new Map<string, PriceItem[]>()
    for (const item of items.value) {
      const list = byCat.get(item.category) ?? []
      list.push(item)
      byCat.set(item.category, list)
    }

    return Array.from(byCat.entries()).map(([key, catItems]) => {
      // Within a category, pair items with the same name that differ only by audience
      const rowMap = new Map<string, PricingRow>()
      for (const item of catItems) {
        const existing = rowMap.get(item.name)
        if (existing) {
          if (item.audience === 'member') existing.memberAmount = item.amount
          else if (item.audience === 'non_member') existing.nonMemberAmount = item.amount
          else existing.allAmount = item.amount
        } else {
          const row: PricingRow = {
            id: item.id,
            name: item.name,
            description: item.description,
            unit: item.unit,
            metadata: item.metadata,
            installments_allowed: item.installments_allowed,
            max_installments: item.max_installments,
          }
          if (item.audience === 'member') row.memberAmount = item.amount
          else if (item.audience === 'non_member') row.nonMemberAmount = item.amount
          else row.allAmount = item.amount
          rowMap.set(item.name, row)
        }
      }

      const rows = Array.from(rowMap.values())
      const hasSplit = rows.some(r => r.memberAmount !== undefined || r.nonMemberAmount !== undefined)
      return { key, label: categoryLabel(key), rows, hasSplit }
    })
  })

  return { ...query, items, categories, categoryLabel, unitLabel }
}
