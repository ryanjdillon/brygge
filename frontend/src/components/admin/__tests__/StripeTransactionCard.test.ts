import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import StripeTransactionCard from '@/components/admin/StripeTransactionCard.vue'
import { ApiError } from '@/lib/errors'
import type { StripeTxnItem } from '@/composables/useStripeTransactions'

const { ensureFreshTotp, undismiss, rebook, toast, dialog } = vi.hoisted(() => ({
  ensureFreshTotp: vi.fn(),
  undismiss: vi.fn(),
  rebook: vi.fn(),
  toast: { success: vi.fn(), info: vi.fn(), error: vi.fn() },
  dialog: (name: string) => ({
    default: { props: ['open', 'item'], template: `<div v-if="open" data-testid="${name}" />` },
  }),
}))

vi.mock('@/composables/useFreshTotp', () => ({ useFreshTotp: () => ({ ensureFreshTotp }) }))
vi.mock('@/composables/useToast', () => ({ useToast: () => toast }))
vi.mock('@/composables/useStripeTransactions', async () => {
  const actual = await vi.importActual<typeof import('@/composables/useStripeTransactions')>(
    '@/composables/useStripeTransactions',
  )
  const m = () => ({ mutateAsync: vi.fn(), isPending: ref(false) })
  return {
    ...actual,
    useStripeTxnMutations: () => ({
      assignInvoice: m(),
      assignAccount: m(),
      dismiss: m(),
      undismiss: { mutateAsync: undismiss, isPending: ref(false) },
      unassign: m(),
      rebook: { mutateAsync: rebook, isPending: ref(false) },
    }),
  }
})
vi.mock('@/components/admin/StripeTxnInvoiceDialog.vue', () => dialog('invoice-dialog'))
vi.mock('@/components/admin/StripeTxnAccountDialog.vue', () => dialog('account-dialog'))
vi.mock('@/components/admin/StripeTxnDismissDialog.vue', () => dialog('dismiss-dialog'))
vi.mock('@/components/admin/StripeTxnUnassignDialog.vue', () => dialog('unassign-dialog'))

const item = (extra: Partial<StripeTxnItem> = {}): StripeTxnItem => ({
  id: 'row-1',
  balance_transaction_id: 'txn_test_0123456789abcdefghij',
  reporting_category: 'charge',
  stripe_type: 'charge',
  amount: 450,
  fee: 11.25,
  net: 438.75,
  currency: 'nok',
  date: '2026-06-15',
  created_at: '2026-06-15T10:00:00Z',
  status: 'needs_review',
  review_reason: 'no_attempt',
  order_id: null,
  attempt_id: null,
  payout_id: null,
  payout_cleared: false,
  allowed_actions: ['assign_invoice', 'assign_account', 'dismiss'],
  ...extra,
})

const actionIds = (w: ReturnType<typeof mountWithPlugins>) =>
  w.findAll('[data-testid^="stripe-txn-action-"]').map((b) => b.attributes('data-testid')!.replace('stripe-txn-action-', ''))

