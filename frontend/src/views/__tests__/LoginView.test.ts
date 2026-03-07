import { describe, it, expect, vi } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import LoginView from '@/views/LoginView.vue'

vi.mock('lucide-vue-next', () => ({
  LogIn: { template: '<span data-icon="login" />' },
  ChevronDown: { template: '<span data-icon="chevron-down" />' },
  ChevronUp: { template: '<span data-icon="chevron-up" />' },
}))

describe('LoginView', () => {
  it('renders without errors', () => {
    const wrapper = mountWithPlugins(LoginView)
    expect(wrapper.exists()).toBe(true)
  })

  it('renders login heading', () => {
    const wrapper = mountWithPlugins(LoginView)
    expect(wrapper.find('h1').text()).toBe('login.title')
  })
})
