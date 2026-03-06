import { describe, it, expect, vi } from 'vitest'
import { mountWithPlugins, createMockAuthStore } from '@/test/test-utils'
import PortalView from '@/views/PortalView.vue'

vi.mock('lucide-vue-next', () => ({
  LayoutDashboard: { template: '<span data-icon="layout-dashboard" />' },
  User: { template: '<span data-icon="user" />' },
  Ship: { template: '<span data-icon="ship" />' },
  Users: { template: '<span data-icon="users" />' },
  FileText: { template: '<span data-icon="file-text" />' },
  ListOrdered: { template: '<span data-icon="list-ordered" />' },
  Anchor: { template: '<span data-icon="anchor" />' },
  CalendarDays: { template: '<span data-icon="calendar-days" />' },
  MessageCircle: { template: '<span data-icon="message-circle" />' },
  Lightbulb: { template: '<span data-icon="lightbulb" />' },
  Menu: { template: '<span data-icon="menu" />' },
  X: { template: '<span data-icon="x" />' },
}))

describe('PortalView', () => {
  it('renders without errors', () => {
    const wrapper = mountWithPlugins(PortalView)
    expect(wrapper.exists()).toBe(true)
  })

  it('renders portal title', () => {
    const wrapper = mountWithPlugins(PortalView)
    expect(wrapper.text()).toContain('portal.title')
  })

  it('renders sidebar navigation links', () => {
    const wrapper = mountWithPlugins(PortalView)
    expect(wrapper.text()).toContain('portal.sidebar.dashboard')
    expect(wrapper.text()).toContain('portal.sidebar.profile')
    expect(wrapper.text()).toContain('portal.sidebar.boats')
    expect(wrapper.text()).toContain('portal.sidebar.documents')
    expect(wrapper.text()).toContain('portal.sidebar.bookings')
  })

  it('renders RouterLink elements for navigation', () => {
    const wrapper = mountWithPlugins(PortalView)
    const links = wrapper.findAll('a')
    const hrefs = links.map((l) => l.attributes('href'))
    expect(hrefs).toContain('/portal')
    expect(hrefs).toContain('/portal/profile')
    expect(hrefs).toContain('/portal/boats')
  })
})
