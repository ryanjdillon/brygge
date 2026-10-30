import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import BankReconcileTab from '@/components/admin/BankReconcileTab.vue'
import { ApiError } from '@/lib/errors'
import type { BankRowSummary } from '@/composables/useBankReconcile'

// A failed suggestion load is shown, not swallowed (BRY-508), and an assign
// refused because the faktura is credited says so and refreshes the list.
const { icon, rows, invoice, assignInvoice, fetchPotential, toast, invalidateBank, invalidateVipps } = vi.hoisted(() => ({
  icon: { template: '<span />' },
  rows: [
    {
      id: 'a',
      row_date: '2026-09-01',
      amount: 300,
      kid_number: '',
      reference: 'SYNa',
      counterpart: 'SYN PAYER',
      description: 'Synthetic deposit',
      bank_account_code: '1920',
      likely_duplicate_of_matched: false,
      possible_double_payment: false,
      suggestion_count: 1,
      vipps_payout: false,
      provisional: false,
      partner_hint: null,
    },
  ] as BankRowSummary[],
  invoice: {
    invoice_id: 'inv-1',
    invoice_number: 1001,
    user_id: 'member-1',
    payer_resolves_here: false,
    member_name: 'Synthetic Fortoy',
    member_email: 'fortoy@example.invalid',
    price_item_name: 'Synthetic membership',
    issue_date: '2026-08-01',
    due_date: '2026-08-31',
    total_amount: 300,
    kid_number: '',
    score: 20,
    confidence_label: 'potensiell' as const,
    why_tooltip: 'synthetic',
  },
  assignInvoice: { mutateAsync: vi.fn(), isPending: { value: false } },
  fetchPotential: vi.fn(),
  toast: { success: vi.fn(), info: vi.fn(), error: vi.fn() },
  invalidateBank: vi.fn(),
  invalidateVipps: vi.fn(),
}))

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
vi.mock('@/composables/useToast', () => ({ useToast: () => toast }))
vi.mock('@/composables/useBankReconcile', async () => {
  const actual = await vi.importActual<typeof import('@/composables/useBankReconcile')>(
    '@/composables/useBankReconcile',
  )
  const mutation = () => ({ mutateAsync: vi.fn(), isPending: ref(false) })
  return {
    ...actual,
    fetchPotentialInvoices: fetchPotential,
    fetchBankRowPartners: vi.fn(async () => []),
    fetchBankRowSuggestions: vi.fn(async () => ({ invoices: [], accounts: [] })),
    useBankUnmatchedRows: () => ({ data: ref(rows), isLoading: ref(false) }),
    useBankRowSuggestions: () => ({ data: ref({ invoices: [invoice], accounts: [] }) }),
    useBankUnmatchedCountsByYear: () => ({ data: ref({}) }),
    useMissingVippsSettlements: () => ({ data: ref([]) }),
    useReconcileMutations: () => ({
      assignInvoice,
      assignAccount: mutation(),
      assignAccountGroup: mutation(),
      dismiss: mutation(),
      unassign: mutation(),
      invalidate: invalidateBank,
    }),
    useAssignMultiInvoiceMutation: () => mutation(),
    useVippsPerChargeEnabled: () => ({ data: ref(false) }),
    useUnreconciledVippsCharges: () => ({ data: ref([]) }),
    useVippsChargeMutations: () => ({
      assignInvoice: mutation(),
      bilag: mutation(),
      dismiss: mutation(),
      clearPayout: mutation(),
      invalidate: invalidateVipps,
    }),
  }
})
vi.mock('@/composables/useAccounting', () => ({
  useFiscalPeriods: () => ({ data: ref([]) }), useAccountsList: () => ({ data: ref([]) }) }))

async function openSearch() {
  const wrapper = mountWithPlugins(BankReconcileTab, { global: { stubs: { teleport: true } } })
  await wrapper.findAll('button').find((b) => b.text().includes('admin.bankReconcile.showSuggestions'))!.trigger('click')
  await flushPromises()
  const btn = wrapper.findAll('button').find((b) => b.text().includes('admin.bankReconcile.searchInvoice'))!
  await btn.trigger('click')
  await flushPromises()
  return wrapper
}

describe('BankReconcileTab faktura search', () => {
  beforeEach(() => {
    fetchPotential.mockReset()
    assignInvoice.mutateAsync.mockReset()
    invalidateBank.mockReset()
    invalidateVipps.mockReset()
    Object.values(toast).forEach((f) => f.mockReset())
  })

  it('shows a failed load with a retry instead of an empty list', async () => {
    fetchPotential.mockRejectedValueOnce(new ApiError(500, 'internal error'))
    fetchPotential.mockResolvedValueOnce([invoice])
    const wrapper = await openSearch()
    expect(wrapper.find('[data-testid="suggestions-load-error"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('admin.bankReconcile.noPotential')

    await wrapper.get('[data-testid="suggestions-load-retry"]').trigger('click')
    await flushPromises()
    expect(fetchPotential).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-testid="suggestions-load-error"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('#1001')
  })

  it.each([
    ['invoice_credited', 'admin.bankReconcile.invoiceCreditedRefused'],
    ['invoice_not_debt', 'admin.bankReconcile.invoiceNotDebtRefused'],
    ['invoice_voided', 'admin.bankReconcile.invoiceVoidedRefused'],
  ])('refreshes the suggestion panel after a %s refusal', async (code, message) => {
    assignInvoice.mutateAsync.mockRejectedValueOnce(new ApiError(409, 'refused', code))
    const wrapper = mountWithPlugins(BankReconcileTab, { global: { stubs: { teleport: true } } })
    await wrapper.findAll('button').find((b) => b.text().includes('admin.bankReconcile.showSuggestions'))!.trigger('click')
    await flushPromises()
    const assign = wrapper.findAll('button').find((b) => b.text() === 'admin.bankReconcile.assign')!
    await assign.trigger('click')
    await flushPromises()

    expect(toast.error).toHaveBeenCalledWith(message, 10000)
    expect(invalidateBank).toHaveBeenCalled()
    expect(invalidateVipps).toHaveBeenCalled()
  })

  it('says a credited faktura cannot be assigned and reloads the list', async () => {
    fetchPotential.mockResolvedValue([invoice])
    assignInvoice.mutateAsync.mockRejectedValueOnce(new ApiError(409, 'credited', 'invoice_credited'))
    const wrapper = await openSearch()
    const assign = wrapper.findAll('button').filter((b) => b.text() === 'admin.bankReconcile.assign').pop()!
    await assign.trigger('click')
    await flushPromises()

    expect(toast.error).toHaveBeenCalledWith('admin.bankReconcile.invoiceCreditedRefused', 10000)
    expect(fetchPotential).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('admin.bankReconcile.potentialTitle')
  })
})
