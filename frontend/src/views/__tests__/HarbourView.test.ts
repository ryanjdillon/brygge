import { describe, it, expect, vi } from 'vitest'
import { ref } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import HarbourView from '@/views/HarbourView.vue'

vi.mock('maplibre-gl', () => ({
  default: {
    Map: vi.fn().mockImplementation(() => ({
      addControl: vi.fn(),
      on: vi.fn(),
      remove: vi.fn(),
    })),
    NavigationControl: vi.fn(),
    FullscreenControl: vi.fn(),
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
  Anchor: { template: '<span data-icon="anchor" />' },
  Radio: { template: '<span data-icon="radio" />' },
  Sailboat: { template: '<span data-icon="sailboat" />' },
  HandCoins: { template: '<span data-icon="hand-coins" />' },
  Download: { template: '<span data-icon="download" />' },
  ExternalLink: { template: '<span data-icon="external-link" />' },
}))

vi.mock('@/composables/useMap', () => ({
  useClubCoordinates: () => ({
    data: ref({ name: 'Test Klubb', latitude: 59.9, longitude: 10.7 }),
    isLoading: ref(false),
  }),
  useMapMarkers: () => ({
    data: ref([]),
    isLoading: ref(false),
  }),
}))

vi.mock('@/composables/usePricing', () => ({
  usePricing: () => ({
    categories: ref([
      { key: 'guest', label: 'Gjesteplasser', items: [{ id: '1', name: 'Gjesteplass', amount: 350, unit: 'night' }] },
    ]),
    isLoading: ref(false),
    unitLabel: (unit: string) => `/${unit}`,
  }),
}))

vi.mock('@/composables/useBookings', () => ({
  useTodayAvailability: () => ({
    data: ref({ available: 12, total: 15 }),
    isLoading: ref(false),
  }),
}))

vi.mock('@/composables/useApi', () => ({
  useApi: () => ({
    fetchApi: vi.fn(),
  }),
}))

describe('HarbourView', () => {
  it('renders without errors', () => {
    const wrapper = mountWithPlugins(HarbourView)
    expect(wrapper.exists()).toBe(true)
  })

  it('renders harbour title', () => {
    const wrapper = mountWithPlugins(HarbourView)
    expect(wrapper.find('h1').text()).toBe('harbour.title')
  })

  it('shows availability badge', () => {
    const wrapper = mountWithPlugins(HarbourView)
    expect(wrapper.text()).toContain('booking.availableToday')
  })

  it('renders pricing section', () => {
    const wrapper = mountWithPlugins(HarbourView)
    expect(wrapper.text()).toContain('harbour.pricing')
    expect(wrapper.text()).toContain('Gjesteplasser')
  })

  it('renders navigation info', () => {
    const wrapper = mountWithPlugins(HarbourView)
    expect(wrapper.text()).toContain('harbour.navigation')
    expect(wrapper.text()).toContain('directions.coordinates')
    expect(wrapper.text()).toContain('directions.vhf')
  })

  it('renders CTA section', () => {
    const wrapper = mountWithPlugins(HarbourView)
    expect(wrapper.text()).toContain('harbour.ctaTitle')
  })
})
