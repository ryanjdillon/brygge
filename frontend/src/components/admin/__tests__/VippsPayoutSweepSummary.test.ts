import { describe, it, expect } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import VippsPayoutSweepSummary from '@/components/admin/VippsPayoutSweepSummary.vue'

const result = {
  cleared: [{ settlement_number: '3000101', msn: '000001', bank_row_id: 'b1' }],
  waiting: [{ settlement_number: '3000102', msn: '000001', reason: '1 belastninger venter i Tildel' }],
  skipped: [{ settlement_number: '3000103', msn: '000001', reason: 'ingen bankinnbetaling' }],
  failed: [{ settlement_number: '3000104', msn: '000001', reason: 'fiscal period closed' }],
}

describe('VippsPayoutSweepSummary', () => {
  it('lists only the payouts that need attention, with their reason', () => {
    const wrapper = mountWithPlugins(VippsPayoutSweepSummary, { props: { result } })
    const items = wrapper.findAll('li').map((li) => li.text())
    expect(items).toHaveLength(2)
    expect(items[0]).toContain('Utb. 3000103')
    expect(items[0]).toContain('ingen bankinnbetaling')
    expect(items[1]).toContain('fiscal period closed')
    expect(wrapper.text()).not.toContain('3000101')
  })
})
