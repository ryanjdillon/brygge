import { describe, it, expect, vi, beforeEach } from 'vitest'
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
})
