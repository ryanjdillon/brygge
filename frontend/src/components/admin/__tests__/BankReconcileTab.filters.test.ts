import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref, type Ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import BankReconcileTab from '@/components/admin/BankReconcileTab.vue'
import { bankRowKindParam, type BankRowFilter } from '@/composables/useBankReconcile'

const { icon } = vi.hoisted(() => ({ icon: { template: '<span />' } }))
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

// The filter ref the tab hands to the rows query; the tests read the
// `kind` it would send to the server.
let capturedFilter: Ref<BankRowFilter> | null = null

vi.mock('@/composables/useBankReconcile', async () => {
  const actual = await vi.importActual<typeof import('@/composables/useBankReconcile')>(
    '@/composables/useBankReconcile',
  )
  const mutation = () => ({ mutateAsync: vi.fn(), isPending: ref(false) })
  return {
    ...actual,
    useBankUnmatchedRows: (filter: Ref<BankRowFilter>) => {
      capturedFilter = filter
      return { data: ref([]), isLoading: ref(false) }
    },
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

function kind(): string {
  return bankRowKindParam(capturedFilter!.value)
}

describe('BankReconcileTab filters', () => {
  beforeEach(() => {
    capturedFilter = null
  })

  it('puts the search box before the filter chips', () => {
    const wrapper = mountWithPlugins(BankReconcileTab)
    const bar = wrapper.get('[data-testid="reconcile-filters"]').element
    const search = bar.querySelector('input[type="search"]')!
    const firstChip = bar.querySelector('[data-testid="filter-all"]')!
    expect(search.compareDocumentPosition(firstChip) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('combines direction, source and flags', async () => {
    const wrapper = mountWithPlugins(BankReconcileTab)
    expect(kind()).toBe('')

    await wrapper.get('[data-testid="filter-incoming"]').trigger('click')
    await wrapper.get('[data-testid="filter-bank"]').trigger('click')
    await wrapper.get('[data-testid="filter-duplicate"]').trigger('click')
    await wrapper.get('[data-testid="filter-double_payment"]').trigger('click')
    expect(kind()).toBe('incoming,bank,duplicate,double_payment')
    expect(wrapper.get('[data-testid="filter-all"]').attributes('aria-pressed')).toBe('false')
  })

  it('keeps one choice per single-select group and clears on re-click', async () => {
    const wrapper = mountWithPlugins(BankReconcileTab)
    await wrapper.get('[data-testid="filter-incoming"]').trigger('click')
    await wrapper.get('[data-testid="filter-outgoing"]').trigger('click')
    expect(kind()).toBe('outgoing')
    await wrapper.get('[data-testid="filter-outgoing"]').trigger('click')
    expect(kind()).toBe('')

    await wrapper.get('[data-testid="filter-bank"]').trigger('click')
    await wrapper.get('[data-testid="filter-vipps"]').trigger('click')
    expect(kind()).toBe('vipps')
  })

  it('"Alle" clears every group', async () => {
    const wrapper = mountWithPlugins(BankReconcileTab)
    await wrapper.get('[data-testid="filter-outgoing"]').trigger('click')
    await wrapper.get('[data-testid="filter-vipps"]').trigger('click')
    await wrapper.get('[data-testid="filter-duplicate"]').trigger('click')
    await wrapper.get('[data-testid="filter-all"]').trigger('click')
    expect(kind()).toBe('')
    expect(wrapper.get('[data-testid="filter-all"]').attributes('aria-pressed')).toBe('true')
  })

  it('mirrors the filter into the URL query', async () => {
    const wrapper = mountWithPlugins(BankReconcileTab)
    await wrapper.get('[data-testid="filter-incoming"]').trigger('click')
    await wrapper.get('[data-testid="filter-bank"]').trigger('click')
    await wrapper.get('[data-testid="filter-duplicate"]').trigger('click')
    await flushPromises()
    const query = wrapper.vm.$router.currentRoute.value.query
    expect(query).toMatchObject({ dir: 'incoming', src: 'bank', flags: 'duplicate' })
  })
})

describe('bankRowKindParam', () => {
  it('serialises groups in a stable order and omits empty ones', () => {
    expect(bankRowKindParam({ direction: null, source: null, flags: [] })).toBe('')
    expect(bankRowKindParam({ direction: 'dismissed', source: 'vipps', flags: ['double_payment'] })).toBe(
      'dismissed,vipps,double_payment',
    )
  })
})
