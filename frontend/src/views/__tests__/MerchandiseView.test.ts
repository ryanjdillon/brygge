import { describe, it, expect, vi } from 'vitest'
import { ref } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import MerchandiseView from '@/views/MerchandiseView.vue'

vi.mock('lucide-vue-next', () => ({
  ShoppingCart: { template: '<span data-icon="shopping-cart" />' },
  Plus: { template: '<span data-icon="plus" />' },
  Check: { template: '<span data-icon="check" />' },
}))

vi.mock('@/composables/useApi', () => ({
  useApi: () => ({
    fetchApi: vi.fn(),
  }),
}))

vi.mock('@tanstack/vue-query', async () => {
  const actual = await vi.importActual('@tanstack/vue-query')
  return {
    ...actual,
    useQuery: () => ({
      data: ref({
        products: [
          { id: '1', name: 'Standervimpel', description: 'Flott vimpel', price: 350, currency: 'NOK', image_url: '', stock: 10 },
          { id: '2', name: 'T-skjorte', description: 'Fin t-skjorte', price: 299, currency: 'NOK', image_url: '', stock: 5 },
        ],
      }),
      isLoading: ref(false),
    }),
  }
})

describe('MerchandiseView', () => {
  it('renders without errors', () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    expect(wrapper.exists()).toBe(true)
  })

  it('renders merchandise heading', () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    expect(wrapper.find('h1').text()).toBe('merchandise.title')
  })

  it('renders product cards', () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    expect(wrapper.text()).toContain('Standervimpel')
    expect(wrapper.text()).toContain('T-skjorte')
  })

  it('displays product prices', () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    expect(wrapper.text()).toContain('350 kr')
    expect(wrapper.text()).toContain('299 kr')
  })
})
