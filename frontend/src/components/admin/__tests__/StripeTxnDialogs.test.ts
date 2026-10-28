import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import StripeTxnInvoiceDialog from '@/components/admin/StripeTxnInvoiceDialog.vue'
import StripeTxnAccountDialog from '@/components/admin/StripeTxnAccountDialog.vue'
import StripeTxnDismissDialog from '@/components/admin/StripeTxnDismissDialog.vue'
import { ApiError } from '@/lib/errors'
import type { StripeTxnItem } from '@/composables/useStripeTransactions'

const { assignInvoice, assignAccount, dismiss, toast, invoices, accounts } = vi.hoisted(() => ({
  assignInvoice: vi.fn(),
  assignAccount: vi.fn(),
  dismiss: vi.fn(),
  toast: { success: vi.fn(), info: vi.fn(), error: vi.fn() },
  invoices: { value: [] as unknown[] },
  accounts: { value: [] as unknown[] },
}))

vi.mock('lucide-vue-next', () => ({ X: { template: '<span />' } }))
vi.mock('@/composables/useToast', () => ({ useToast: () => toast }))
vi.mock('@/composables/useStripeTransactions', async () => {
  const actual = await vi.importActual<typeof import('@/composables/useStripeTransactions')>(
    '@/composables/useStripeTransactions',
  )
  return {
    ...actual,
    useStripeTxnPotentialInvoices: () => ({ data: ref(invoices.value), isLoading: ref(false) }),
    useStripeAssignableAccounts: () => ({ data: ref(accounts.value), isLoading: ref(false) }),
    useStripeTxnMutations: () => ({
      assignInvoice: { mutateAsync: assignInvoice, isPending: ref(false) },
      assignAccount: { mutateAsync: assignAccount, isPending: ref(false) },
      dismiss: { mutateAsync: dismiss, isPending: ref(false) },
      undismiss: { mutateAsync: vi.fn(), isPending: ref(false) },
    }),
  }
})

const item = (extra: Partial<StripeTxnItem> = {}): StripeTxnItem => ({
  id: 'row-1',
  balance_transaction_id: 'txn_test_dialog',
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

const mountOpts = (props: { open: boolean; item: StripeTxnItem }) => ({ props, global: { stubs: { teleport: true } } })

describe('Stripe transaction dialogs', () => {
  beforeEach(() => {
    for (const f of [assignInvoice, assignAccount, dismiss]) f.mockReset().mockResolvedValue({})
    Object.values(toast).forEach((f) => f.mockReset())
    invoices.value = [
      {
        invoice_id: 'inv-1',
        invoice_number: 1042,
        member_name: 'Synthetic Member',
        price_item_name: '',
        total_amount: 450,
        due_date: '2026-06-30',
      },
    ]
    accounts.value = [
      { code: '3900', name: 'Andre inntekter', account_type: 'revenue' },
      { code: '7790', name: 'Andre driftskostnader', account_type: 'expense' },
    ]
  })

  it('books a charge to the faktura picked', async () => {
    const w = mountWithPlugins(StripeTxnInvoiceDialog, mountOpts({ open: true, item: item() }))
    expect(w.find('[data-testid="stripe-txn-invoice-confirm"]').attributes('disabled')).toBeDefined()
    await w.find('[data-testid="stripe-txn-invoice-1042"]').trigger('click')
    await w.find('[data-testid="stripe-txn-invoice-confirm"]').trigger('click')
    await flushPromises()
    expect(assignInvoice).toHaveBeenCalledWith({ id: 'row-1', invoiceId: 'inv-1' })
    expect(toast.success).toHaveBeenCalled()
    const emitted = w.emitted('update:open') ?? []
    expect(emitted[emitted.length - 1]).toEqual([false])
  })

  it('shows a refused faktura assign and stays open', async () => {
    assignInvoice.mockRejectedValue(new ApiError(409, 'refused', 'amount_mismatch', { reason: 'x' }))
    const w = mountWithPlugins(StripeTxnInvoiceDialog, mountOpts({ open: true, item: item() }))
    await w.find('[data-testid="stripe-txn-invoice-1042"]').trigger('click')
    await w.find('[data-testid="stripe-txn-invoice-confirm"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="stripe-txn-dialog-error"]').text()).toBe('admin.bankImports.stripeTxn.errors.amount_mismatch')
    expect(w.emitted('update:open')).toBeUndefined()
  })

  it('says when no faktura matches', () => {
    invoices.value = []
    const w = mountWithPlugins(StripeTxnInvoiceDialog, mountOpts({ open: true, item: item() }))
    expect(w.find('[data-testid="stripe-txn-invoice-empty"]').exists()).toBe(true)
  })

  it('books to an account the server offers, with the note', async () => {
    const w = mountWithPlugins(StripeTxnAccountDialog, mountOpts({ open: true, item: item() }))
    const options = w.findAll('[data-testid="stripe-txn-account-select"] option').map((o) => o.attributes('value'))
    expect(options).toEqual(['', '3900', '7790'])
    expect(w.find('[data-testid="stripe-txn-account-confirm"]').attributes('disabled')).toBeDefined()
    await w.find('[data-testid="stripe-txn-account-select"]').setValue('3900')
    await w.find('[data-testid="stripe-txn-account-note"]').setValue('  Synthetic walk-up ')
    await w.find('[data-testid="stripe-txn-account-dialog"]').trigger('submit')
    await flushPromises()
    expect(assignAccount).toHaveBeenCalledWith({ id: 'row-1', accountCode: '3900', note: 'Synthetic walk-up' })
  })

  it('explains money going out for a negative row', () => {
    const w = mountWithPlugins(StripeTxnAccountDialog, mountOpts({ open: true, item: item({ amount: -50 }) }))
    expect(w.text()).toContain('admin.bankImports.stripeTxn.accountDialog.postingOut')
  })

  it('warns what a dismissal leaves on the clearing account', async () => {
    const w = mountWithPlugins(StripeTxnDismissDialog, mountOpts({ open: true, item: item() }))
    expect(w.find('[data-testid="stripe-txn-dismiss-warning"]').exists()).toBe(true)
    expect(w.find('[data-testid="stripe-txn-dismiss-refund-warning"]').exists()).toBe(false)
    expect(w.find('[data-testid="stripe-txn-dismiss-confirm"]').attributes('disabled')).toBeDefined()
    await w.find('[data-testid="stripe-txn-dismiss-reason"]').setValue('booked_manually')
    await w.find('[data-testid="stripe-txn-dismiss-note"]').setValue('Synthetic bilag')
    await w.find('[data-testid="stripe-txn-dismiss-dialog"]').trigger('submit')
    await flushPromises()
    expect(dismiss).toHaveBeenCalledWith({ id: 'row-1', reason: 'booked_manually', note: 'Synthetic bilag' })
  })

  it('warns that a refund dismissal bypasses its later booking', () => {
    const w = mountWithPlugins(
      StripeTxnDismissDialog,
      mountOpts({ open: true, item: item({ reporting_category: 'refund', review_reason: 'refund_pending_bry446', amount: -50 }) }),
    )
    expect(w.find('[data-testid="stripe-txn-dismiss-refund-warning"]').exists()).toBe(true)
    const reasons = w.findAll('[data-testid="stripe-txn-dismiss-reason"] option').map((o) => o.attributes('value'))
    expect(reasons).not.toContain('internal_transfer')
  })
})
