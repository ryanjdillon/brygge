import { ref, readonly } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import type { components } from '@/types/api'

type FeaturesMap = components['schemas']['FeaturesResponse']

const features = ref<FeaturesMap>({
  bookings: true,
  projects: true,
  calendar: true,
  commerce: true,
  communications: true,
})

let initialized = false

export function useFeatures() {
  if (!initialized) {
    initialized = true
    useQuery({
      queryKey: ['features'],
      queryFn: async () => {
        const res = await fetch('/api/v1/features')
        if (!res.ok) return features.value
        const data: FeaturesMap = await res.json()
        features.value = data
        return data
      },
      staleTime: Infinity,
    })
  }

  function isEnabled(feature: keyof FeaturesMap): boolean {
    return features.value[feature] !== false
  }

  return { features: readonly(features), isEnabled }
}
