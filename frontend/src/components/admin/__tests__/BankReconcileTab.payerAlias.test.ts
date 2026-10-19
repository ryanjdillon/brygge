import { describe, it, expect, vi, beforeEach } from 'vitest'
import { computed, ref, type Ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import BankReconcileTab from '@/components/admin/BankReconcileTab.vue'
import { useConfirmStore } from '@/stores/confirm'
import { ApiError } from '@/lib/errors'
import type { BankRowSuggestions, BankRowSummary, PayerAliasState } from '@/composables/useBankReconcile'

// Synthetic deposits from a company paying a member's faktura.
const { icon, rows, state, invoice, assignInvoice, assignMulti, rowStates } = vi.hoisted(() => {
  const row = (id: string, amount: number, counterpart: string) => ({
    id,
    row_date: '2026-09-01',
    amount,
    kid_number: '',
    reference: 'SYN' + id,
    counterpart,
    description: 'Synthetic deposit ' + id,
    bank_account_code: '1920',
    likely_duplicate_of_matched: false,
    possible_double_payment: false,
    suggestion_count: 1,
    vipps_payout: false,
    provisional: false,
    partner_hint: null,
  })
  const invoice = (id: string, userId: string, total: number) => ({
    invoice_id: id,
    invoice_number: 1001,
    user_id: userId,
    member_name: 'Synthetic Fortoy',
    member_email: 'fortoy@example.invalid',
    price_item_name: 'Synthetic membership',
    issue_date: '2026-08-01',
    due_date: '2026-08-31',
    total_amount: total,
    kid_number: '',
    score: 20,
    confidence_label: 'svak' as const,
    why_tooltip: 'synthetic',
  })
  const mutation = () => ({ mutateAsync: vi.fn(), isPending: { value: false } })
  return {
    icon: { template: '<span />' },
    rows: [row('a', 300, 'SYN FORTOY HOLDING'), row('b', 200, 'SYN FORTOY HOLDING'), row('c', 200, 'SYN OTHER PAYER')] as BankRowSummary[],
    state: { current: null as PayerAliasState | null },
    invoice,
    assignInvoice: mutation(),
    assignMulti: mutation(),
    rowStates: {} as Record<string, PayerAliasState>,
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
    fetchPotentialInvoices: vi.fn(async () => [invoice('inv-multi', 'member-1', 500)]),
    fetchBankRowSuggestions: vi.fn(async (id: string) => ({ invoices: [], accounts: [], payer_alias: rowStates[id] })),
    useBankUnmatchedRows: () => ({ data: ref(rows), isLoading: ref(false) }),
    useBankRowSuggestions: (id: Ref<string | null>) => ({
      data: computed<BankRowSuggestions | undefined>(() =>
        id.value
          ? { invoices: [invoice('inv-1', 'member-1', 300)], accounts: [], payer_alias: state.current ?? undefined }
          : undefined,
      ),
    }),
    useBankUnmatchedCountsByYear: () => ({ data: ref({}) }),
    useMissingVippsSettlements: () => ({ data: ref([]) }),
    useReconcileMutations: () => ({
      assignInvoice,
      assignAccount: mutation(),
      assignAccountGroup: mutation(),
      dismiss: mutation(),
      unassign: mutation(),
      invalidate: vi.fn(),
    }),
    useAssignMultiInvoiceMutation: () => assignMulti,
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
  useAccountsList: () => ({ data: ref([]) }),
}))

const payer = (o: Partial<PayerAliasState> = {}): PayerAliasState => ({
  payer: 'SYN FORTOY HOLDING',
  pass: 'none',
  conflict: false,
  own_club: false,
  ...o,
})

function mountTab() {
  return mountWithPlugins(BankReconcileTab, { global: { stubs: { teleport: true } } })
}

async function expandFirstRow() {
  const wrapper = mountTab()
  const toggle = wrapper.findAll('button').find((b) => b.text().includes('admin.bankReconcile.showSuggestions'))!
  await toggle.trigger('click')
  await flushPromises()
  return wrapper
}

async function clickAssign(wrapper: ReturnType<typeof mountTab>) {
  const btn = wrapper.findAll('button').find((b) => b.text() === 'admin.bankReconcile.assign')!
  await btn.trigger('click')
  await flushPromises()
}

const lastAlias = () => {
  const calls = assignInvoice.mutateAsync.mock.calls
  return calls[calls.length - 1][0].alias
}

describe('BankReconcileTab Hugs betalaren', () => {
  beforeEach(() => {
    assignInvoice.mutateAsync.mockReset()
    assignMulti.mutateAsync.mockReset()
    state.current = null
    for (const k of Object.keys(rowStates)) delete rowStates[k]
  })

  it('is ticked by default and sends remember_payer', async () => {
    state.current = payer()
    const wrapper = await expandFirstRow()
    const box = wrapper.get('[data-testid="payer-alias-checkbox"]')
    expect((box.element as HTMLInputElement).checked).toBe(true)
    expect(wrapper.text()).toContain('SYN FORTOY HOLDING')
    expect(wrapper.find('[data-testid="payer-alias-conflict"]').exists()).toBe(false)

    await clickAssign(wrapper)
    expect(assignInvoice.mutateAsync).toHaveBeenCalledWith({
      rowId: 'a',
      invoiceId: 'inv-1',
      alias: { remember_payer: true, confirm_alias_override: false },
    })
  })

  it('sends remember_payer false once unticked', async () => {
    state.current = payer()
    const wrapper = await expandFirstRow()
    await wrapper.get('[data-testid="payer-alias-checkbox"]').setValue(false)
    await clickAssign(wrapper)
    expect(lastAlias()).toEqual({ remember_payer: false, confirm_alias_override: false })
  })

  it.each([
    ['the payer already is the faktura member', payer({ member_id: 'member-1', pass: 'name', conflict: true })],
    ['the payer is the club', payer({ own_club: true })],
    ['the row has no payer name', payer({ payer: '' })],
  ])('is hidden when %s', async (_why, st) => {
    state.current = st
    const wrapper = await expandFirstRow()
    expect(wrapper.find('[data-testid="payer-alias"]').exists()).toBe(false)
    await clickAssign(wrapper)
    expect(lastAlias()).toEqual({ remember_payer: false, confirm_alias_override: false })
  })

  it('starts unticked with a warning when the name is another member’s', async () => {
    state.current = payer({ member_id: 'member-2', pass: 'alias', conflict: true })
    const wrapper = await expandFirstRow()
    const box = wrapper.get('[data-testid="payer-alias-checkbox"]')
    expect((box.element as HTMLInputElement).checked).toBe(false)
    expect(wrapper.get('[data-testid="payer-alias-conflict"]').text()).toBe('admin.bankReconcile.payerAlias.conflictWarning')
    expect(wrapper.find('[data-testid="payer-alias"]').text()).not.toContain('member-2')

    await clickAssign(wrapper)
    expect(lastAlias()).toEqual({ remember_payer: false, confirm_alias_override: false })

    // Ticked despite the warning: the request confirms the override.
    await box.setValue(true)
    await clickAssign(wrapper)
    expect(lastAlias()).toEqual({ remember_payer: true, confirm_alias_override: true })
  })

  it.each([
    ['override', { remember_payer: true, confirm_alias_override: true }],
    ['skip', { remember_payer: false, confirm_alias_override: false }],
  ])('asks again on a 409 alias_conflict and resends with %s', async (choice, retried) => {
    state.current = payer()
    assignInvoice.mutateAsync.mockRejectedValueOnce(new ApiError(409, 'alias_conflict'))
    const wrapper = await expandFirstRow()
    const confirm = useConfirmStore()
    vi.mocked(confirm.ask).mockImplementation(async () => {
      confirm.choiceValue = choice
      return true
    })

    await clickAssign(wrapper)
    expect(confirm.ask).toHaveBeenCalledOnce()
    expect(assignInvoice.mutateAsync).toHaveBeenCalledTimes(2)
    expect(assignInvoice.mutateAsync.mock.calls[0][0].alias).toEqual({ remember_payer: true, confirm_alias_override: false })
    expect(lastAlias()).toEqual(retried)
  })

  it('books nothing more when the 409 prompt is cancelled', async () => {
    state.current = payer()
    assignInvoice.mutateAsync.mockRejectedValueOnce(new ApiError(409, 'alias_conflict'))
    const wrapper = await expandFirstRow()
    vi.mocked(useConfirmStore().ask).mockResolvedValue(false)

    await clickAssign(wrapper)
    expect(assignInvoice.mutateAsync).toHaveBeenCalledTimes(1)
  })

  async function openMulti(ids: string[]) {
    const wrapper = mountTab()
    const boxes = wrapper.findAll('input[type="checkbox"]')
    for (const id of ids) await boxes[rows.findIndex((r) => r.id === id)].setValue(true)
    const btn = wrapper.findAll('button').find((b) => b.text().includes('admin.bankReconcile.assignInvoice'))!
    await btn.trigger('click')
    await flushPromises()
    return wrapper
  }

  it('remembers the shared payer of several rows', async () => {
    rowStates.a = payer()
    rowStates.b = payer({ payer: 'Syn Fortoy Holding' })
    const wrapper = await openMulti(['a', 'b'])
    const box = wrapper.get('[data-testid="payer-alias-checkbox"]')
    expect((box.element as HTMLInputElement).checked).toBe(true)

    await clickAssign(wrapper)
    expect(assignMulti.mutateAsync).toHaveBeenCalledWith({
      rowIds: ['a', 'b'],
      invoiceId: 'inv-multi',
      alias: { remember_payer: true, confirm_alias_override: false },
    })
  })

  it('hides the box for rows from different payers', async () => {
    rowStates.a = payer()
    rowStates.c = payer({ payer: 'SYN OTHER PAYER' })
    const wrapper = await openMulti(['a', 'c'])
    expect(wrapper.find('[data-testid="payer-alias"]').exists()).toBe(false)
    await clickAssign(wrapper)
    expect(assignMulti.mutateAsync.mock.calls[0][0].alias).toEqual({ remember_payer: false, confirm_alias_override: false })
  })
})
