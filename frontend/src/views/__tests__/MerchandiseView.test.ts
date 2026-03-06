import { describe, it, expect, vi } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import MerchandiseView from '@/views/MerchandiseView.vue'

vi.mock('lucide-vue-next', () => ({
  ShoppingBag: { template: '<span data-icon="shopping-bag" />' },
}))

describe('MerchandiseView', () => {
  it('renders without errors', () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    expect(wrapper.exists()).toBe(true)
  })

  it('renders merchandise heading', () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    expect(wrapper.find('h1').text()).toBe('merchandise.title')
  })

  it('shows coming soon notice', () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    expect(wrapper.text()).toContain('merchandise.comingSoon')
  })

  it('renders product cards', () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    expect(wrapper.text()).toContain('merchandise.burgee')
    expect(wrapper.text()).toContain('merchandise.tshirt')
    expect(wrapper.text()).toContain('merchandise.cap')
  })

  it('displays product prices', () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    expect(wrapper.text()).toContain('350 kr')
    expect(wrapper.text()).toContain('299 kr')
    expect(wrapper.text()).toContain('199 kr')
  })
})
