import { describe, it, expect, vi } from 'vitest'
import { ref } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import MapMarkersAdminView from '@/views/admin/MapMarkersAdminView.vue'

vi.mock('lucide-vue-next', () => ({
  Plus: { template: '<span data-icon="plus" />' },
  Pencil: { template: '<span data-icon="pencil" />' },
  Trash2: { template: '<span data-icon="trash" />' },
}))

const mockMarkers = [
  {
    id: '1',
    club_id: 'c1',
    marker_type: 'waypoint',
    label: 'Test Marker',
    lat: 59.9,
    lng: 10.7,
    sort_order: 1,
    created_at: '2026-01-01T00:00:00Z',
  },
]

vi.mock('@/composables/useMap', () => ({
  useMapMarkers: () => ({
    data: ref(mockMarkers),
    isLoading: ref(false),
  }),
}))

vi.mock('@/lib/apiClient', () => ({
  useApiClient: () => ({ GET: vi.fn(), POST: vi.fn(), PUT: vi.fn(), DELETE: vi.fn() }),
  unwrap: vi.fn((x) => x),
}))

vi.mock('@tanstack/vue-query', async () => {
  const actual = await vi.importActual('@tanstack/vue-query')
  return {
    ...actual,
    useQueryClient: () => ({
      invalidateQueries: vi.fn(),
    }),
  }
})

describe('MapMarkersAdminView', () => {
  it('renders without errors', () => {
    const wrapper = mountWithPlugins(MapMarkersAdminView)
    expect(wrapper.exists()).toBe(true)
  })

  it('renders title', () => {
    const wrapper = mountWithPlugins(MapMarkersAdminView)
    expect(wrapper.find('h1').text()).toBe('mapAdmin.title')
  })

  it('renders add marker button', () => {
    const wrapper = mountWithPlugins(MapMarkersAdminView)
    expect(wrapper.text()).toContain('mapAdmin.addMarker')
  })

  it('renders marker table with data', () => {
    const wrapper = mountWithPlugins(MapMarkersAdminView)
    expect(wrapper.find('table').exists()).toBe(true)
    expect(wrapper.text()).toContain('Test Marker')
    expect(wrapper.text()).toContain('59.900000')
  })

  it('opens create modal on button click', async () => {
    const wrapper = mountWithPlugins(MapMarkersAdminView)
    const addBtn = wrapper.findAll('button').find((b) => b.text().includes('mapAdmin.addMarker'))
    await addBtn!.trigger('click')
    expect(wrapper.find('[role="dialog"]').exists()).toBe(true)
  })
})
