import { describe, it, expect } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import VippsPayoutSweepSummary from '@/components/admin/VippsPayoutSweepSummary.vue'

const result = {
  cleared: [{ settlement_number: '3000101', msn: '000001', bank_row_id: 'b1' }],
  waiting: [{ settlement_number: '3000102', msn: '000001', reason: '1 belastning venter i Tildel' }],
  skipped: [{ settlement_number: '3000103', msn: '000001', reason: 'ingen bankinnbetaling' }],
  failed: [{ settlement_number: '3000104', msn: '000001', reason: 'regnskapsperioden er lukket' }],
}

describe('VippsPayoutSweepSummary', () => {
  it('lists only the payouts that need attention, with their reason', () => {
    const wrapper = mountWithPlugins(VippsPayoutSweepSummary, { props: { result } })
    const items = wrapper.findAll('li').map((li) => li.text())
    expect(items).toHaveLength(2)
    expect(items[0]).toContain('Utb. 3000103')
    expect(items[0]).toContain('ingen bankinnbetaling')
    expect(items[1]).toContain('regnskapsperioden er lukket')
    expect(wrapper.text()).not.toContain('3000101')
  })

  it('shows a sweep that could not run', () => {
    const wrapper = mountWithPlugins(VippsPayoutSweepSummary, {
      props: { result: { cleared: [], waiting: [], skipped: [], failed: [], error: 'Automatisk avstemming feilet' } },
    })
    expect(wrapper.find('[data-testid="vipps-payout-sweep-error"]').text()).toContain('Automatisk avstemming feilet')
  })

  it('shows no error line when the sweep ran', () => {
    const wrapper = mountWithPlugins(VippsPayoutSweepSummary, { props: { result } })
    expect(wrapper.find('[data-testid="vipps-payout-sweep-error"]').exists()).toBe(false)
  })
})
