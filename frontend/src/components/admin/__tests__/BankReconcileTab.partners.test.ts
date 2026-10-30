import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import BankReconcileTab from '@/components/admin/BankReconcileTab.vue'
import type { BankRowSummary, PartnerSuggestion } from '@/composables/useBankReconcile'

// Synthetic buy-back payout and the overpayment returned two days later.
const { icon, rows, partners, assignAccount, assignAccountGroup } = vi.hoisted(() => {
  const row = (id: string, amount: number, date: string) => ({
    id,
    row_date: date,
    amount,
    kid_number: '',
    reference: 'SYN' + id,
    counterpart: 'Kari Testdotter',
    description: 'Synthetic transfer',
    bank_account_code: '1920',
    likely_duplicate_of_matched: false,
    possible_double_payment: false,
    suggestion_count: 0,
    vipps_payout: false,
    provisional: false,
    partner_hint: 'strong' as const,
  })
  const mutation = () => ({ mutateAsync: vi.fn(), isPending: { value: false } })
  return {
    icon: { template: '<span />' },
    rows: [row('out', -63000, '2026-08-31'), row('in', 1000, '2026-09-02')] as BankRowSummary[],
    partners: {} as Record<string, PartnerSuggestion[]>,
    assignAccount: mutation(),
    assignAccountGroup: mutation(),
  }
})

vi.mock('lucide-vue-next', () => ({
  ArrowDownLeft: icon,
  ArrowUpRight: icon,
  Sparkles: icon,
  Search: icon,
  FileText: icon,
  Ban: icon,
  Undo2: icon,
  CheckSquare: icon,
  X: icon,
  ChevronDown: icon,
  Check: icon,
}))

vi.mock('@/composables/useBankReconcile', async () => {
  const actual = await vi.importActual<typeof import('@/composables/useBankReconcile')>(
    '@/composables/useBankReconcile',
  )
  const mutation = () => ({ mutateAsync: vi.fn(), isPending: ref(false) })
  return {
    ...actual,
    fetchBankRowPartners: vi.fn(async (id: string) => partners[id] ?? []),
    useBankUnmatchedRows: () => ({ data: ref(rows), isLoading: ref(false) }),
    useBankRowSuggestions: () => ({ data: ref(null) }),
    useBankUnmatchedCountsByYear: () => ({ data: ref({}) }),
    useMissingVippsSettlements: () => ({ data: ref([]) }),
    useReconcileMutations: () => ({
      assignInvoice: mutation(),
      assignAccount,
      assignAccountGroup,
      dismiss: mutation(),
      unassign: mutation(),
      invalidate: vi.fn(),
    }),
    useAssignMultiInvoiceMutation: mutation,
    useVippsPerChargeEnabled: () => ({ data: ref(false) }),
    useUnreconciledVippsCharges: () => ({ data: ref([]) }),
    useVippsChargeMutations: () => ({
      assignInvoice: mutation(),
      bilag: mutation(),
      dismiss: mutation(),
      clearPayout: mutation(),
    }),
  }
})

vi.mock('@/composables/useAccounting', () => ({
  useFiscalPeriods: () => ({ data: ref([]) }),
  useAccountsList: () => ({
    data: ref([
      { code: '1500', name: 'Kundefordringer', account_type: 'asset', is_active: true },
      { code: '1920', name: 'Bankkonto drift', account_type: 'asset', is_active: true },
      { code: '2050', name: 'Annen egenkapital', account_type: 'liability', is_active: true },
      { code: '3900', name: 'Andre inntekter', account_type: 'revenue', is_active: true },
      { code: '6300', name: 'Leie', account_type: 'expense', is_active: true },
    ]),
  }),
}))

const partner = (id: string, amount: number, strength: 'strong' | 'weak', net: number): PartnerSuggestion => ({
  id,
  row_date: '2026-08-31',
  amount,
  counterpart: 'Kari Testdotter',
  description: 'Synthetic transfer',
  strength,
  reasons: strength === 'strong' ? ['same_account', 'nets_to_price'] : ['nets_to_price'],
  days_apart: 2,
  net,
})

async function openPickerFor(rowId: string) {
  const wrapper = mountWithPlugins(BankReconcileTab, { global: { stubs: { teleport: true } } })
  const idx = rows.findIndex((r) => r.id === rowId)
  await wrapper.findAll('input[type="checkbox"]')[idx].setValue(true)
  const lagBilag = wrapper.findAll('button').find((b) => b.text().includes('admin.bankReconcile.lagBilag'))
  await lagBilag!.trigger('click')
  await flushPromises()
  return wrapper
}

