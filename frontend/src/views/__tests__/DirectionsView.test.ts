import { describe, it, expect, vi } from 'vitest'
import { ref } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import DirectionsView from '@/views/DirectionsView.vue'

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
  MapPin: { template: '<span data-icon="map-pin" />' },
  Navigation: { template: '<span data-icon="navigation" />' },
  Radio: { template: '<span data-icon="radio" />' },
  Anchor: { template: '<span data-icon="anchor" />' },
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

describe('DirectionsView', () => {
  it('renders without errors', () => {
    const wrapper = mountWithPlugins(DirectionsView)
    expect(wrapper.exists()).toBe(true)
  })

  it('renders directions heading', () => {
    const wrapper = mountWithPlugins(DirectionsView)
    expect(wrapper.find('h1').text()).toBe('directions.title')
  })

  it('renders land and sea sections', () => {
    const wrapper = mountWithPlugins(DirectionsView)
    const sections = wrapper.findAll('h2')
    expect(sections.length).toBe(2)
    expect(sections[0].text()).toContain('directions.land')
    expect(sections[1].text()).toContain('directions.sea')
  })

  it('renders GPX download link', () => {
    const wrapper = mountWithPlugins(DirectionsView)
    const gpxLink = wrapper.find('a[href="/api/v1/map/export/gpx"]')
    expect(gpxLink.exists()).toBe(true)
    expect(gpxLink.text()).toContain('directions.downloadGPX')
  })

  it('renders VHF and approach info', () => {
    const wrapper = mountWithPlugins(DirectionsView)
    expect(wrapper.text()).toContain('directions.vhf')
    expect(wrapper.text()).toContain('directions.approach')
    expect(wrapper.text()).toContain('directions.depth')
  })

  it('renders coordinates from API', () => {
    const wrapper = mountWithPlugins(DirectionsView)
    expect(wrapper.text()).toContain('59.9000')
    expect(wrapper.text()).toContain('10.7000')
  })
})
