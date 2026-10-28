import { describe, it, expect, vi, afterEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import PayInvoiceView from '@/views/PayInvoiceView.vue'

vi.mock('lucide-vue-next', () => ({
  Loader2: { template: '<span />' },
  CheckCircle: { template: '<span />' },
  CreditCard: { template: '<span />' },
}))

type State = { providers: string[]; online_blocked?: boolean; bank_account?: string }

function checkoutBody(s: State) {
  return {
    status: 'pending',
    currency: 'NOK',
    invoice: { number: 42, amount: 450, lines: [{ name: 'Test fee', quantity: 1, unit_amount: 450, total: 450 }] },
    lines: [],
    total: 450,
    catalog: [],
    already_paid: false,
    paid_amount: 0,
    online_blocked: s.online_blocked ?? false,
    kid_number: '00000420042',
    bank_account: s.bank_account ?? '',
    providers: s.providers,
  }
}

function stubFetch(
  s: State,
  payResponse?: { ok: boolean; status?: number; body?: unknown },
  afterPay?: State,
) {
  let paid = false
  const fetchMock = vi.fn(async (url: string) => {
    if (url.endsWith('/pay')) {
      paid = true
      const status = payResponse?.status ?? (payResponse?.ok === false ? 502 : 200)
      return { ok: payResponse?.ok ?? true, status, json: async () => payResponse?.body ?? {} }
    }
    return { ok: true, status: 200, json: async () => checkoutBody(paid && afterPay ? afterPay : s) }
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

async function mountPage() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/betal/:token', component: PayInvoiceView }],
  })
  await router.push('/betal/tok123')
  await router.isReady()
  const wrapper = mount(PayInvoiceView, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

const providersShown = (w: Awaited<ReturnType<typeof mountPage>>) =>
  w.findAll('[data-provider]').map((b) => b.attributes('data-provider'))

describe('PayInvoiceView', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('offers a choice when two providers are configured', async () => {
    stubFetch({ providers: ['vipps', 'stripe'] })
    const w = await mountPage()
    expect(providersShown(w)).toEqual(['vipps', 'stripe'])
    expect(w.text()).toContain('pay.methods.chooseTitle')
    expect(w.find('[data-testid="bank-only"]').exists()).toBe(false)
  })

  it('shows a single labelled button with one provider', async () => {
    stubFetch({ providers: ['stripe'] })
    const w = await mountPage()
    expect(providersShown(w)).toEqual(['stripe'])
    expect(w.text()).not.toContain('pay.methods.chooseTitle')
  })

  it('shows the bank box with the over-limit text when the order is blocked', async () => {
    stubFetch({ providers: [], online_blocked: true, bank_account: '1234.56.78903' })
    const w = await mountPage()
    expect(providersShown(w)).toEqual([])
    const box = w.get('[data-testid="bank-only"]')
    expect(box.text()).toContain('1234.56.78903')
    expect(box.text()).toContain('00000420042')
    expect(box.text()).toContain('pay.bankOnlyBody')
    expect(box.text()).not.toContain('pay.bankOnlyBodyNoProvider')
  })

  it('shows the bank box with a neutral text when no provider is configured', async () => {
    stubFetch({ providers: [], bank_account: '1234.56.78903' })
    const w = await mountPage()
    expect(w.get('[data-testid="bank-only"]').text()).toContain('pay.bankOnlyBodyNoProvider')
  })

  it('reloads into the bank box when the order became blocked after loading', async () => {
    stubFetch(
      { providers: ['vipps', 'stripe'] },
      { ok: false, status: 409, body: { error: 'online_payment_over_limit' } },
      { providers: [], online_blocked: true, bank_account: '1234.56.78903' },
    )
    const w = await mountPage()
    await w.get('[data-provider="vipps"]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid="bank-only"]').text()).toContain('1234.56.78903')
    expect(w.find('[data-provider]').exists()).toBe(false)
    expect(w.text()).not.toContain('pay.payError')
  })

  it('posts the chosen provider', async () => {
    const fetchMock = stubFetch({ providers: ['vipps', 'stripe'] }, { ok: true, body: { redirect_url: '' } })
    const w = await mountPage()
    await w.get('[data-provider="stripe"]').trigger('click')
    await flushPromises()
    const call = fetchMock.mock.calls.find(([url]) => String(url).endsWith('/pay')) as unknown as [string, RequestInit]
    expect(call[0]).toBe('/api/v1/checkout/tok123/pay')
    expect(JSON.parse(String(call[1].body))).toEqual({ provider: 'stripe' })
    // No redirect URL came back, so the error shows in the alert region.
    expect(w.get('[role="alert"]').text()).toBe('pay.payError')
  })
})
