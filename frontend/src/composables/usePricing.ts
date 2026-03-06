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
  return response.json()
}

export function usePricing() {
  return useQuery({
    queryKey: ['pricing'],
    queryFn: fetchPricing,
    staleTime: 10 * 60 * 1000,
  })
}
