import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import { setJoinStatus } from '@/test/joinStatus'
import { useAuthStore } from '@/stores/auth'
import NavBar from '@/components/layout/NavBar.vue'

vi.mock('@/composables/useJoinStatus', async () => (await import('@/test/joinStatus')).joinStatusModule)

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
  beforeEach(() => setJoinStatus())

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

  it('offers joining to a signed-out visitor while joining is open', async () => {
    setJoinStatus({ open: true })
    const wrapper = await mountNav()
    const join = wrapper.find('[data-testid="nav-join"]')
    expect(join.attributes('href')).toBe('/join')
    expect(join.text()).toBe('nav.join')
  })

  it('offers joining on the hero page too', async () => {
    setJoinStatus({ open: true })
    const wrapper = mountWithPlugins(NavBar, { initialRoute: '/', global: { stubs: { InboxIndicator: true } } })
    await flushPromises()
    expect(wrapper.find('[data-testid="nav-join"]').attributes('href')).toBe('/join')
  })

  it('hides joining while it is closed', async () => {
    const wrapper = await mountNav()
    expect(wrapper.find('[data-testid="nav-join"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('nav.join')
  })

  it('hides joining from a signed-in user even while it is open', async () => {
    setJoinStatus({ open: true })
    const wrapper = await mountNav()
    const auth = useAuthStore()
    auth.user = { id: '1', name: 'Test', email: 'test@example.com', roles: ['member'] } as unknown as typeof auth.user
    await flushPromises()
    expect(wrapper.find('[data-testid="nav-join"]').exists()).toBe(false)
  })
})
