import { describe, it, expect } from 'vitest'
import { useFeatures } from '@/composables/useFeatures'

describe('useFeatures', () => {
  it('returns features ref and isEnabled function', () => {
    const { features, isEnabled } = useFeatures()
    expect(features).toBeDefined()
    expect(typeof isEnabled).toBe('function')
  })

  it('defaults all features to enabled', () => {
    const { isEnabled } = useFeatures()
    expect(isEnabled('bookings')).toBe(true)
    expect(isEnabled('projects')).toBe(true)
    expect(isEnabled('calendar')).toBe(true)
    expect(isEnabled('commerce')).toBe(true)
    expect(isEnabled('communications')).toBe(true)
  })

  it('features ref contains all feature keys', () => {
    const { features } = useFeatures()
    expect(features.value).toEqual({
      bookings: true,
      projects: true,
      calendar: true,
      commerce: true,
      communications: true,
    })
  })
})
