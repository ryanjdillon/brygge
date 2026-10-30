import { describe, it, expect, vi } from 'vitest'
import { ref, nextTick } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import BankReconcileTab from '@/components/admin/BankReconcileTab.vue'
import type { BankRowSummary } from '@/composables/useBankReconcile'

const { ensureFreshTotp } = vi.hoisted(() => ({ ensureFreshTotp: vi.fn() }))
vi.mock('@/composables/useFreshTotp', () => ({ useFreshTotp: () => ({ ensureFreshTotp }) }))
vi.mock('@/composables/useStripePayouts', async () => {
  const actual = await vi.importActual<typeof import('@/composables/useStripePayouts')>('@/composables/useStripePayouts')
  return {
    ...actual,
    useUnclearedStripePayouts: () => ({ data: ref([]), isLoading: ref(false) }),
    useClearStripePayout: () => ({ mutateAsync: vi.fn(), isPending: ref(false) }),
  }
})

const { icon, rows } = vi.hoisted(() => {
  const row = (id: string, provider: 'stripe' | 'vipps' | null, reason?: string, byHand = false) => ({
    id,
    row_date: '2026-09-01',
    amount: 412.65,
    kid_number: '',
    reference: 'SYN' + id,
    counterpart: '',
    description: provider === 'vipps' ? 'Utb. 3000100 Vippsnr 548005' : 'STRIPE PAYOUT',
    bank_account_code: '1920',
    likely_duplicate_of_matched: false,
    possible_double_payment: false,
    suggestion_count: 0,
    vipps_payout: provider === 'vipps',
    provider_payout: provider,
    provider_payout_reason: reason,
    provider_payout_by_hand: byHand,
  })
  return {
    icon: { template: '<span />' },
    rows: [
      row('stripe', 'stripe', '1 kortbetaling venter på bokføring'),
      row('manual', 'stripe', 'manuell utbetaling – avstem manuelt', true),
      row('vipps', 'vipps'),
      row('plain', null),
    ] as BankRowSummary[],
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

describe('BankReconcileTab Stripe payout rows', () => {
  const rowItem = (wrapper: ReturnType<typeof mountWithPlugins>, id: string) =>
    wrapper.findAll('li').find((li) => li.text().includes('Ref SYN' + id))!

  it('shows the Stripe payout item with its reason and no suggestions or selection', () => {
    const wrapper = mountWithPlugins(BankReconcileTab)
    const li = rowItem(wrapper, 'stripe')
    const item = li.find('[data-testid="bank-row-stripe-payout"]')
    expect(item.text()).toContain('admin.bankReconcile.stripeAutoClearing')
    expect(item.find('[data-testid="bank-row-stripe-payout-reason"]').text()).toBe('1 kortbetaling venter på bokføring')
    expect(li.text()).not.toContain('admin.bankReconcile.showSuggestions')
    expect(li.text()).not.toContain('admin.bankReconcile.noInvoiceSuggestions')
    expect(li.find('input[type="checkbox"]').exists()).toBe(false)
    expect(item.find('[data-testid="bank-row-stripe-payout-by-hand"]').exists()).toBe(false)
    expect(item.text()).not.toContain('admin.bankReconcile.dismiss')
  })

  it('offers a by-hand row the payout picker, and still lets it be dismissed', () => {
    const wrapper = mountWithPlugins(BankReconcileTab)
    const li = rowItem(wrapper, 'manual')
    const byHand = li.find('[data-testid="bank-row-stripe-payout-by-hand"]')
    expect(byHand.text()).toContain('admin.bankReconcile.stripeByHandHint')
    expect(byHand.find('[data-testid="bank-row-stripe-clear"]').text()).toContain('admin.bankReconcile.stripeClearAction')
    expect(li.text()).toContain('admin.bankReconcile.dismiss')
    expect(li.text()).not.toContain('admin.bankReconcile.showSuggestions')
    expect(li.text()).not.toContain('admin.bankReconcile.noInvoiceSuggestions')
    expect(rowItem(wrapper, 'stripe').find('[data-testid="bank-row-stripe-clear"]').exists()).toBe(false)
  })

  it('asks for a fresh TOTP before opening the picker, and not at all when it is refused', async () => {
    const wrapper = mountWithPlugins(BankReconcileTab, { global: { stubs: { teleport: true } } })
    const button = rowItem(wrapper, 'manual').find('[data-testid="bank-row-stripe-clear"]')

    ensureFreshTotp.mockResolvedValueOnce(false)
    await button.trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="stripe-payout-picker"]').exists()).toBe(false)

    ensureFreshTotp.mockResolvedValueOnce(true)
    await button.trigger('click')
    await flushPromises()
    await nextTick()
    expect(ensureFreshTotp).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-testid="stripe-payout-picker"]').exists()).toBe(true)
  })

  it('keeps suggestions on an ordinary row', () => {
    const wrapper = mountWithPlugins(BankReconcileTab)
    const li = rowItem(wrapper, 'plain')
    expect(li.find('[data-testid="bank-row-stripe-payout"]').exists()).toBe(false)
    expect(li.text()).toContain('admin.bankReconcile.noInvoiceSuggestions')
  })
})
