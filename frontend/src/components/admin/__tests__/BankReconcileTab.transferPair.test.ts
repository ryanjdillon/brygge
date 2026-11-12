import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import BankReconcileTab from '@/components/admin/BankReconcileTab.vue'
import { transferPairCandidate, type BankRowSummary } from '@/composables/useBankReconcile'
import { ApiError } from '@/lib/errors'

// BRY-672: «Par som overføring» in Tildel's multi-select bar.
const { icon, state, pairTransfer, ensureFreshTotp, askConfirm, toastError, toastSuccess, invalidate, row } = vi.hoisted(() => {
  const row = (id: string, amount: number, over: Partial<BankRowSummary> = {}): BankRowSummary => ({
    id,
    row_date: '2026-06-03',
    amount,
    kid_number: '',
    reference: 'SYN' + id,
    counterpart: '',
    description: 'Synthetic transfer',
    bank_account_code: amount < 0 ? '1920' : '1925',
    likely_duplicate_of_matched: false,
    possible_double_payment: false,
    suggestion_count: 0,
    vipps_payout: false,
    provisional: false,
    partner_hint: null,
    reference_period: false,
    ...over,
  })
  return {
    row,
    icon: { template: '<span />' },
    state: { rows: [] as BankRowSummary[] },
    pairTransfer: { mutateAsync: vi.fn(), isPending: { value: false } },
    ensureFreshTotp: vi.fn(),
    askConfirm: vi.fn(),
    toastError: vi.fn(),
    toastSuccess: vi.fn(),
    invalidate: vi.fn(),
  }
})

vi.mock('lucide-vue-next', () => ({
  ArrowDownLeft: icon, ArrowUpRight: icon, ArrowLeftRight: icon, Sparkles: icon, Search: icon, FileText: icon, Ban: icon,
  Undo2: icon, CheckSquare: icon, X: icon, ChevronDown: icon, Check: icon,
}))
vi.mock('@/composables/useToast', () => ({ useToast: () => ({ error: toastError, success: toastSuccess }) }))
vi.mock('@/composables/useFreshTotp', () => ({ useFreshTotp: () => ({ ensureFreshTotp }) }))
vi.mock('@/stores/confirm', () => ({ useConfirm: () => askConfirm, useConfirmStore: () => ({}) }))

vi.mock('@/composables/useBankReconcile', async () => {
  const actual = await vi.importActual<typeof import('@/composables/useBankReconcile')>(
    '@/composables/useBankReconcile',
  )
  const mutation = () => ({ mutateAsync: vi.fn(), isPending: ref(false) })
  return {
    ...actual,
    useBankUnmatchedRows: () => ({ data: ref(state.rows), isLoading: ref(false) }),
    useBankRowSuggestions: () => ({ data: ref(null) }),
    useBankUnmatchedCountsByYear: () => ({ data: ref({}) }),
    useReferenceRowCount: () => ({ data: ref(0) }),
    useReferenceYears: () => ({ data: ref(new Set<number>()) }),
    useMissingVippsSettlements: () => ({ data: ref([]) }),
    useReconcileMutations: () => ({
      assignInvoice: mutation(),
      assignAccount: mutation(),
      assignAccountGroup: mutation(),
      dismiss: mutation(),
      unassign: mutation(),
      undismiss: mutation(),
      pairTransfer,
      invalidate,
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
      { code: '1920', name: 'Bankkonto drift', account_type: 'asset', is_active: true, price_items: [], price_categories: [] },
      { code: '1925', name: 'Bankkonto høyrente', account_type: 'asset', is_active: true, price_items: [], price_categories: [] },
    ]),
  }),
}))

async function mountSelected(rows: BankRowSummary[]) {
  state.rows = rows
  const wrapper = mountWithPlugins(BankReconcileTab, { global: { stubs: { teleport: true } } })
  for (const box of wrapper.findAll('li input[type="checkbox"]')) await box.setValue(true)
  return wrapper
}

const out = () => row('out', -1500)
const inn = () => row('in', 1500)

describe('transferPairCandidate', () => {
  const cases: [string, BankRowSummary[], boolean][] = [
    ['an opposite, equal pair on different accounts', [out(), inn()], true],
    ['14 days apart', [out(), row('in', 1500, { row_date: '2026-06-17' })], true],
    ['the same account', [out(), row('in', 1500, { bank_account_code: '1920' })], false],
    ['the same sign', [out(), row('in', -1500, { bank_account_code: '1925' })], false],
    ['amounts 1 øre apart', [out(), row('in', 1500.01)], false],
    ['more than 14 days apart', [out(), row('in', 1500, { row_date: '2026-06-18' })], false],
    ['a provisional row', [out(), row('in', 1500, { provisional: true })], false],
    ['a provider payout', [out(), row('in', 1500, { provider_payout: 'vipps', vipps_payout: true })], false],
    ['a reference-year row', [row('out', -1500, { reference_period: true }), inn()], false],
    ['an incoming row with a KID', [out(), row('in', 1500, { kid_number: '000004242018' })], false],
    ['a KID on the outgoing row only', [row('out', -1500, { kid_number: '000004242018' }), inn()], true],
    ['different years', [row('out', -1500, { row_date: '2026-12-31' }), row('in', 1500, { row_date: '2027-01-02' })], false],
    ['one row', [out()], false],
    ['three rows', [out(), inn(), row('x', 1500)], false],
  ]
  for (const [name, rows, want] of cases) {
    it(`${want ? 'offers' : 'does not offer'} the pair for ${name}`, () => {
      expect(transferPairCandidate(rows)).toBe(want)
    })
  }
})

