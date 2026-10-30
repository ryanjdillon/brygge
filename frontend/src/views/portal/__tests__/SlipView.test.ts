import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import SlipView from '@/views/portal/SlipView.vue'

const projectsEnabled = ref(true)

vi.mock('@/composables/useFeatures', () => ({
  useFeatures: () => ({
    isEnabled: (feature: string) => (feature === 'projects' ? projectsEnabled.value : true),
  }),
}))

vi.mock('@/lib/apiClient', () => ({
  useApiClient: () => ({ GET: vi.fn(), POST: vi.fn() }),
  unwrap: (v: unknown) => v,
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: () => ({
    data: ref({
      number: '12',
      section: 'A',
      length_m: 9,
      width_m: 3,
      status: 'assigned',
      assigned_at: '2026-01-01T00:00:00Z',
    }),
    isLoading: ref(false),
    isError: ref(false),
  }),
  useMutation: () => ({ mutate: vi.fn(), isPending: ref(false) }),
}))

vi.mock('lucide-vue-next', () => ({
  AlertTriangle: { template: '<span data-icon="alert-triangle" />' },
}))

describe('SlipView', () => {
  beforeEach(() => {
    projectsEnabled.value = true
  })

  it('offers the report-an-issue button when Projects is on', () => {
    const wrapper = mountWithPlugins(SlipView)
    expect(wrapper.text()).toContain('portal.slip.reportIssue')
  })

  it('hides the report-an-issue button when Projects is off', () => {
    projectsEnabled.value = false
    const wrapper = mountWithPlugins(SlipView)
    expect(wrapper.text()).not.toContain('portal.slip.reportIssue')
    expect(wrapper.find('form').exists()).toBe(false)
    // The slip details still render.
    expect(wrapper.text()).toContain('portal.slip.slipNumber')
  })
})
