import { useQuery } from '@tanstack/vue-query'

export interface PricingCategory {
  key: string
  label: string
  items: PricingItem[]
}

export interface PricingItem {
  name: string
  price: number
  unit: string
  description?: string
}

async function fetchPricing(): Promise<PricingCategory[]> {
  const response = await fetch('/api/v1/pricing')
  if (!response.ok) {
    throw new Error('Failed to fetch pricing data')
  }
  const data = await response.json()
  const pricing = data.pricing ?? {}

  return Object.entries(pricing).map(([key, value]) => {
    if (Array.isArray(value)) {
      return { key, label: key, items: value as PricingItem[] }
    }
    if (typeof value === 'object' && value !== null) {
      const items = Object.entries(value as Record<string, unknown>).map(([name, price]) => ({
        name,
        price: Number(price) || 0,
        unit: '',
      }))
      return { key, label: key, items }
    }
    return { key, label: key, items: [] }
  })
}

export function usePricing() {
  return useQuery({
    queryKey: ['pricing'],
    queryFn: fetchPricing,
    staleTime: 10 * 60 * 1000,
  })
}