describe('BankReconcileTab Par som overføring', () => {
  beforeEach(() => {
    pairTransfer.mutateAsync.mockReset()
    ensureFreshTotp.mockReset()
    askConfirm.mockReset()
    toastError.mockReset()
    toastSuccess.mockReset()
    invalidate.mockReset()
  })

  it('shows the button for a plausible pair and hides it otherwise', async () => {
    expect((await mountSelected([out(), inn()])).find('[data-testid="pair-transfer"]').exists()).toBe(true)
    expect((await mountSelected([out(), row('in', 1500, { bank_account_code: '1920' })])).find('[data-testid="pair-transfer"]').exists()).toBe(false)
    expect((await mountSelected([out(), row('in', 1500, { provisional: true })])).find('[data-testid="pair-transfer"]').exists()).toBe(false)
    expect((await mountSelected([out()])).find('[data-testid="pair-transfer"]').exists()).toBe(false)
  })

  it('asks for the step-up before the confirm, and sends nothing when it is refused', async () => {
    const wrapper = await mountSelected([out(), inn()])
    ensureFreshTotp.mockResolvedValueOnce(false)
    await wrapper.get('[data-testid="pair-transfer"]').trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(askConfirm).not.toHaveBeenCalled()
    expect(pairTransfer.mutateAsync).not.toHaveBeenCalled()
  })

  it('confirms with both accounts, posts row_ids and clears the selection', async () => {
    const wrapper = await mountSelected([inn(), out()])
    const order: string[] = []
    ensureFreshTotp.mockImplementationOnce(async () => {
      order.push('totp')
      return true
    })
    askConfirm.mockImplementationOnce(async () => {
      order.push('confirm')
      return true
    })
    pairTransfer.mutateAsync.mockResolvedValueOnce({})
    await wrapper.get('[data-testid="pair-transfer"]').trigger('click')
    await flushPromises()

    expect(order).toEqual(['totp', 'confirm'])
    const opts = askConfirm.mock.calls[0][0]
    expect(opts.title).toBe('admin.bankReconcile.transfer.confirmTitle')
    expect(pairTransfer.mutateAsync).toHaveBeenCalledWith({ rowIds: ['in', 'out'] })
    expect(toastSuccess).toHaveBeenCalledWith('admin.bankReconcile.transfer.success')
    expect(wrapper.find('[data-testid="multi-select-bar"]').exists()).toBe(false)
  })

  it('does nothing when the confirm is cancelled', async () => {
    const wrapper = await mountSelected([out(), inn()])
    ensureFreshTotp.mockResolvedValueOnce(true)
    askConfirm.mockResolvedValueOnce(false)
    await wrapper.get('[data-testid="pair-transfer"]').trigger('click')
    await flushPromises()
    expect(pairTransfer.mutateAsync).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="multi-select-bar"]').exists()).toBe(true)
  })

  it('maps a 409 refusal code to its toast and refreshes the list', async () => {
    const wrapper = await mountSelected([out(), inn()])
    ensureFreshTotp.mockResolvedValueOnce(true)
    askConfirm.mockResolvedValueOnce(true)
    pairTransfer.mutateAsync.mockRejectedValueOnce(new ApiError(409, 'radene ligg i ulike rekneskapsår', 'transfer_crosses_period'))
    await wrapper.get('[data-testid="pair-transfer"]').trigger('click')
    await flushPromises()
    expect(toastError).toHaveBeenCalledWith('admin.bankReconcile.transfer.refused.transfer_crosses_period', 10000)
    expect(invalidate).toHaveBeenCalled()
    expect(wrapper.find('[data-testid="multi-select-bar"]').exists()).toBe(true)
  })

  it('toasts any other error instead of throwing it', async () => {
    const wrapper = await mountSelected([out(), inn()])
    ensureFreshTotp.mockResolvedValueOnce(true)
    askConfirm.mockResolvedValueOnce(true)
    pairTransfer.mutateAsync.mockRejectedValueOnce(new ApiError(500, 'internal error'))
    await wrapper.get('[data-testid="pair-transfer"]').trigger('click')
    await flushPromises()
    expect(toastError).toHaveBeenCalledWith('common.error: internal error', 10000)
    expect(wrapper.find('[data-testid="multi-select-bar"]').exists()).toBe(true)
  })

  it('maps reference_period to the reference-year toast', async () => {
    const wrapper = await mountSelected([out(), inn()])
    ensureFreshTotp.mockResolvedValueOnce(true)
    askConfirm.mockResolvedValueOnce(true)
    pairTransfer.mutateAsync.mockRejectedValueOnce(new ApiError(409, 'referanseår', 'reference_period'))
    await wrapper.get('[data-testid="pair-transfer"]').trigger('click')
    await flushPromises()
    expect(toastError).toHaveBeenCalledWith('admin.accounting.referencePeriod', 10000)
  })
})
