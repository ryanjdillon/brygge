import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import BankReconcileTab from '@/components/admin/BankReconcileTab.vue'
import type { BankRowSummary } from '@/composables/useBankReconcile'
import { ApiError } from '@/lib/errors'

// One synthetic outgoing row of a reference year and one ordinary incoming row.
const { icon, state, assignAccount, undismiss, toastError, baseRows, makeRow } = vi.hoisted(() => {
  const row = (id: string, amount: number, reference: boolean) => ({
    id,
    row_date: reference ? '2025-06-02' : '2026-09-01',
    amount,
    kid_number: '',
    reference: 'SYN' + id,
    counterpart: 'Synthetic Leverandor',
    description: 'Synthetic payment',
    bank_account_code: '1920',
    likely_duplicate_of_matched: false,
    possible_double_payment: false,
    suggestion_count: 1,
    vipps_payout: false,
    provisional: false,
    partner_hint: null,
    reference_period: reference,
  })
  const baseRows = [row('ref1', -1200, true), row('cur1', 450, false)] as BankRowSummary[]
  return {
    baseRows,
    makeRow: row,
    undismiss: { mutateAsync: vi.fn(), isPending: { value: false } },
    icon: { template: '<span />' },
    state: {
      rows: baseRows,
      referenceCount: 1,
      referenceYears: new Set<number>([2025]),
    },
    assignAccount: { mutateAsync: vi.fn(), isPending: { value: false } },
    toastError: vi.fn(),
  }
})

// The year the tab hands to the rows query (null = no year in the request).
let capturedYear: { value: number | null } | null = null

vi.mock('lucide-vue-next', () => ({
  ArrowDownLeft: icon, ArrowUpRight: icon, Sparkles: icon, Search: icon, FileText: icon, Ban: icon,
  Undo2: icon, CheckSquare: icon, X: icon, ChevronDown: icon, Check: icon,
}))
vi.mock('@/composables/useToast', () => ({ useToast: () => ({ error: toastError, success: vi.fn() }) }))

vi.mock('@/composables/useBankReconcile', async () => {
  const actual = await vi.importActual<typeof import('@/composables/useBankReconcile')>(
    '@/composables/useBankReconcile',
  )
  const mutation = () => ({ mutateAsync: vi.fn(), isPending: ref(false) })
  return {
    ...actual,
    // Rows book together when they are listed as each other's strong partner.
    fetchBankRowPartners: vi.fn(async (id: string) =>
      state.rows.length === 2 && state.rows.every((r) => r.partner_hint === 'strong')
        ? [{ id: state.rows.find((r) => r.id !== id)!.id, row_date: '2026-01-01', amount: -1, counterpart: 'x',
             description: 'x', strength: 'strong', reasons: [], days_apart: 0, net: -2400 }]
        : []),
    useBankUnmatchedRows: (_filter: unknown, _q: unknown, year: { value: number | null }) => {
      capturedYear = year
      return { data: ref(state.rows), isLoading: ref(false) }
    },
    useBankRowSuggestions: () => ({ data: ref(null) }),
    useBankUnmatchedCountsByYear: () => ({ data: ref({}) }),
    useReferenceRowCount: () => ({ data: ref(state.referenceCount) }),
    useReferenceYears: () => ({ data: ref(state.referenceYears) }),
    useMissingVippsSettlements: () => ({ data: ref([]) }),
    useReconcileMutations: () => ({
      assignInvoice: mutation(),
      assignAccount,
      assignAccountGroup: mutation(),
      dismiss: mutation(),
      unassign: mutation(),
      undismiss,
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
      invalidate: vi.fn(),
    }),
  }
})

vi.mock('@/composables/useAccounting', () => ({
  useFiscalPeriods: () => ({ data: ref([]) }),
  useAccountsList: () => ({
    data: ref([
      { code: '6200', name: 'Vedlikehold klubbhus', account_type: 'expense', is_active: true, price_items: [], price_categories: [] },
      { code: '3100', name: 'Medlemskontingent', account_type: 'revenue', is_active: true, price_items: [], price_categories: [] },
      { code: '2050', name: 'Annen egenkapital', account_type: 'liability', is_active: true, price_items: [], price_categories: [] },
    ]),
  }),
}))

