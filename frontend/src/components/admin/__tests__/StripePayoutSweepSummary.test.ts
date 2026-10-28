import { describe, it, expect } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import StripePayoutSweepSummary from '@/components/admin/StripePayoutSweepSummary.vue'

const empty = { cleared: [], waiting: [], skipped: [], failed: [] }

describe('StripePayoutSweepSummary', () => {
  it('shows the counts and nothing open when all cleared', () => {
    const wrapper = mountWithPlugins(StripePayoutSweepSummary, {
      props: { result: { ...empty, cleared: [{ payout_id: 'po_test_1', amount: 115, journal_entry_id: 'je-1' }] } },
    })
    expect(wrapper.text()).toContain('admin.bankImports.stripePayoutSweepCounts')
    expect(wrapper.find('[data-testid="stripe-payout-sweep-open"]').exists()).toBe(false)
  })

  it('lists waiting, skipped and failed payouts with their reasons', () => {
    const wrapper = mountWithPlugins(StripePayoutSweepSummary, {
      props: {
        result: {
          ...empty,
          waiting: [{ payout_id: 'po_test_2', amount: 50, reason: '1 kortbetaling venter på bokføring' }],
          skipped: [{ amount: 12.34, bank_row_id: 'row-1', bank_date: '2026-06-05', reason: 'ingen Stripe-utbetaling' }],
          failed: [{ payout_id: 'po_test_3', amount: 70, reason: 'kunne ikke bokføres – se loggen' }],
        },
      },
    })
    const items = wrapper.findAll('[data-testid="stripe-payout-sweep-open"] li')
    expect(items).toHaveLength(3)
    expect(items[0].text()).toContain('po_test_2: 1 kortbetaling venter på bokføring')
    expect(items[1].text()).toContain('admin.bankImports.stripePayoutDeposit')
    expect(items[2].text()).toContain('po_test_3')
  })

  it('shows a sweep that failed', () => {
    const wrapper = mountWithPlugins(StripePayoutSweepSummary, {
      props: { result: { ...empty, error: 'Automatisk avstemming av Stripe-utbetalinger feilet' } },
    })
    expect(wrapper.find('[data-testid="stripe-payout-sweep-error"]').text()).toContain('Stripe-utbetalinger feilet')
  })
})
