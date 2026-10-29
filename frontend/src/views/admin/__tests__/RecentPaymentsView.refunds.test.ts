import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import RecentPaymentsView from '@/views/admin/RecentPaymentsView.vue'

const { fetchApi } = vi.hoisted(() => ({ fetchApi: vi.fn() }))
vi.mock('@/composables/useApi', () => ({ useApi: () => ({ fetchApi }) }))

const payment = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  paid_at: '2026-06-01T10:00:00Z',
  amount: 450,
  payer: 'Synthetic Payer',
  summary: '',
  kind: 'guest',
  channel: 'stripe',
  method: 'card',
  card_brand: 'visa',
  refund_state: 'none',
  refunded_amount: 0,
  dispute_status: null,
  dispute_due_by: null,
  ...extra,
})

async function mountList(body: Record<string, unknown>) {
  fetchApi.mockReset()
  fetchApi.mockResolvedValue(body)
  const w = mountWithPlugins(RecentPaymentsView)
  await flushPromises()
  return w
}

const kinds = (w: ReturnType<typeof mountWithPlugins>) =>
  w.findAll('[data-testid="refund-state-chip"]').map((c) => c.attributes('data-kind'))

describe('RecentPaymentsView refunds and disputes', () => {
  beforeEach(() => localStorage.clear())

  it('marks refunded, partly refunded and disputed payments, and nothing on the rest', async () => {
    const w = await mountList({
      payments: [
        payment('plain'),
        payment('full', { refund_state: 'full', refunded_amount: 450 }),
        payment('part', { refund_state: 'partial', refunded_amount: 100 }),
        payment('disp', { dispute_status: 'needs_response', dispute_due_by: '2026-11-03T12:00:00Z' }),
        payment('lost', { dispute_status: 'lost' }),
        payment('closed', { dispute_status: 'warning_closed' }),
      ],
    })
    const rows = w.findAll('li')
    expect(rows).toHaveLength(6)
    expect(rows.map((r) => r.findAll('[data-testid="refund-state-chip"]').map((c) => c.attributes('data-kind')))).toEqual([
      [],
      ['refunded'],
      ['partial'],
      ['disputed'],
      ['dispute_lost'],
      [],
    ])
    // The method chip stays on every row.
    expect(w.findAll('[data-testid="payment-method-chip"]')).toHaveLength(6)
  })

  it('shows the amount of a partial refund and keeps the amount paid', async () => {
    const w = await mountList({ payments: [payment('part', { refund_state: 'partial', refunded_amount: 100 })] })
    expect(w.get('[data-testid="refund-state-chip"]').text()).toContain('admin.refund.chip.partialAmount')
    expect(w.get('li').text()).toMatch(/450/)
  })

  it('links the chips to Stripe when the server sent the addresses', async () => {
    const w = await mountList({
      payments: [
        payment('p', {
          refund_state: 'full',
          refunded_amount: 450,
          payment_url: 'https://dashboard.stripe.com/payments/pi_1',
          dispute_status: 'under_review',
          dispute_url: 'https://dashboard.stripe.com/disputes/dp_1',
        }),
      ],
    })
    const links = w.findAll('a[data-testid="refund-state-chip"]')
    expect(links.map((a) => a.attributes('href'))).toEqual([
      'https://dashboard.stripe.com/payments/pi_1',
      'https://dashboard.stripe.com/disputes/dp_1',
    ])
    expect(kinds(w)).toEqual(['refunded', 'disputed'])
  })

  it('offers the disputes waiting for an answer as one link, only when there are some', async () => {
    const url = 'https://dashboard.stripe.com/disputes?statuses[0]=needs_response'
    const w = await mountList({ payments: [payment('p')], open_disputes: 2, open_disputes_url: url })
    const banner = w.get('[data-testid="open-disputes"]')
    expect(banner.attributes('href')).toBe(url)
    expect(banner.attributes('target')).toBe('_blank')
    expect(banner.classes()).toContain('min-h-11')
    expect(banner.text()).toContain('admin.refund.chip.disputesWaiting')

    const none = await mountList({ payments: [payment('p')] })
    expect(none.find('[data-testid="open-disputes"]').exists()).toBe(false)
  })

  it('keeps the chip row wrapping on narrow screens', async () => {
    const w = await mountList({ payments: [payment('full', { refund_state: 'full', refunded_amount: 450 })] })
    const row = w.get('[data-testid="refund-state-chip"]').element.parentElement!
    expect(row.className).toContain('flex-wrap')
  })
})
