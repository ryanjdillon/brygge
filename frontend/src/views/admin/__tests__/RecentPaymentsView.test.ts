import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import RecentPaymentsView from '@/views/admin/RecentPaymentsView.vue'

const { fetchApi } = vi.hoisted(() => ({ fetchApi: vi.fn() }))
vi.mock('@/composables/useApi', () => ({ useApi: () => ({ fetchApi }) }))

const payment = (id: string, method: string, cardBrand: string | null, kind = 'guest') => ({
  id,
  paid_at: '2026-06-01T10:00:00Z',
  amount: 450,
  payer: 'Synthetic Payer',
  summary: '',
  kind,
  channel: method === 'vipps' ? 'vipps' : 'stripe',
  method,
  card_brand: cardBrand,
})

describe('RecentPaymentsView payment methods', () => {
  beforeEach(() => {
    fetchApi.mockReset()
    fetchApi.mockResolvedValue({
      payments: [
        payment('p1', 'vipps', null),
        payment('p2', 'card', 'visa'),
        payment('p3', 'apple_pay', 'mastercard'),
        payment('p4', 'google_pay', null),
        payment('p5', 'card', null),
      ],
    })
  })

  it('shows how each payment was made', async () => {
    const wrapper = mountWithPlugins(RecentPaymentsView)
    await flushPromises()
    const chips = wrapper.findAll('[data-testid="payment-method-chip"]')
    expect(chips.map((c) => c.attributes('data-method'))).toEqual(['vipps', 'card', 'apple_pay', 'google_pay', 'card'])
    expect(chips.map((c) => c.text())).toEqual([
      'recentPayments.method.vipps',
      'Visa',
      'recentPayments.method.apple_pay · Mastercard',
      'recentPayments.method.google_pay',
      'recentPayments.method.card',
    ])
  })

  it('keeps the chip row wrapping on narrow screens', async () => {
    const wrapper = mountWithPlugins(RecentPaymentsView)
    await flushPromises()
    const row = wrapper.find('[data-testid="payment-method-chip"]').element.parentElement!
    expect(row.className).toContain('flex-wrap')
  })
})
