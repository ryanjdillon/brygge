import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import ClearedSettlementRefusal from '@/components/admin/ClearedSettlementRefusal.vue'
import { ApiError } from '@/lib/errors'
import { asClearedSettlementRefusal } from '@/composables/useClearedSettlement'

const { unassignBankRow, ensureFreshTotp, askConfirm } = vi.hoisted(() => ({
  unassignBankRow: vi.fn(),
  ensureFreshTotp: vi.fn(),
  askConfirm: vi.fn(),
}))

vi.mock('@/composables/useBankImports', () => ({ unassignBankRow: (id: string) => unassignBankRow(id) }))
vi.mock('@/composables/useFreshTotp', () => ({ useFreshTotp: () => ({ ensureFreshTotp }) }))
vi.mock('@/stores/confirm', () => ({ useConfirm: () => askConfirm }))
vi.mock('@tanstack/vue-query', async () => {
  const actual = await vi.importActual('@tanstack/vue-query')
  return { ...actual, useQueryClient: () => ({ invalidateQueries: vi.fn() }) }
})

const refusal = { settlementNumber: '3000101', msn: '000001', bankRowId: 'bank-1', message: 'refused' }

describe('ClearedSettlementRefusal', () => {
  beforeEach(() => {
    unassignBankRow.mockReset().mockResolvedValue({ status: 'unassigned' })
    ensureFreshTotp.mockReset().mockResolvedValue(true)
    askConfirm.mockReset().mockResolvedValue(true)
  })

  it('undoes the deposit after the step-up and a confirmation that lists what it undoes', async () => {
    const wrapper = mountWithPlugins(ClearedSettlementRefusal, { props: { refusal } })
    expect(wrapper.text()).toContain('admin.accounting.clearedSettlement.refused')
    await wrapper.get('[data-testid="cleared-settlement-undo"]').trigger('click')
    await flushPromises()

    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(askConfirm).toHaveBeenCalledTimes(1)
    expect(askConfirm.mock.calls[0][0].details).toHaveLength(3)
    expect(unassignBankRow).toHaveBeenCalledWith('bank-1')
    expect(wrapper.emitted('undone')).toHaveLength(1)
  })

  it('does nothing without the step-up or when the confirmation is declined', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const wrapper = mountWithPlugins(ClearedSettlementRefusal, { props: { refusal } })
    await wrapper.get('[data-testid="cleared-settlement-undo"]').trigger('click')
    await flushPromises()
    expect(askConfirm).not.toHaveBeenCalled()

    ensureFreshTotp.mockResolvedValue(true)
    askConfirm.mockResolvedValue(false)
    await wrapper.get('[data-testid="cleared-settlement-undo"]').trigger('click')
    await flushPromises()
    expect(unassignBankRow).not.toHaveBeenCalled()
    expect(wrapper.emitted('undone')).toBeUndefined()
  })

  it('shows the failure and does not report the clearing undone', async () => {
    unassignBankRow.mockRejectedValue(new Error('Synthetic failure'))
    const wrapper = mountWithPlugins(ClearedSettlementRefusal, { props: { refusal } })
    await wrapper.get('[data-testid="cleared-settlement-undo"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="cleared-settlement-error"]').text()).toBe('Synthetic failure')
    expect(wrapper.emitted('undone')).toBeUndefined()
  })

  it('offers no button when no bank row carries the clearing', () => {
    const wrapper = mountWithPlugins(ClearedSettlementRefusal, { props: { refusal: { ...refusal, bankRowId: '' } } })
    expect(wrapper.find('[data-testid="cleared-settlement-undo"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('admin.accounting.clearedSettlement.noDeposit')
  })
})

describe('asClearedSettlementRefusal', () => {
  it('reads the refusal from a 409 charge_in_cleared_settlement', () => {
    const e = new ApiError(409, 'refused', 'charge_in_cleared_settlement', {
      settlement_number: '3000101', msn: '000001', bank_row_id: 'bank-1',
    })
    expect(asClearedSettlementRefusal(e)).toEqual({ settlementNumber: '3000101', msn: '000001', bankRowId: 'bank-1', message: 'refused' })
  })

  it('ignores every other error', () => {
    expect(asClearedSettlementRefusal(new ApiError(409, 'x', 'charge_in_settlement'))).toBeNull()
    expect(asClearedSettlementRefusal(new ApiError(400, 'x', 'charge_in_cleared_settlement'))).toBeNull()
    expect(asClearedSettlementRefusal(new Error('x'))).toBeNull()
  })
})
