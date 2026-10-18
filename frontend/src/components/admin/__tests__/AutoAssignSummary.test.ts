import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import AutoAssignSummary from '@/components/admin/AutoAssignSummary.vue'

const unassignVipps = vi.fn()
const unassignBank = vi.fn()
const ensureFreshTotp = vi.fn()

vi.mock('@/composables/useBankImports', () => ({
  unassignVippsCharge: (id: string) => unassignVipps(id),
  unassignBankRow: (id: string) => unassignBank(id),
}))

vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp: () => ensureFreshTotp() }),
}))

vi.mock('@tanstack/vue-query', async () => {
  const actual = await vi.importActual('@tanstack/vue-query')
  return { ...actual, useQueryClient: () => ({ invalidateQueries: vi.fn() }) }
})

const vippsResult = {
  enabled: true,
  needs_review: 3,
  assigned: [
    { row_id: 'r1', date: '2026-07-03', amount: 1600, rule: 'price_item' as const, target: '3300', entry_id: 'e1' },
    { row_id: 'r2', date: '2026-07-04', amount: 450, rule: 'member_invoice' as const, target: 'faktura 7', entry_id: 'e2' },
  ],
}

const bankResult = {
  source: 'bank' as const,
  enabled: true,
  needs_review: 2,
  by_reason: { no_strong_member: 2 },
  assigned: [
    {
      source: 'bank' as const,
      row_id: 'b1',
      date: '2026-07-05',
      amount: 450,
      rule: 'member_invoice' as const,
      target: 'faktura 9',
      entry_id: 'e3',
      invoice_number: 9,
      match_pass: 'full_name',
    },
  ],
}

describe('AutoAssignSummary', () => {
  beforeEach(() => {
    unassignVipps.mockReset().mockResolvedValue({ status: 'unassigned' })
    unassignBank.mockReset().mockResolvedValue({ status: 'unassigned' })
    ensureFreshTotp.mockReset().mockResolvedValue(true)
  })

  it('lists Vipps bookings and undoes one through the charge endpoint', async () => {
    const wrapper = mountWithPlugins(AutoAssignSummary, { props: { result: vippsResult, source: 'vipps' } })
    expect(wrapper.text()).toContain('faktura 7')
    expect(wrapper.findAll('li')).toHaveLength(2)

    await wrapper.findAll('button')[0].trigger('click')
    await flushPromises()
    expect(unassignVipps).toHaveBeenCalledWith('r1')
    expect(unassignBank).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.bankImports.autoAssignUndone')
  })

  it('undoes a bank deposit through the bank row endpoint after a fresh TOTP', async () => {
    const wrapper = mountWithPlugins(AutoAssignSummary, { props: { result: bankResult, source: 'bank' } })
    expect(wrapper.text()).toContain('admin.bankImports.bankAutoAssignCounts')
    expect(wrapper.text()).toContain('faktura 9')

    await wrapper.find('button').trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(unassignBank).toHaveBeenCalledWith('b1')
    expect(unassignVipps).not.toHaveBeenCalled()
  })

  it('does nothing when the step-up is declined', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const wrapper = mountWithPlugins(AutoAssignSummary, { props: { result: bankResult, source: 'bank' } })
    await wrapper.find('button').trigger('click')
    await flushPromises()
    expect(unassignBank).not.toHaveBeenCalled()
  })

  it('shows a run error', () => {
    const wrapper = mountWithPlugins(AutoAssignSummary, {
      props: { result: { ...bankResult, assigned: [], error: 'Synthetic failure' }, source: 'bank' },
    })
    expect(wrapper.text()).toContain('Synthetic failure')
  })

  it('says when auto-assign is off', () => {
    const wrapper = mountWithPlugins(AutoAssignSummary, {
      props: { result: { enabled: false, needs_review: 5, assigned: [] }, source: 'vipps' },
    })
    expect(wrapper.text()).toContain('admin.bankImports.autoAssignOff')
    expect(wrapper.find('li').exists()).toBe(false)
  })
})
