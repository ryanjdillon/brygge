import { describe, it, expect, vi } from 'vitest'
import { ref } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import MerchandiseView from '@/views/MerchandiseView.vue'

vi.mock('lucide-vue-next', () => ({
  ShoppingCart: { template: '<span data-icon="shopping-cart" />' },
  Plus: { template: '<span data-icon="plus" />' },
  Check: { template: '<span data-icon="check" />' },
  X: { template: '<span data-icon="x" />' },
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
          { id: '1', name: 'Klubbvimpel', description: 'Fin vimpel', price: 350, currency: 'NOK', image_url: '/images/products/vimpel.jpg', stock: 25, variants: [] },
          { id: '2', name: 'T-skjorte', description: 'Brygge-logo', price: 299, currency: 'NOK', image_url: '/images/products/tskjorte-hvit.jpg', stock: 0, variants: [
            { id: 'v1', size: 'S', color: 'Hvit', stock: 10, price_override: null, image_url: '/images/products/tskjorte-hvit.jpg', sort_order: 0 },
            { id: 'v2', size: 'M', color: 'Hvit', stock: 0, price_override: null, image_url: '/images/products/tskjorte-hvit.jpg', sort_order: 1 },
            { id: 'v3', size: 'S', color: 'Navy', stock: 8, price_override: null, image_url: '/images/products/tskjorte-navy.jpg', sort_order: 2 },
          ] },
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

  it('renders product names in the page', () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    const text = wrapper.text()
    expect(text).toContain('Klubbvimpel')
    expect(text).toContain('T-skjorte')
  })

  it('renders one card per product', () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    const cards = wrapper.findAll('button').filter((b) => b.text().includes('Klubbvimpel') || b.text().includes('T-skjorte'))
    expect(cards.length).toBe(2)
  })

  it('renders product descriptions', () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    expect(wrapper.text()).toContain('Fin vimpel')
    expect(wrapper.text()).toContain('Brygge-logo')
  })

  it('displays product prices', () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    expect(wrapper.text()).toContain('350 kr')
    expect(wrapper.text()).toContain('299 kr')
  })

  it('does not show empty state when products exist', () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    expect(wrapper.text()).not.toContain('Ingen produkter tilgjengelig')
  })

  it('shows Utsolgt only for out-of-stock products without available variants', () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    const cards = wrapper.findAll('button').filter((b) => b.text().includes('Klubbvimpel') || b.text().includes('T-skjorte'))
    const vimpelCard = cards.find((b) => b.text().includes('Klubbvimpel'))!
    const tshirtCard = cards.find((b) => b.text().includes('T-skjorte'))!
    // Klubbvimpel has stock=25, no variants — should NOT show Utsolgt
    expect(vimpelCard.text()).not.toContain('Utsolgt')
    // T-skjorte has variant v1 with stock=10 — should NOT show Utsolgt
    expect(tshirtCard.text()).not.toContain('Utsolgt')
  })

  it('opens product modal on card click', async () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    const card = wrapper.findAll('button').find((b) => b.text().includes('T-skjorte'))
    await card!.trigger('click')
    expect(wrapper.find('[role="dialog"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('merchandise.size')
  })

  it('shows size options in modal for product with variants', async () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    const card = wrapper.findAll('button').find((b) => b.text().includes('T-skjorte'))
    await card!.trigger('click')
    const dialog = wrapper.find('[role="dialog"]')
    expect(dialog.text()).toContain('S')
    expect(dialog.text()).toContain('M')
  })

  it('disables add-to-cart when no variant selected for variant product', async () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    const card = wrapper.findAll('button').find((b) => b.text().includes('T-skjorte'))
    await card!.trigger('click')
    const dialog = wrapper.find('[role="dialog"]')
    const addBtn = dialog.findAll('button').find((b) => b.text().includes('merchandise.addToCart'))
    expect(addBtn!.attributes('disabled')).toBeDefined()
  })

  it('shows product image on card when image_url is set', () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    const images = wrapper.findAll('img').filter((img) => img.attributes('alt') === 'Klubbvimpel')
    expect(images.length).toBe(1)
    expect(images[0].attributes('src')).toBe('/images/products/vimpel.jpg')
  })

  it('shows product image in modal', async () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    const card = wrapper.findAll('button').find((b) => b.text().includes('T-skjorte'))
    await card!.trigger('click')
    const dialog = wrapper.find('[role="dialog"]')
    const img = dialog.find('img')
    expect(img.exists()).toBe(true)
    expect(img.attributes('src')).toBe('/images/products/tskjorte-hvit.jpg')
  })

  it('switches modal image when selecting a different color', async () => {
    const wrapper = mountWithPlugins(MerchandiseView)
    const card = wrapper.findAll('button').find((b) => b.text().includes('T-skjorte'))
    await card!.trigger('click')
    const dialog = wrapper.find('[role="dialog"]')
    // Click Navy color button
    const navyBtn = dialog.findAll('button').find((b) => b.text().trim() === 'Navy')
    expect(navyBtn).toBeDefined()
    await navyBtn!.trigger('click')
    const img = dialog.find('img')
    expect(img.attributes('src')).toBe('/images/products/tskjorte-navy.jpg')
  })
})
