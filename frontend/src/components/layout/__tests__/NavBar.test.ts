import { describe, it, expect } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import { useAuthStore } from '@/stores/auth'
import NavBar from '@/components/layout/NavBar.vue'

// NavBar is a slim top bar: brand (on non-hero routes) + language switcher
// + auth controls (login, or portal/admin/logout). The old site-nav links,
// club dropdown, and mobile menu were moved out of this component, so the
// tests cover only what NavBar still renders. Mount on a non-hero route so
// the brand/standard variant renders rather than the bare hero variant.
async function mountNav(piniaOptions?: { initialState?: Record<string, unknown> }) {
  const wrapper = mountWithPlugins(NavBar, {
    initialRoute: '/contact',
    piniaOptions,
    global: { stubs: { InboxIndicator: true } },
  })
  await flushPromises()
  return wrapper
}

describe('NavBar', () => {
  it('renders the club brand on non-hero routes', async () => {
    const wrapper = await mountNav()
    expect(wrapper.text()).toContain('Brygge')
  })

  it('shows the login link when unauthenticated', async () => {
    const wrapper = await mountNav()
    expect(wrapper.text()).toContain('nav.login')
    expect(wrapper.text()).not.toContain('nav.portal')
  })

  it('shows the portal link (and no login) when authenticated', async () => {
    // initialState patching is unreliable for setup stores, so set the
    // store directly after the testing-pinia is installed by mount.
    const wrapper = await mountNav()
    const auth = useAuthStore()
    auth.user = {
      id: '1',
      name: 'Test',
      email: 'test@example.com',
      roles: ['member'],
    } as unknown as typeof auth.user
    await flushPromises()

    expect(wrapper.text()).toContain('nav.portal')
    expect(wrapper.text()).not.toContain('nav.login')
  })
})
