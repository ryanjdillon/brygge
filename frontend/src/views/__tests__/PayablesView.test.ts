import { describe, it, expect, vi, afterEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import PayablesView from '@/views/PayablesView.vue'

vi.mock('lucide-vue-next', () => ({
  Loader2: { template: '<span />' },
  CreditCard: { template: '<span />' },
}))

const payables = {
  club_name: 'Test Club',
  member_name: 'Test Member',
  bank_account: '1234.56.78903',
  invoices: [
    {
      id: 'inv-1',
      invoice_number: 7,
      kid_number: '00000070007',
      total_amount: 450,
      issue_date: '2026-01-01',
      due_date: '2026-02-01',
      paid: false,
      payment_received: false,
      price_item_name: 'Test fee',
      description: '',
      has_pdf: false,
      pay_token: 'paytok7',
    },
  ],
}

describe('PayablesView', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('opens the invoice pay page in the app instead of minting a payment', async () => {
    const fetchMock = vi.fn(async (_url: string) => ({ ok: true, json: async () => payables }))
    vi.stubGlobal('fetch', fetchMock)
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/betal/bruker/:token', component: PayablesView },
        { path: '/betal/:token', component: { template: '<div />' } },
      ],
    })
    await router.push('/betal/bruker/membertok')
    await router.isReady()
    const wrapper = mount(PayablesView, { global: { plugins: [router] } })
    await flushPromises()

    const pay = wrapper.findAll('button').find((b) => b.text().includes('portal.payables.pay'))
    expect(pay).toBeTruthy()
    await pay!.trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.path).toBe('/betal/paytok7')
    expect(fetchMock.mock.calls.some(([url]) => String(url).startsWith('/api/v1/betal/paytok7'))).toBe(false)
  })
})
