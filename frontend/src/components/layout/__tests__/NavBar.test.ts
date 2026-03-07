import { describe, it, expect, vi } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import NavBar from '@/components/layout/NavBar.vue'

vi.mock('lucide-vue-next', () => ({
  Menu: { template: '<span data-icon="menu" />' },
  X: { template: '<span data-icon="x" />' },
  LogIn: { template: '<span data-icon="login" />' },
  LogOut: { template: '<span data-icon="logout" />' },
  User: { template: '<span data-icon="user" />' },
  Shield: { template: '<span data-icon="shield" />' },
  ChevronDown: { template: '<span data-icon="chevron-down" />' },
}))

describe('NavBar', () => {
  it('renders club name', () => {
    const wrapper = mountWithPlugins(NavBar)
    expect(wrapper.text()).toContain('Brygge')
  })

  it('renders top-level nav links', () => {
    const wrapper = mountWithPlugins(NavBar)
    const expectedLinks = [
      'nav.home',
      'nav.harbour',
      'nav.bobil',
      'nav.weather',
      'nav.merchandise',
      'nav.contact',
    ]

    for (const label of expectedLinks) {
      expect(wrapper.text()).toContain(label)
    }
  })

  it('renders club dropdown trigger', () => {
    const wrapper = mountWithPlugins(NavBar)
    expect(wrapper.text()).toContain('nav.club')
  })

  it('shows club dropdown items on click', async () => {
    const wrapper = mountWithPlugins(NavBar)
    const dropdownBtn = wrapper.findAll('button').find((b) => b.text().includes('nav.club'))
    await dropdownBtn!.trigger('click')

    expect(wrapper.text()).toContain('nav.calendar')
    expect(wrapper.text()).toContain('nav.pricing')
    expect(wrapper.text()).toContain('nav.join')
    expect(wrapper.text()).toContain('nav.history')
  })

  it('shows login button when unauthenticated', () => {
    const wrapper = mountWithPlugins(NavBar)
    expect(wrapper.text()).toContain('nav.login')
    expect(wrapper.text()).not.toContain('nav.portal')
  })

  it('shows portal link when authenticated', () => {
    const wrapper = mountWithPlugins(NavBar, {
      piniaOptions: {
        initialState: {
          auth: {
            user: { id: '1', name: 'Test', email: 'test@example.com', roles: ['member'] },
          },
        },
      },
    })

    expect(wrapper.text()).toContain('nav.portal')
  })

  it('mobile menu toggle works', async () => {
    const wrapper = mountWithPlugins(NavBar)

    const mobileMenu = () => wrapper.find('.md\\:hidden.border-t')
    expect(mobileMenu().exists()).toBe(false)

    const hamburger = wrapper.find('button.md\\:hidden')
    await hamburger.trigger('click')

    expect(mobileMenu().exists()).toBe(true)

    await hamburger.trigger('click')
    expect(mobileMenu().exists()).toBe(false)
  })

  it('mobile menu shows club links under section header', async () => {
    const wrapper = mountWithPlugins(NavBar)
    const hamburger = wrapper.find('button.md\\:hidden')
    await hamburger.trigger('click')

    const mobileMenu = wrapper.find('.md\\:hidden.border-t')
    expect(mobileMenu.text()).toContain('nav.club')
    expect(mobileMenu.text()).toContain('nav.calendar')
    expect(mobileMenu.text()).toContain('nav.history')
  })
})
