import { describe, it, expect, vi } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import CalendarView from '@/views/CalendarView.vue'

vi.mock('lucide-vue-next', () => ({
  Download: { template: '<span data-icon="download" />' },
  Plus: { template: '<span data-icon="plus" />' },
  X: { template: '<span data-icon="x" />' },
}))

vi.mock('@/composables/useEvents', () => ({
  useEvents: () => ({
    data: { value: null },
    isLoading: { value: false },
  }),
}))

describe('CalendarView', () => {
  it('renders without errors', () => {
    const wrapper = mountWithPlugins(CalendarView)
    expect(wrapper.exists()).toBe(true)
  })

  it('renders calendar heading', () => {
    const wrapper = mountWithPlugins(CalendarView)
    expect(wrapper.find('h1').text()).toBe('calendar.title')
  })

  it('renders description text', () => {
    const wrapper = mountWithPlugins(CalendarView)
    expect(wrapper.text()).toContain('calendar.description')
  })

  it('renders filter buttons for event tags', () => {
    const wrapper = mountWithPlugins(CalendarView)
    expect(wrapper.text()).toContain('calendar.filterRegatta')
    expect(wrapper.text()).toContain('calendar.filterDugnad')
    expect(wrapper.text()).toContain('calendar.filterSocial')
    expect(wrapper.text()).toContain('calendar.filterAgm')
  })

  it('renders export calendar link', () => {
    const wrapper = mountWithPlugins(CalendarView)
    const exportLink = wrapper.find('a[href="/api/v1/calendar/public.ics"]')
    expect(exportLink.exists()).toBe(true)
    expect(exportLink.text()).toContain('calendar.export')
  })

})
