import { describe, it, expect, vi } from 'vitest'
import { computed, ref, type Ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import BankReconcileTab from '@/components/admin/BankReconcileTab.vue'
import type { AccountSuggestion, BankRowSuggestions, BankRowSummary } from '@/composables/useBankReconcile'

// Two synthetic incoming rows, each with its own account suggestion.
const { icon, rows, suggestionsByRow } = vi.hoisted(() => {
  const row = (id: string, amount: number) => ({
    id,
    row_date: '2026-09-01',
    amount,
    kid_number: '',
    reference: 'SYN' + id,
    counterpart: 'Ola Testmann',
    description: 'Synthetic deposit ' + id,
    bank_account_code: '1920',
    likely_duplicate_of_matched: false,
    possible_double_payment: false,
    suggestion_count: 1,
    vipps_payout: false,
    provisional: false,
    partner_hint: null,
  })
  const account = (code: string, name: string) => ({
    code,
    name,
    account_type: 'revenue',
    score: 60,
    confidence_label: 'sannsynleg' as const,
    why_tooltip: 'synthetic',
  })
  return {
    icon: { template: '<span />' },
    rows: [row('a', 800), row('b', 450)] as BankRowSummary[],
    suggestionsByRow: {
      a: { invoices: [], accounts: [account('3300', 'Salgsinntekter')] },
      b: { invoices: [], accounts: [account('3900', 'Andre inntekter')] },
    } as Record<string, { invoices: []; accounts: AccountSuggestion[] }>,
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
    fetchBankRowPartners: vi.fn(async () => []),
    useBankUnmatchedRows: () => ({ data: ref(rows), isLoading: ref(false) }),
    // Answers per row id, the way the real query is keyed.
    useBankRowSuggestions: (id: Ref<string | null>) => ({
      data: computed<BankRowSuggestions | undefined>(() => (id.value ? suggestionsByRow[id.value] : undefined)),
    }),
    useBankUnmatchedCountsByYear: () => ({ data: ref({}) }),
    useMissingVippsSettlements: () => ({ data: ref([]) }),
    useReconcileMutations: () => ({
      assignInvoice: mutation(),
      assignAccount: mutation(),
      assignAccountGroup: mutation(),
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
      { code: '3300', name: 'Salgsinntekter', account_type: 'revenue', is_active: true },
      { code: '3900', name: 'Andre inntekter', account_type: 'revenue', is_active: true },
    ]),
  }),
}))

function mountTab() {
  return mountWithPlugins(BankReconcileTab, { global: { stubs: { teleport: true } } })
}

async function focusRow(wrapper: ReturnType<typeof mountTab>, idx: number) {
  const toggles = wrapper.findAll('button').filter((b) => b.text().includes('admin.bankReconcile.showSuggestions'))
  await toggles[idx].trigger('click')
}

function suggestedCodes(wrapper: ReturnType<typeof mountTab>): string[] {
  return wrapper
    .findAll('[data-testid^="account-suggestion-"]')
    .map((el) => el.attributes('data-testid')!.replace('account-suggestion-', ''))
}

describe('BankReconcileTab Lag bilag suggestions', () => {
  it('shows the suggestions of the row the picker opened on, not the expanded row', async () => {
    const wrapper = mountTab()
    await focusRow(wrapper, 0)

    // Row B picked from the bulk toolbar while row A stays expanded.
    await wrapper.findAll('input[type="checkbox"]')[1].setValue(true)
    const lagBilagButtons = wrapper.findAll('button').filter((b) => b.text().includes('admin.bankReconcile.lagBilag'))
    const toolbarLagBilag = lagBilagButtons[lagBilagButtons.length - 1]
    await toolbarLagBilag.trigger('click')
    await flushPromises()

    expect(suggestedCodes(wrapper)).toEqual(['3900'])
  })

  it('shows the expanded row its own suggestions', async () => {
    const wrapper = mountTab()
    await focusRow(wrapper, 0)
    const footerLagBilag = wrapper.findAll('button').find((b) => b.text().includes('admin.bankReconcile.lagBilag'))!
    await footerLagBilag.trigger('click')
    await flushPromises()

    expect(suggestedCodes(wrapper)).toEqual(['3300'])
  })
})