describe('BankReconcileTab partner rows', () => {
  beforeEach(() => {
    assignAccount.mutateAsync.mockReset()
    assignAccountGroup.mutateAsync.mockReset()
    for (const k of Object.keys(partners)) delete partners[k]
  })

  it('badges rows that have a partner', () => {
    const wrapper = mountWithPlugins(BankReconcileTab)
    expect(wrapper.findAll('[data-testid="bank-row-partner-hint"]')).toHaveLength(2)
  })

  it('keeps booking disabled until a strong partner choice is made', async () => {
    partners.in = [partner('out', -63000, 'strong', -62000)]
    const wrapper = await openPickerFor('in')

    expect(wrapper.find('[data-testid="partner-choice"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="partner-choice-required"]').exists()).toBe(true)
    const assign3900 = wrapper.get('[data-testid="assign-account-3900"]')
    expect(assign3900.attributes('disabled')).toBeDefined()
    await assign3900.trigger('click')
    expect(assignAccount.mutateAsync).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="partner-option-alone"]').setValue(true)
    expect(wrapper.get('[data-testid="assign-account-3900"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-testid="assign-account-3900"]').trigger('click')
    expect(assignAccount.mutateAsync).toHaveBeenCalledWith(
      expect.objectContaining({ rowId: 'in', accountCode: '3900', confirmAlone: true }),
    )
  })

  it('books the pair as one bilag for the net on any non-bank account', async () => {
    partners.in = [partner('out', -63000, 'strong', -62000)]
    const wrapper = await openPickerFor('in')

    await wrapper.get('[data-testid="partner-option-out"]').setValue(true)
    const group = wrapper.get('[data-testid="group-accounts"]')
    expect(group.text()).toContain('2050')
    expect(group.text()).not.toContain('1920')
    expect(wrapper.text()).toContain('admin.bankReconcile.partners.groupAccountsHint')

    await wrapper.get('[data-testid="assign-group-2050"]').trigger('click')
    expect(assignAccountGroup.mutateAsync).toHaveBeenCalledWith(
      expect.objectContaining({ rowIds: ['in', 'out'], accountCode: '2050' }),
    )
    expect(assignAccount.mutateAsync).not.toHaveBeenCalled()
  })

  it('does not gate a weak partner', async () => {
    partners.in = [partner('out', -63000, 'weak', -62000)]
    const wrapper = await openPickerFor('in')

    expect(wrapper.find('[data-testid="partner-choice"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="partner-choice-required"]').exists()).toBe(false)
    await wrapper.get('[data-testid="assign-account-3900"]').trigger('click')
    expect(assignAccount.mutateAsync).toHaveBeenCalledWith(
      expect.objectContaining({ rowId: 'in', confirmAlone: false }),
    )
  })

  it('books a selected pair together as one group', async () => {
    partners.in = [partner('out', -63000, 'strong', -62000)]
    partners.out = [{ ...partner('in', 1000, 'strong', -62000), row_date: '2026-09-02' }]
    const wrapper = mountWithPlugins(BankReconcileTab, { global: { stubs: { teleport: true } } })
    const boxes = wrapper.findAll('input[type="checkbox"]')
    await boxes[0].setValue(true)
    await boxes[1].setValue(true)
    await wrapper.findAll('button').find((b) => b.text().includes('admin.bankReconcile.lagBilag'))!.trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="partner-group-banner"]').exists()).toBe(true)
    await wrapper.get('[data-testid="assign-group-2050"]').trigger('click')
    expect(assignAccountGroup.mutateAsync).toHaveBeenCalledWith(
      expect.objectContaining({ rowIds: ['out', 'in'], accountCode: '2050' }),
    )
    expect(assignAccount.mutateAsync).not.toHaveBeenCalled()
  })

  it('gates a group whose row has a strong partner outside the selection until acknowledged', async () => {
    // A↔B are each other's strong partners and both selected; B also has
    // a strong partner C that is not selected.
    partners.out = [{ ...partner('in', 1000, 'strong', -62000), row_date: '2026-09-02' }]
    partners.in = [partner('out', -63000, 'strong', -62000), partner('other', -500, 'strong', 500)]
    const wrapper = mountWithPlugins(BankReconcileTab, { global: { stubs: { teleport: true } } })
    const boxes = wrapper.findAll('input[type="checkbox"]')
    await boxes[0].setValue(true)
    await boxes[1].setValue(true)
    await wrapper.findAll('button').find((b) => b.text().includes('admin.bankReconcile.lagBilag'))!.trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="partner-group-banner"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="partner-outside"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="assign-group-2050"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="assign-group-2050"]').trigger('click')
    expect(assignAccountGroup.mutateAsync).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="partner-ack-separate"]').setValue(true)
    expect(wrapper.get('[data-testid="assign-group-2050"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-testid="assign-group-2050"]').trigger('click')
    expect(assignAccountGroup.mutateAsync).toHaveBeenCalledWith(
      expect.objectContaining({ rowIds: ['out', 'in'], accountCode: '2050' }),
    )
  })

  it('does not offer receivables or bank accounts for a group', async () => {
    partners.in = [partner('out', -63000, 'strong', -62000)]
    const wrapper = await openPickerFor('in')
    await wrapper.get('[data-testid="partner-option-out"]').setValue(true)
    expect(wrapper.find('[data-testid="assign-group-1500"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="assign-group-1920"]').exists()).toBe(false)
  })
})
