import { describe, it, expect, vi } from 'vitest'
import { ref } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import BankReconcileTab from '@/components/admin/BankReconcileTab.vue'
import type { BankRowSummary } from '@/composables/useBankReconcile'

const { icon, rows } = vi.hoisted(() => {
  const row = (id: string, provisional: boolean) => ({
    id,
    row_date: '2026-09-01',
    amount: 412.65,
    kid_number: '',
    reference: 'SYN' + id,
    counterpart: '',
    description: 'Overførsel',
    bank_account_code: '1920',
    likely_duplicate_of_matched: false,
    possible_double_payment: false,
    suggestion_count: 0,
    vipps_payout: false,
    provisional,
  })
  return { icon: { template: '<span />' }, rows: [row('a', true), row('b', false)] as BankRowSummary[] }
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
    useBankUnmatchedRows: () => ({ data: ref(rows), isLoading: ref(false) }),
    useBankRowSuggestions: () => ({ data: ref(null) }),
    useBankUnmatchedCountsByYear: () => ({ data: ref({}) }),
    useMissingVippsSettlements: () => ({ data: ref([]) }),
    useReconcileMutations: () => ({
      assignInvoice: mutation(),
      assignAccount: mutation(),
      dismiss: mutation(),
      unassign: mutation(),
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
  useAccountsList: () => ({ data: ref([]) }),
}))

describe('BankReconcileTab provisional rows', () => {
  it('badges only the provisional row', () => {
    const wrapper = mountWithPlugins(BankReconcileTab)
    expect(wrapper.findAll('[data-testid="bank-row-provisional"]')).toHaveLength(1)
  })
})
