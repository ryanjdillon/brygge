import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import { ApiError } from '@/lib/errors'
import PricingAdminView from '@/views/admin/PricingAdminView.vue'

const item = {
  id: 'p1',
  category: 'service',
  name: 'Synthetic service',
  description: '',
  amount: 100,
  currency: 'NOK',
  unit: 'once',
  installments_allowed: false,
  max_installments: 1,
  metadata: {},
  sort_order: 0,
  is_active: true,
  pricing_kind: 'flat',
  tier_dimension: null,
  show_in_batch: false,
  show_in_single: true,
  requires_boat_selection: false,
  audience: 'all',
}

const client = { GET: vi.fn(), POST: vi.fn(), PUT: vi.fn(), DELETE: vi.fn() }
const unwrap = vi.fn()
const askConfirm = vi.fn()

vi.mock('@/lib/apiClient', () => ({
  useApiClient: () => client,
  unwrap: (x: unknown) => unwrap(x),
}))

vi.mock('@/stores/confirm', () => ({
  useConfirm: () => askConfirm,
}))

vi.mock('@tanstack/vue-query', async () => {
  const actual = await vi.importActual('@tanstack/vue-query')
  return {
    ...actual,
    useQuery: () => ({
      data: ref({ items: [item, { ...item, id: 'p2', name: 'Retired service', is_active: false }] }),
      isLoading: ref(false),
    }),
    useQueryClient: () => ({ invalidateQueries: vi.fn() }),
    useMutation: () => ({ mutate: vi.fn(), isPending: ref(false) }),
  }
})

async function clickDelete() {
  const wrapper = mountWithPlugins(PricingAdminView)
  const del = wrapper.findAll('button').find((b) => b.classes().includes('hover:text-red-600'))
  await del!.trigger('click')
  await flushPromises()
  return wrapper
}

describe('PricingAdminView delete', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    askConfirm.mockResolvedValue(true)
  })

  it('deletes an unreferenced item after confirming', async () => {
    unwrap.mockReturnValue({ status: 'deleted' })
    await clickDelete()
    expect(client.DELETE).toHaveBeenCalledTimes(1)
    expect(client.PUT).not.toHaveBeenCalled()
    expect(askConfirm).toHaveBeenCalledTimes(1)
  })

  it('offers to archive when the item is in use, and archives on confirm', async () => {
    unwrap
      .mockImplementationOnce(() => {
        throw new ApiError(409, 'in use', 'price_item_in_use')
      })
      .mockReturnValue(item)
    await clickDelete()
    expect(askConfirm).toHaveBeenCalledTimes(2)
    expect(askConfirm.mock.calls[1][0].title).toBe('admin.pricing.inUseTitle')
    expect(client.PUT).toHaveBeenCalledTimes(1)
    const body = client.PUT.mock.calls[0][1].body
    expect(body.is_active).toBe(false)
    expect(body.name).toBe('Synthetic service')
  })

  it('does nothing when the delete is cancelled', async () => {
    askConfirm.mockResolvedValue(false)
    await clickDelete()
    expect(client.DELETE).not.toHaveBeenCalled()
  })
})

describe('PricingAdminView archived section', () => {
  it('hides archived prices until the section is expanded', async () => {
    const wrapper = mountWithPlugins(PricingAdminView)
    expect(wrapper.text()).toContain('Synthetic service')
    expect(wrapper.text()).not.toContain('Retired service')

    const toggle = wrapper.find('button[aria-expanded]')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    await toggle.trigger('click')
    expect(wrapper.text()).toContain('Retired service')
  })
})
