import { describe, it, expect } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import StripeBookSummary from '@/components/admin/StripeBookSummary.vue'

const base = { charges_booked: 2, fees_booked: 1, needs_review: {}, deferred: [] }

describe('StripeBookSummary', () => {
  it('shows what the pass booked', () => {
    const wrapper = mountWithPlugins(StripeBookSummary, { props: { result: base } })
    expect(wrapper.text()).toContain('admin.bankImports.stripeBookCounts')
    expect(wrapper.find('[data-testid="stripe-book-review"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="stripe-book-deferred"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="stripe-book-errors"]').exists()).toBe(false)
  })

  it('reports review, deferred and failed rows', () => {
    const wrapper = mountWithPlugins(StripeBookSummary, {
      props: {
        result: {
          ...base,
          needs_review: { split_mismatch: 1, receivable_not_open: 2 },
          deferred: [{ balance_transaction_id: 'txn_test_1', date: '2025-07-01', reason: 'period_closed' }],
          errors: ['txn_test_2: boom'],
        },
      },
    })
    expect(wrapper.find('[data-testid="stripe-book-review"]').text()).toContain('admin.bankImports.stripeBookReview')
    expect(wrapper.find('[data-testid="stripe-book-deferred"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="stripe-book-errors"]').exists()).toBe(true)
  })

  it('says when a running Stripe sync kept the pass from booking', () => {
    const wrapper = mountWithPlugins(StripeBookSummary, { props: { result: { ...base, charges_booked: 0, fees_booked: 0, busy: true } } })
    expect(wrapper.find('[data-testid="stripe-book-busy"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('admin.bankImports.stripeBookCounts')
  })

  it('shows a pass that failed', () => {
    const wrapper = mountWithPlugins(StripeBookSummary, {
      props: { result: { ...base, error: 'Bokføring av Stripe-transaksjoner feilet' } },
    })
    expect(wrapper.find('[data-testid="stripe-book-error"]').text()).toContain('Bokføring av Stripe-transaksjoner feilet')
  })
})