describe('StripeTransactionCard', () => {
  beforeEach(() => {
    ensureFreshTotp.mockReset().mockResolvedValue(true)
    undismiss.mockReset().mockResolvedValue({})
    rebook.mockReset().mockResolvedValue({ outcome: 'booked', status: 'booked' })
    Object.values(toast).forEach((f) => f.mockReset())
  })

  it('offers exactly the actions the server allows', () => {
    expect(actionIds(mountWithPlugins(StripeTransactionCard, { props: { item: item() } }))).toEqual([
      'assign_invoice',
      'assign_account',
      'dismiss',
    ])
    const noAccount = mountWithPlugins(StripeTransactionCard, {
      props: { item: item({ reporting_category: 'dispute', review_reason: 'dispute_account_missing', allowed_actions: ['dismiss'] }) },
    })
    expect(actionIds(noAccount)).toEqual(['dismiss'])
    const legacyDispute = mountWithPlugins(StripeTransactionCard, {
      props: { item: item({ reporting_category: 'dispute', review_reason: 'refund_legacy', allowed_actions: ['rebook', 'dismiss'] }) },
    })
    expect(actionIds(legacyDispute)).toEqual(['rebook', 'dismiss'])
    const none = mountWithPlugins(StripeTransactionCard, { props: { item: item({ allowed_actions: [] }) } })
    expect(actionIds(none)).toEqual([])
    expect(none.find('[data-testid="stripe-txn-no-actions"]').exists()).toBe(true)
  })

  it('shows the amount, fee, net, reason and a shortened Stripe id', () => {
    const w = mountWithPlugins(StripeTransactionCard, { props: { item: item() } })
    expect(w.text()).toContain('450')
    expect(w.text()).toContain('admin.bankImports.stripeTxn.fee')
    expect(w.find('[data-testid="stripe-txn-reason"]').text()).toBe('no_attempt')
    const code = w.find('code')
    expect(code.text()).toContain('…')
    expect(code.attributes('title')).toBe('txn_test_0123456789abcdefghij')
  })

  it('steps up at click time, then opens the dialog', async () => {
    const w = mountWithPlugins(StripeTransactionCard, { props: { item: item() } })
    for (const [action, testid] of [
      ['assign_invoice', 'invoice-dialog'],
      ['assign_account', 'account-dialog'],
      ['dismiss', 'dismiss-dialog'],
    ]) {
      ensureFreshTotp.mockClear()
      await w.find(`[data-testid="stripe-txn-action-${action}"]`).trigger('click')
      await flushPromises()
      expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
      expect(w.find(`[data-testid="${testid}"]`).exists()).toBe(true)
    }
  })

  it('opens nothing when the step-up is cancelled', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const w = mountWithPlugins(StripeTransactionCard, { props: { item: item() } })
    await w.find('[data-testid="stripe-txn-action-assign_invoice"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="invoice-dialog"]').exists()).toBe(false)
  })

  it('undoes a dismissal after the step-up and shows a refusal', async () => {
    const dismissed = item({
      status: 'dismissed',
      dismissed_at: '2026-06-20T08:00:00Z',
      dismissed_reason: 'duplicate',
      dismissed_note: 'Synthetic note',
      allowed_actions: ['undismiss'],
    })
    const w = mountWithPlugins(StripeTransactionCard, { props: { item: dismissed } })
    expect(w.find('[data-testid="stripe-txn-dismissed"]').text()).toContain('duplicate')
    expect(w.find('[data-testid="stripe-txn-dismissed"]').text()).toContain('Synthetic note')
    await w.find('[data-testid="stripe-txn-action-undismiss"]').trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(undismiss).toHaveBeenCalledWith({ id: 'row-1' })
    expect(toast.success).toHaveBeenCalled()

    undismiss.mockRejectedValue(new ApiError(409, 'refused', 'payout_cleared', { reason: 'x' }))
    await w.find('[data-testid="stripe-txn-action-undismiss"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="stripe-txn-error"]').text()).toBe('admin.bankImports.stripeTxn.errors.payout_cleared')
  })

  it('never undoes a dismissal without the step-up', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const w = mountWithPlugins(StripeTransactionCard, {
      props: { item: item({ status: 'dismissed', dismissed_at: '2026-06-20T08:00:00Z', allowed_actions: ['undismiss'] }) },
    })
    await w.find('[data-testid="stripe-txn-action-undismiss"]').trigger('click')
    await flushPromises()
    expect(undismiss).not.toHaveBeenCalled()
  })

  it('says why a row in review of a cleared payout only dismisses', () => {
    const w = mountWithPlugins(StripeTransactionCard, {
      props: { item: item({ payout_cleared: true, allowed_actions: ['dismiss'] }) },
    })
    expect(w.find('[data-testid="stripe-txn-payout-cleared"]').exists()).toBe(true)
    expect(actionIds(w)).toEqual(['dismiss'])
  })

  it('says why a dismissal of a cleared payout stays', () => {
    const w = mountWithPlugins(StripeTransactionCard, {
      props: { item: item({ status: 'dismissed', dismissed_at: '2026-06-20T08:00:00Z', payout_cleared: true, allowed_actions: [] }) },
    })
    expect(w.find('[data-testid="stripe-txn-payout-cleared"]').exists()).toBe(true)
    expect(actionIds(w)).toEqual([])
  })

  it('shows a booked row with its entry and fakturas, and steps up before the undo dialog', async () => {
    const booked = item({
      status: 'booked',
      review_reason: undefined,
      journal_entry_id: 'entry-1',
      journal_entry_number: 17,
      journal_entry_description: 'Kortbetaling ordre 0a1b2c3d',
      journal_entry_status: 'posted',
      invoice_numbers: [1042],
      allowed_actions: ['unassign'],
    })
    const w = mountWithPlugins(StripeTransactionCard, { props: { item: booked } })
    expect(w.find('[data-testid="stripe-txn-entry"]').exists()).toBe(true)
    expect(w.find('[data-testid="stripe-txn-invoices"]').exists()).toBe(true)
    expect(actionIds(w)).toEqual(['unassign'])

    ensureFreshTotp.mockResolvedValueOnce(false)
    await w.find('[data-testid="stripe-txn-action-unassign"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="unassign-dialog"]').exists()).toBe(false)

    await w.find('[data-testid="stripe-txn-action-unassign"]').trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).toHaveBeenCalledTimes(2)
    expect(w.find('[data-testid="unassign-dialog"]').exists()).toBe(true)
  })

  it('rebooks a held row from its order after the step-up', async () => {
    const held = item({
      review_reason: 'unassigned',
      order_id: 'order-1',
      allowed_actions: ['assign_invoice', 'assign_account', 'dismiss', 'rebook'],
    })
    const w = mountWithPlugins(StripeTransactionCard, { props: { item: held } })
    expect(w.find('[data-testid="stripe-txn-reason"]').text()).toBe('unassigned')
    expect(actionIds(w)).toEqual(['rebook', 'assign_invoice', 'assign_account', 'dismiss'])

    ensureFreshTotp.mockResolvedValueOnce(false)
    await w.find('[data-testid="stripe-txn-action-rebook"]').trigger('click')
    await flushPromises()
    expect(rebook).not.toHaveBeenCalled()

    await w.find('[data-testid="stripe-txn-action-rebook"]').trigger('click')
    await flushPromises()
    expect(rebook).toHaveBeenCalledWith({ id: 'row-1' })
    expect(toast.success).toHaveBeenCalledWith('admin.bankImports.stripeTxn.rebook.booked')

    rebook.mockResolvedValue({ outcome: 'needs_review', reason: 'split_mismatch', status: 'needs_review' })
    await w.find('[data-testid="stripe-txn-action-rebook"]').trigger('click')
    await flushPromises()
    expect(toast.info).toHaveBeenCalledWith('admin.bankImports.stripeTxn.rebook.needsReview')
  })
})
