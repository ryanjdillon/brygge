import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import VippsAutoAssignSummary from '@/components/admin/VippsAutoAssignSummary.vue'

const unassign = vi.fn()

vi.mock('@/composables/useBankImports', () => ({
  unassignVippsCharge: (id: string) => unassign(id),
}))

vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp: () => Promise.resolve(true) }),
}))

vi.mock('@tanstack/vue-query', async () => {
  const actual = await vi.importActual('@tanstack/vue-query')
  return { ...actual, useQueryClient: () => ({ invalidateQueries: vi.fn() }) }
})

const result = {
  enabled: true,
  needs_review: 3,
  assigned: [
    { row_id: 'r1', date: '2026-07-03', amount: 1600, rule: 'price_item' as const, target: '3300', entry_id: 'e1' },
    { row_id: 'r2', date: '2026-07-04', amount: 450, rule: 'member_invoice' as const, target: 'faktura 7', entry_id: 'e2' },
  ],
}

describe('VippsAutoAssignSummary', () => {
  beforeEach(() => {
    unassign.mockReset().mockResolvedValue({ status: 'unassigned' })
  })

  it('lists the bookings and undoes one', async () => {
    const wrapper = mountWithPlugins(VippsAutoAssignSummary, { props: { result } })
    expect(wrapper.text()).toContain('faktura 7')
    expect(wrapper.findAll('li')).toHaveLength(2)

    await wrapper.findAll('button')[0].trigger('click')
    await flushPromises()
    expect(unassign).toHaveBeenCalledWith('r1')
    expect(wrapper.text()).toContain('admin.bankImports.autoAssignUndone')
  })

  it('says when auto-assign is off', () => {
    const wrapper = mountWithPlugins(VippsAutoAssignSummary, {
      props: { result: { enabled: false, needs_review: 5, assigned: [] } },
    })
    expect(wrapper.text()).toContain('admin.bankImports.autoAssignOff')
    expect(wrapper.find('li').exists()).toBe(false)
  })
})
