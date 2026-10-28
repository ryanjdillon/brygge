import { describe, it, expect } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import StripeReviewList from '@/components/admin/StripeReviewList.vue'

const items = [
  {
    id: 'r1',
    balance_transaction_id: 'txn_test_review_1',
    reporting_category: 'charge',
    review_reason: 'split_mismatch',
    amount_ore: 12300,
    currency: 'nok',
    date: '2026-05-04',
  },
  {
    id: 'r2',
    balance_transaction_id: 'txn_test_review_2',
    reporting_category: 'refund',
    review_reason: 'refund_pending_bry446',
    amount_ore: -5000,
    currency: 'nok',
    date: '2026-05-05',
  },
]

describe('StripeReviewList', () => {
  it('lists each row with its Stripe id, amount and reason', () => {
    const wrapper = mountWithPlugins(StripeReviewList, { props: { items } })
    expect(wrapper.find('h2').text()).toContain('admin.bankImports.stripeReviewTitle')
    const rows = wrapper.findAll('tbody tr').map((tr) => tr.text())
    expect(rows).toHaveLength(2)
    expect(rows[0]).toContain('txn_test_review_1')
    expect(rows[0]).toContain('123')
    expect(rows[0]).toContain('split_mismatch')
    expect(rows[1]).toContain('refund_pending_bry446')
  })

  it('renders nothing when no row waits', () => {
    const wrapper = mountWithPlugins(StripeReviewList, { props: { items: [] } })
    expect(wrapper.find('[data-testid="stripe-review-list"]').exists()).toBe(false)
  })
})
