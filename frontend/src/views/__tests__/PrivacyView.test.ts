import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import PrivacyView from '@/views/portal/PrivacyView.vue'

vi.mock('lucide-vue-next', () => ({
  Download: { template: '<span data-icon="download" />' },
  Trash2: { template: '<span data-icon="trash" />' },
  ShieldCheck: { template: '<span data-icon="shield-check" />' },
}))

vi.mock('@/composables/useGdpr', () => ({
  useDeletionStatus: () => ({
    data: ref(null),
    isLoading: ref(false),
  }),
  useRequestDeletion: () => ({
    mutate: vi.fn(),
    isPending: ref(false),
  }),
  useCancelDeletion: () => ({
    mutate: vi.fn(),
    isPending: ref(false),
  }),
  useDataExport: () => ({
    mutate: vi.fn(),
    isPending: ref(false),
  }),
  useMyConsents: () => ({
    consents: ref([
      { id: '1', consent_type: 'terms', version: '1.0', granted_at: '2025-01-01T00:00:00Z' },
      { id: '2', consent_type: 'privacy_policy', version: '1.0', granted_at: '2025-01-01T00:00:00Z' },
    ]),
    isLoading: ref(false),
  }),
}))

describe('PrivacyView', () => {
  it('renders without errors', () => {
    const wrapper = mountWithPlugins(PrivacyView)
    expect(wrapper.exists()).toBe(true)
  })

  it('renders title', () => {
    const wrapper = mountWithPlugins(PrivacyView)
    expect(wrapper.find('h2').text()).toBeTruthy()
  })

  it('renders data export button', () => {
    const wrapper = mountWithPlugins(PrivacyView)
    const buttons = wrapper.findAll('button')
    expect(buttons.length).toBeGreaterThan(0)
  })

  it('renders delete account section', () => {
    const wrapper = mountWithPlugins(PrivacyView)
    const icons = wrapper.findAll('[data-icon="trash"]')
    expect(icons.length).toBe(1)
  })

  it('renders consent history', () => {
    const wrapper = mountWithPlugins(PrivacyView)
    const listItems = wrapper.findAll('li')
    expect(listItems.length).toBe(2)
  })
})

describe('PrivacyView with pending deletion', () => {
  beforeEach(() => {
    vi.doMock('@/composables/useGdpr', () => ({
      useDeletionStatus: () => ({
        data: ref({ id: '1', status: 'pending', requested_at: '2025-01-01T00:00:00Z', grace_end: '2025-01-15T00:00:00Z', cancelled_at: null, processed_at: null }),
        isLoading: ref(false),
      }),
      useRequestDeletion: () => ({
        mutate: vi.fn(),
        isPending: ref(false),
      }),
      useCancelDeletion: () => ({
        mutate: vi.fn(),
        isPending: ref(false),
      }),
      useDataExport: () => ({
        mutate: vi.fn(),
        isPending: ref(false),
      }),
      useMyConsents: () => ({
        consents: ref([]),
        isLoading: ref(false),
      }),
    }))
  })

  it('renders pending deletion state', async () => {
    const { default: PV } = await import('@/views/portal/PrivacyView.vue')
    const wrapper = mountWithPlugins(PV)
    expect(wrapper.exists()).toBe(true)
  })
})
