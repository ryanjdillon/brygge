import { describe, it, expect, vi } from 'vitest'
import { ref } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import BobilView from '@/views/BobilView.vue'

vi.mock('maplibre-gl', () => ({
  default: {
    Map: vi.fn().mockImplementation(() => ({
      addControl: vi.fn(),
      on: vi.fn(),
      remove: vi.fn(),
    })),
    NavigationControl: vi.fn(),
    Marker: vi.fn().mockImplementation(() => ({
      setLngLat: vi.fn().mockReturnThis(),
      setPopup: vi.fn().mockReturnThis(),
      addTo: vi.fn().mockReturnThis(),
    })),
    Popup: vi.fn().mockImplementation(() => ({
      setHTML: vi.fn().mockReturnThis(),
    })),
  },
}))

vi.mock('lucide-vue-next', () => ({
  Car: { template: '<span data-icon="car" />' },
  Plug: { template: '<span data-icon="plug" />' },
  Droplets: { template: '<span data-icon="droplets" />' },
  Info: { template: '<span data-icon="info" />' },
  ExternalLink: { template: '<span data-icon="external-link" />' },
}))

vi.mock('@/composables/useMap', () => ({
  useClubCoordinates: () => ({
    data: ref({ name: 'Test Klubb', latitude: 59.9, longitude: 10.7 }),
    isLoading: ref(false),
  }),
}))

vi.mock('@/composables/usePricing', () => ({
  usePricing: () => ({
    categories: ref([
      { key: 'bobil', label: 'Bobilparkering', items: [{ id: '1', name: 'Bobilplass', amount: 250, unit: 'night' }] },
    ]),
    isLoading: ref(false),
    unitLabel: (unit: string) => `/${unit}`,
  }),
}))

vi.mock('@/composables/useBookings', () => ({
  useTodayAvailability: () => ({
    data: ref({ available: 8, total: 10 }),
    isLoading: ref(false),
  }),
}))

vi.mock('@/composables/useApi', () => ({
  useApi: () => ({
    fetchApi: vi.fn(),
  }),
}))

describe('BobilView', () => {
  it('renders without errors', () => {
    const wrapper = mountWithPlugins(BobilView)
    expect(wrapper.exists()).toBe(true)
  })

  it('renders bobil title', () => {
    const wrapper = mountWithPlugins(BobilView)
    expect(wrapper.find('h1').text()).toBe('bobil.title')
  })

  it('shows availability badge', () => {
    const wrapper = mountWithPlugins(BobilView)
    expect(wrapper.text()).toContain('booking.availableToday')
  })

  it('renders pricing section', () => {
    const wrapper = mountWithPlugins(BobilView)
    expect(wrapper.text()).toContain('bobil.pricing')
    expect(wrapper.text()).toContain('Bobilparkering')
  })

  it('renders practical info section', () => {
    const wrapper = mountWithPlugins(BobilView)
    expect(wrapper.text()).toContain('bobil.practicalInfo')
    expect(wrapper.text()).toContain('bobil.power')
    expect(wrapper.text()).toContain('bobil.facilities')
  })

  it('renders CTA section', () => {
    const wrapper = mountWithPlugins(BobilView)
    expect(wrapper.text()).toContain('bobil.ctaTitle')
  })
})
