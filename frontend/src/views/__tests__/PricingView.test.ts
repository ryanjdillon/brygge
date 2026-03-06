import { describe, it, expect, vi } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import PricingView from '@/views/PricingView.vue'

vi.mock('@/composables/usePricing', () => ({
  usePricing: () => ({
    data: { value: null },
    isLoading: { value: false },
    isError: { value: false },
  }),
}))

describe('PricingView', () => {
  it('renders without errors', () => {
    const wrapper = mountWithPlugins(PricingView)
    expect(wrapper.exists()).toBe(true)
  })

  it('renders pricing heading', () => {
    const wrapper = mountWithPlugins(PricingView)
    expect(wrapper.find('h1').text()).toBe('pricing.title')
  })
})
