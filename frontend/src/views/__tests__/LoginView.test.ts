import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { useAuthStore } from '@/stores/auth'
import { mountWithPlugins } from '@/test/test-utils'
import { setJoinStatus } from '@/test/joinStatus'
import LoginView from '@/views/LoginView.vue'

vi.mock('@/composables/useJoinStatus', async () => (await import('@/test/joinStatus')).joinStatusModule)

vi.mock('lucide-vue-next', () => ({
  LogIn: { template: '<span data-icon="login" />' },
  ChevronDown: { template: '<span data-icon="chevron-down" />' },
  ChevronUp: { template: '<span data-icon="chevron-up" />' },
}))

describe('LoginView', () => {
  beforeEach(() => setJoinStatus())

  it('renders without errors', () => {
    const wrapper = mountWithPlugins(LoginView)
    expect(wrapper.exists()).toBe(true)
  })

  it('renders login heading', () => {
    const wrapper = mountWithPlugins(LoginView)
    expect(wrapper.find('h1').text()).toBe('login.title')
  })

  it('links the join form, labelled Bli medlem, while joining is open', () => {
    setJoinStatus({ open: true })
    const line = mountWithPlugins(LoginView).find('[data-testid="login-join"]')
    expect(line.text()).toContain('login.noAccount')
    expect(line.find('a').attributes('href')).toBe('/join')
    expect(line.find('a').text()).toBe('nav.join')
  })

  it('drops the join line while joining is closed', () => {
    const wrapper = mountWithPlugins(LoginView)
    expect(wrapper.find('[data-testid="login-join"]').exists()).toBe(false)
    expect(wrapper.find('a[href="/join"]').exists()).toBe(false)
  })

  // The guard's ?redirect= is kept, once a login link was sent, for the page
  // that link opens (BRY-575), with the address it went to, and only when it
  // is a path on this site.
  async function requestLink(route: string, ok: boolean) {
    const wrapper = mountWithPlugins(LoginView, { initialRoute: route })
    await flushPromises()
    vi.mocked(useAuthStore().requestMagicLink).mockResolvedValue(ok)
    await wrapper.find('input#email').setValue('kasserer@example.test')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    return wrapper
  }

  it('keeps nothing on mount, before a link is requested', async () => {
    localStorage.clear()
    mountWithPlugins(LoginView, { initialRoute: '/login?redirect=%2Fadmin' })
    await flushPromises()
    expect(localStorage.getItem('brygge.loginRedirect')).toBeNull()
  })

  it('keeps a same-origin ?redirect= with the address once the link is sent', async () => {
    localStorage.clear()
    await requestLink('/login?redirect=%2Fadmin%2Faccounting%2Fjournal%3FopenPeriod%3D2027', true)
    const kept = JSON.parse(localStorage.getItem('brygge.loginRedirect') ?? '{}')
    expect(kept.path).toBe('/admin/accounting/journal?openPeriod=2027')
    expect(kept.email).toBe('kasserer@example.test')
  })

  it('keeps nothing when the request failed', async () => {
    localStorage.clear()
    await requestLink('/login?redirect=%2Fadmin', false)
    expect(localStorage.getItem('brygge.loginRedirect')).toBeNull()
  })

  it('keeps nothing for a ?redirect= off this site, and clears an old one', async () => {
    localStorage.setItem('brygge.loginRedirect', JSON.stringify({ path: '/admin', email: 'kasserer@example.test', at: Date.now() }))
    await requestLink('/login?redirect=%2F%2Fevil.example%2Fx', true)
    expect(localStorage.getItem('brygge.loginRedirect')).toBeNull()
  })
})