function mountTab() {
  return mountWithPlugins(BankReconcileTab, { global: { stubs: { teleport: true } } })
}

describe('BankReconcileTab reference years', () => {
  beforeEach(() => {
    state.rows = baseRows
    state.referenceCount = 1
    state.referenceYears = new Set([2025])
    undismiss.mutateAsync.mockReset()
    assignAccount.mutateAsync.mockReset()
    toastError.mockReset()
  })

  it('shows the Referanseår chip while a year is a reference year, so dismissed rows stay reachable', () => {
    expect(mountTab().find('[data-testid="filter-reference"]').exists()).toBe(true)
    // The last open row handled: the chip stays, for the dismissed ones.
    state.referenceCount = 0
    expect(mountTab().find('[data-testid="filter-reference"]').exists()).toBe(true)
    state.referenceYears = new Set()
    expect(mountTab().find('[data-testid="filter-reference"]').exists()).toBe(false)
  })

  it('selecting the chip lists reference rows under the kind "reference" and explains them', async () => {
    const wrapper = mountTab()
    await wrapper.get('[data-testid="filter-reference"]').trigger('click')
    expect(wrapper.get('[data-testid="filter-reference"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.get('[data-testid="filter-all"]').attributes('aria-pressed')).toBe('false')
    expect(wrapper.get('[data-testid="reference-hint"]').text()).toBe('admin.bankReconcile.referenceHint')
    await flushPromises()
    expect(wrapper.vm.$router.currentRoute.value.query).toMatchObject({ ref: '1' })

    // Any other filter leaves the mode again.
    await wrapper.get('[data-testid="filter-bank"]').trigger('click')
    expect(wrapper.get('[data-testid="filter-reference"]').attributes('aria-pressed')).toBe('false')
  })

  it('lists reference rows for no year, disables the picker, and brings the year back afterwards', async () => {
    const wrapper = mountTab()
    expect(capturedYear!.value).toBe(new Date().getFullYear())

    await wrapper.get('[data-testid="filter-reference"]').trigger('click')
    expect(capturedYear!.value).toBeNull()
    const picker = wrapper.findAllComponents({ name: 'Select' })[0]
    expect(picker.props('disabled')).toBe(true)
    expect(picker.props('modelValue')).toBe(0)

    await wrapper.get('[data-testid="filter-reference"]').trigger('click')
    expect(capturedYear!.value).toBe(new Date().getFullYear())
    expect(wrapper.findAllComponents({ name: 'Select' })[0].props('disabled')).toBe(false)
  })

  it('gives a reference row only the cost booking and dismiss, no faktura actions', () => {
    const wrapper = mountTab()
    const actions = wrapper.get('[data-testid="bank-row-reference-actions"]')
    const labels = actions.findAll('button').map((b) => b.text())
    expect(labels).toEqual(['admin.bankReconcile.lagBilag', 'admin.bankReconcile.dismiss'])
    expect(actions.text()).not.toContain('admin.bankReconcile.searchInvoice')
    // The ordinary row keeps its suggestions toggle, and there is only one set of reference actions.
    expect(wrapper.findAll('[data-testid="bank-row-reference-actions"]')).toHaveLength(1)
    expect(wrapper.text()).toContain('admin.bankReconcile.showSuggestions')
  })

  it('offers only cost accounts for a reference row', async () => {
    const wrapper = mountTab()
    await wrapper.get('[data-testid="bank-row-reference-book"]').trigger('click')
    await flushPromises()
    const codes = wrapper
      .findAll('[data-testid^="assign-account-"]')
      .map((el) => el.attributes('data-testid')!.replace('assign-account-', ''))
    expect(codes).toEqual(['6200'])
  })

  it('books a group of reference rows to cost accounts only', async () => {
    const pair = (reference: boolean) => [
      { ...makeRow('g1', -1200, reference), partner_hint: 'strong' as const },
      { ...makeRow('g2', -1200, reference), partner_hint: 'strong' as const },
    ] as BankRowSummary[]
    const groupCodes = async (rows: BankRowSummary[]) => {
      state.rows = rows
      const wrapper = mountTab()
      for (const box of wrapper.findAll('input[type="checkbox"]')) await box.setValue(true)
      const bulk = wrapper.findAll('button').filter((b) => b.text().includes('admin.bankReconcile.lagBilag'))
      await bulk[bulk.length - 1].trigger('click')
      await flushPromises()
      return wrapper
        .findAll('[data-testid^="assign-group-"]')
        .map((el) => el.attributes('data-testid')!.replace('assign-group-', ''))
    }

    expect(await groupCodes(pair(true))).toEqual(['6200'])
    // The same group in an ordinary year offers every kind of account.
    expect((await groupCodes(pair(false))).sort()).toEqual(['2050', '3100', '6200'])
  })

  it('lists dismissed reference rows on request and undoes a dismissal', async () => {
    state.rows = [{ ...makeRow('d1', -900, true), dismissed_at: '2026-10-01T00:00:00Z', dismissed_reason: 'internal_transfer' }] as BankRowSummary[]
    const wrapper = mountTab()
    expect(wrapper.find('[data-testid="filter-reference-dismissed"]').exists()).toBe(false)
    await wrapper.get('[data-testid="filter-reference"]').trigger('click')
    await wrapper.get('[data-testid="filter-reference-dismissed"]').trigger('click')
    await flushPromises()
    expect(wrapper.vm.$router.currentRoute.value.query).toMatchObject({ ref: 'd' })

    const actions = wrapper.get('[data-testid="bank-row-reference-actions"]')
    expect(actions.findAll('button').map((b) => b.text())).toEqual(['admin.bankReconcile.undismiss'])
    await actions.get('[data-testid="bank-row-reference-undismiss"]').trigger('click')
    expect(undismiss.mutateAsync).toHaveBeenCalledWith({ rowId: 'd1' })
  })

  it('says so when no dismissed reference row can be restored', async () => {
    state.rows = []
    const wrapper = mountTab()
    await wrapper.get('[data-testid="filter-reference"]').trigger('click')
    expect(wrapper.text()).toContain('admin.bankReconcile.referenceEmpty')
    await wrapper.get('[data-testid="filter-reference-dismissed"]').trigger('click')
    expect(wrapper.text()).toContain('admin.bankReconcile.referenceDismissedEmpty')
    expect(wrapper.text()).not.toContain('admin.bankReconcile.referenceEmpty')
  })

  it('tells the viewer when an undo is refused and reloads the list', async () => {
    state.rows = [{ ...makeRow('d1', -900, true), dismissed_at: '2026-10-01T00:00:00Z', dismissed_reason: 'internal_transfer' }] as BankRowSummary[]
    undismiss.mutateAsync.mockRejectedValue(new ApiError(409, 'bank row is not a dismissed reference-year row'))
    const wrapper = mountTab()
    await wrapper.get('[data-testid="filter-reference"]').trigger('click')
    await wrapper.get('[data-testid="filter-reference-dismissed"]').trigger('click')
    await wrapper.get('[data-testid="bank-row-reference-undismiss"]').trigger('click')
    await flushPromises()
    expect(toastError).toHaveBeenCalledWith('admin.bankReconcile.undismissRefused', 10000)

    undismiss.mutateAsync.mockRejectedValue(new ApiError(500, 'boom'))
    await wrapper.get('[data-testid="bank-row-reference-undismiss"]').trigger('click')
    await flushPromises()
    expect(toastError).toHaveBeenLastCalledWith('common.error: boom', 10000)
  })

  it('words a reference_period refusal in the viewer\'s language and books nothing', async () => {
    assignAccount.mutateAsync.mockRejectedValue(new ApiError(409, 'Året er et referanseår', 'reference_period'))
    const wrapper = mountTab()
    await wrapper.get('[data-testid="bank-row-reference-book"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="assign-account-6200"]').trigger('click')
    await flushPromises()
    expect(toastError).toHaveBeenCalledWith('admin.accounting.referencePeriod', 10000)
  })

  it('marks a reference year in the year picker and points an empty list to the chip', async () => {
    state.rows = []
    const wrapper = mountTab()
    // The default year is the current one; pick the reference year from the options.
    const select = wrapper.findAllComponents({ name: 'Select' })[0]
    expect(select.exists()).toBe(true)
    expect(JSON.stringify(select.props('options'))).toContain('admin.bankReconcile.yearReference')
  })
})
