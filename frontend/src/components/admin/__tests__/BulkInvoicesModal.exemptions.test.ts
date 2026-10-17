import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import BulkInvoicesModal from '@/components/admin/BulkInvoicesModal.vue'
import LineItemPicker from '@/components/admin/LineItemPicker.vue'

// Bulk faktura exemption preview (BRY-351): the run is previewed first, the
// operator sees the role exemptions it would apply, and unticked ones are
// sent back as skip_exemptions so those lines are billed in full.

const totpAwareFetch = vi.fn()
vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp: async () => true, totpAwareFetch }),
}))

function json(body: unknown) {
  return Promise.resolve(new Response(JSON.stringify(body), { status: 200 }))
}

const exemptions = [
  { user_id: 'u-treasurer', price_item_id: 'slip-a', description: 'Synthetic slip', amount: 2600, role: 'treasurer' },
  { user_id: 'u-chair', price_item_id: 'slip-a', description: 'Synthetic slip', amount: 2600, role: 'chair' },
]

async function mountReady() {
  const wrapper = mountWithPlugins(BulkInvoicesModal, {
    props: { userIds: ['u-treasurer', 'u-chair'], userNamesById: { 'u-treasurer': 'Member A', 'u-chair': 'Member B' } },
    global: { stubs: { LineItemPicker: true, teleport: true } },
  })
  await flushPromises()
  wrapper.findComponent(LineItemPicker).vm.$emit('update:tier-categories', ['slip_fee'])
  await flushPromises()
  return wrapper
}

describe('BulkInvoicesModal exemption preview', () => {
  beforeEach(() => {
    totpAwareFetch.mockReset()
    vi.stubGlobal('fetch', vi.fn(() => json([{ id: 'p2026', year: 2026, status: 'open' }])))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('lists exemptions and sends unticked ones as skip_exemptions', async () => {
    totpAwareFetch
      .mockImplementationOnce(() => json({ created: [], skipped: [], exemptions, warnings: [] }))
      .mockImplementationOnce(() => json({ created: [], skipped: [], exemptions: [exemptions[0]] }))
    const wrapper = await mountReady()

    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(JSON.parse(totpAwareFetch.mock.calls[0][1].body).preview).toBe(true)
    const boxes = wrapper.findAll('[data-testid="exemption-apply"]')
    expect(boxes).toHaveLength(2)

    await boxes[1].setValue(false)
    await wrapper.find('[data-testid="bulk-confirm"]').trigger('click')
    await flushPromises()
    const body = JSON.parse(totpAwareFetch.mock.calls[1][1].body)
    expect(body.preview).toBeUndefined()
    expect(body.skip_exemptions).toEqual(['u-chair:slip-a'])
    expect(wrapper.find('[data-testid="bulk-exemptions-applied"]').exists()).toBe(true)
  })

  it('creates straight away when nothing is exempted', async () => {
    totpAwareFetch
      .mockImplementationOnce(() => json({ created: [], skipped: [], exemptions: [] }))
      .mockImplementationOnce(() => json({ created: [], skipped: [], exemptions: [] }))
    const wrapper = await mountReady()

    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(totpAwareFetch).toHaveBeenCalledTimes(2)
    expect(JSON.parse(totpAwareFetch.mock.calls[1][1].body).skip_exemptions).toEqual([])
    expect(wrapper.find('[data-testid="bulk-exemption-preview"]').exists()).toBe(false)
  })

  it('keys each boat separately so one of two boats can be unticked', async () => {
    const boat = (n: number) => ({
      key: `u-a:slip-a:boat-${n}`,
      user_id: 'u-a',
      price_item_id: 'slip-a',
      boat_id: `boat-${n}`,
      description: 'Synthetic slip',
      amount: 2600,
      role: 'treasurer',
    })
    totpAwareFetch
      .mockImplementationOnce(() => json({ created: [], skipped: [], exemptions: [boat(1), boat(2)], warnings: [] }))
      .mockImplementationOnce(() => json({ created: [], skipped: [], exemptions: [boat(1)] }))
    const wrapper = await mountReady()

    await wrapper.find('form').trigger('submit')
    await flushPromises()
    const boxes = wrapper.findAll('[data-testid="exemption-apply"]')
    expect(boxes).toHaveLength(2)

    await boxes[1].setValue(false)
    expect((boxes[0].element as HTMLInputElement).checked).toBe(true)
    await wrapper.find('[data-testid="bulk-confirm"]').trigger('click')
    await flushPromises()
    expect(JSON.parse(totpAwareFetch.mock.calls[1][1].body).skip_exemptions).toEqual(['u-a:slip-a:boat-2'])
  })
})
