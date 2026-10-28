import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import PaymentReceiptView from '@/views/PaymentReceiptView.vue'

vi.mock('lucide-vue-next', () => ({
  Loader2: { template: '<span />' },
  CheckCircle: { template: '<span />' },
  XCircle: { template: '<span />' },
  Clock: { template: '<span />' },
}))

type Answer = { status: string; already_paid: boolean; paid_amount?: number }

function checkoutBody(a: Answer) {
  return {
    status: a.status,
    currency: 'NOK',
    invoice: { number: 42, amount: 450, lines: [] },
    lines: [{ name: 'Test item', quantity: 2, unit_amount: 25, total: 50 }],
    total: 0,
    catalog: [],
    already_paid: a.already_paid,
    paid_amount: a.paid_amount ?? 0,
    online_blocked: false,
    providers: [],
    kid_number: '',
    bank_account: '',
  }
}

// stubAnswers makes each fetch return the next answer, repeating the last.
function stubAnswers(...answers: (Answer | number)[]) {
  let i = 0
  const fetchMock = vi.fn(async () => {
    const a = answers[Math.min(i++, answers.length - 1)]
    if (typeof a === 'number') return { ok: false, status: a, json: async () => ({}) }
    return { ok: true, status: 200, json: async () => checkoutBody(a) }
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

const RECEIPT_PATH = '/betal/tok123/kvittering'

async function mountReceipt() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/betal/:token/kvittering', component: PaymentReceiptView },
      { path: '/betal/:token', component: { template: '<div />' } },
    ],
  })
  await router.push(RECEIPT_PATH)
  await router.isReady()
  const wrapper = mount(PaymentReceiptView, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

async function advance(ms: number) {
  await vi.advanceTimersByTimeAsync(ms)
  await flushPromises()
}

const pending: Answer = { status: 'pending', already_paid: false }
const paid: Answer = { status: 'paid', already_paid: true, paid_amount: 500 }
const failed: Answer = { status: 'failed', already_paid: false }

describe('PaymentReceiptView', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('shows the receipt at once for a paid order', async () => {
    const fetchMock = stubAnswers(paid)
    const wrapper = await mountReceipt()
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/checkout/tok123')
    expect(wrapper.find('[data-testid="receipt-paid"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="receipt-amount"]').text()).toContain('500,00')
    expect(wrapper.find('[data-testid="receipt-invoice"]').exists()).toBe(true)
    await advance(30_000)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('polls with backoff while pending and shows the receipt once paid', async () => {
    const fetchMock = stubAnswers(pending, pending, paid)
    const wrapper = await mountReceipt()
    expect(wrapper.find('[data-testid="receipt-pending"]').exists()).toBe(true)

    await advance(1999)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    await advance(1)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    await advance(2999)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    await advance(1)
    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(wrapper.find('[data-testid="receipt-paid"]').exists()).toBe(true)
  })

  it('tells the payer the receipt will follow when no verdict arrives in time', async () => {
    const fetchMock = stubAnswers(pending)
    const wrapper = await mountReceipt()
    await advance(120_000)
    expect(wrapper.find('[data-testid="receipt-confirming"]').exists()).toBe(true)
    // The backoff stays within ~90 s and well under the per-IP limit.
    const calls = fetchMock.mock.calls.length
    expect(calls).toBeLessThanOrEqual(16)
    await advance(120_000)
    expect(fetchMock).toHaveBeenCalledTimes(calls)
  })

  it('shows a failed order with a way back to the pay page', async () => {
    stubAnswers(failed)
    const wrapper = await mountReceipt()
    expect(wrapper.find('[data-testid="receipt-failed"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="receipt-retry"]').attributes('href')).toBe('/betal/tok123')
    await advance(120_000)
    expect(wrapper.find('[data-testid="receipt-failed"]').exists()).toBe(true)
  })

  it('switches from failed to paid when a newer payment lands', async () => {
    stubAnswers(failed, paid)
    const wrapper = await mountReceipt()
    expect(wrapper.find('[data-testid="receipt-failed"]').exists()).toBe(true)
    await advance(2000)
    expect(wrapper.find('[data-testid="receipt-paid"]').exists()).toBe(true)
  })

  it('keeps polling through a transient error', async () => {
    stubAnswers(503, paid)
    const wrapper = await mountReceipt()
    expect(wrapper.find('[data-testid="receipt-pending"]').exists()).toBe(true)
    await advance(2000)
    expect(wrapper.find('[data-testid="receipt-paid"]').exists()).toBe(true)
  })

  it('shows not found for an unknown token', async () => {
    const fetchMock = stubAnswers(404)
    const wrapper = await mountReceipt()
    expect(wrapper.find('[data-testid="receipt-not-found"]').exists()).toBe(true)
    await advance(30_000)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})
