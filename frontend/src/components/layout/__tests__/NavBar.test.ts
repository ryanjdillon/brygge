import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import { useAuthStore } from '@/stores/auth'
import NavBar from '@/components/layout/NavBar.vue'

vi.mock('lucide-vue-next', () => ({
  Menu: { template: '<span data-icon="menu" />' },
  X: { template: '<span data-icon="x" />' },
  LogIn: { template: '<span data-icon="login" />' },
  User: { template: '<span data-icon="user" />' },
}))

describe('NavBar', () => {
  it('renders club name', () => {
    const wrapper = mountWithPlugins(NavBar)
    expect(wrapper.text()).toContain('Brygge')
  })

  it('renders all public nav links', () => {
    const wrapper = mountWithPlugins(NavBar)
    const expectedLinks = [
      'nav.home',
      'nav.calendar',
      'nav.weather',
      'nav.directions',
      'nav.contact',
      'nav.pricing',
      'nav.merchandise',
      'nav.join',
    ]

    for (const label of expectedLinks) {
      expect(wrapper.text()).toContain(label)
    }
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
})
