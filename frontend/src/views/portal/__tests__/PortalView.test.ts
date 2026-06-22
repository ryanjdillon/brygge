import { describe, it, expect, vi } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
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
  BrushCleaning: { template: '<span data-icon="brush-cleaning" />' },
  Bell: { template: '<span data-icon="bell" />' },
  ShieldCheck: { template: '<span data-icon="shield-check" />' },
  Receipt: { template: '<span data-icon="receipt" />' },
  Map: { template: '<span data-icon="map" />' },
  Menu: { template: '<span data-icon="menu" />' },
  X: { template: '<span data-icon="x" />' },
  ChevronDown: { template: '<span data-icon="chevron-down" />' },
}))

describe('PortalView', () => {
  it('renders without errors', () => {
    const wrapper = mountWithPlugins(PortalView)
    expect(wrapper.exists()).toBe(true)
  })

  it('renders portal title', () => {
    const wrapper = mountWithPlugins(PortalView)
    expect(wrapper.text()).toContain('portal.sidebarTitle')
  })

  it('renders sidebar navigation links', () => {
    const wrapper = mountWithPlugins(PortalView)
    expect(wrapper.text()).toContain('portal.sidebar.dashboard')
    expect(wrapper.text()).toContain('portal.sidebar.profile')
    expect(wrapper.text()).toContain('portal.sidebar.myBoats')
    expect(wrapper.text()).toContain('portal.sidebar.invoices')
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
