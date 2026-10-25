import { describe, it, expect, vi, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import HarborTargetsView from '@/views/admin/HarborTargetsView.vue'

vi.mock('lucide-vue-next', () => {
  const icon = { template: '<span />' }
  return { Plus: icon, FileDown: icon, Pencil: icon, Archive: icon, Copy: icon, QrCode: icon, Printer: icon }
})

const target = (id: string, archived = false) => ({
  id,
  token: `tok${id}`,
  label: `Target ${id}`,
  location: '',
  kind: 'guest_slip',
  price_item_id: 'p1',
  item_name: 'Gjesteplass',
  unit_amount: 300,
  currency: 'NOK',
  unit_label: 'natt',
  min_units: 1,
  max_units: 14,
  fixed_units: false,
  archived,
  public_url: `https://club.example.invalid/qr/tok${id}`,
})

async function mountView() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => ({
      ok: true,
      json: async () => (url.includes('pricing') ? [] : [target('a'), target('b'), target('c', true)]),
    })),
  )
  const wrapper = mountWithPlugins(HarborTargetsView)
  await flushPromises()
  return wrapper
}

describe('HarborTargetsView printing', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('offers a checkbox and print button only on active targets', async () => {
    const wrapper = await mountView()
    expect(wrapper.findAll('[data-testid="select-target"]')).toHaveLength(2)
    expect(wrapper.findAll('[data-testid="print-one"]')).toHaveLength(2)
  })

  it('disables print-selected until something is selected', async () => {
    const wrapper = await mountView()
    const btn = wrapper.get('[data-testid="print-selected"]')
    expect(btn.attributes('disabled')).toBeDefined()
    await wrapper.findAll('[data-testid="select-target"]')[0].trigger('change')
    expect(btn.attributes('disabled')).toBeUndefined()
  })

  it('opens the print sheet with the selected ids in list order and the chosen layout', async () => {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    const wrapper = await mountView()
    const boxes = wrapper.findAll('[data-testid="select-target"]')
    await boxes[1].trigger('change')
    await boxes[0].trigger('change')
    await wrapper.get('[data-testid="per-page"]').setValue('6')
    await wrapper.get('[data-testid="print-selected"]').trigger('click')
    expect(open).toHaveBeenCalledWith(
      '/admin/harbor/payment-targets/print?ids=a%2Cb&per=6',
      '_blank',
      'noopener,noreferrer',
    )
  })

  it('select all picks every active target and prints one card from its row', async () => {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    const wrapper = await mountView()
    await wrapper.get('[data-testid="select-all"]').trigger('change')
    const boxes = wrapper.findAll('[data-testid="select-target"]')
    expect(boxes.every((b) => (b.element as HTMLInputElement).checked)).toBe(true)
    expect(wrapper.get('[data-testid="print-selected"]').attributes('disabled')).toBeUndefined()
    await wrapper.findAll('[data-testid="print-one"]')[1].trigger('click')
    expect(open).toHaveBeenCalledWith('/admin/harbor/payment-targets/print?ids=b&per=4', '_blank', 'noopener,noreferrer')
  })
})
