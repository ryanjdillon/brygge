import { describe, it, expect } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import LoginView from '@/views/LoginView.vue'

describe('LoginView', () => {
  it('renders without errors', () => {
    const wrapper = mountWithPlugins(LoginView)
    expect(wrapper.exists()).toBe(true)
  })

  it('renders login heading', () => {
    const wrapper = mountWithPlugins(LoginView)
    expect(wrapper.find('h1').text()).toBe('nav.login')
  })
})
