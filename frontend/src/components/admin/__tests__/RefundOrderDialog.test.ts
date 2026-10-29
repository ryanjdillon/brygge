import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref, nextTick } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import RefundOrderDialog from '@/components/admin/RefundOrderDialog.vue'
import { ApiError } from '@/lib/errors'
import type { OrderRefundAttempt, OrderRefunds, PaymentRefund } from '@/composables/useOrderRefund'

const { mutate, toast, view } = vi.hoisted(() => ({
  mutate: vi.fn(),
  toast: { success: vi.fn(), info: vi.fn(), error: vi.fn() },
  view: { value: null as unknown },
}))

vi.mock('lucide-vue-next', () => ({ X: { template: '<span />' }, ExternalLink: { template: '<span />' } }))
vi.mock('@/composables/useToast', () => ({ useToast: () => toast }))
vi.mock('@/composables/useOrderRefund', async () => {
  const actual = await vi.importActual<typeof import('@/composables/useOrderRefund')>('@/composables/useOrderRefund')
  return {
    ...actual,
    useOrderRefunds: () => ({
      data: ref(view.value),
      isLoading: ref(false),
      isError: ref(false),
      isFetching: ref(false),
    }),
    useRefundOrder: () => ({ mutateAsync: mutate, isPending: ref(false) }),
  }
})

const attempt = (extra: Partial<OrderRefundAttempt> = {}): OrderRefundAttempt => ({
  id: 'attempt-1',
  provider: 'stripe',
  currency: 'NOK',
  amount: 450,
  refunded: 0,
  refundable: 450,
  duplicate: false,
  disputed: false,
  can_refund: true,
  recollect_allowed: true,
  payment_url: 'https://dashboard.stripe.com/test/payments/pi_synthetic',
  created_at: '2026-06-15T10:00:00Z',
  ...extra,
})

const orderView = (extra: Partial<OrderRefunds> = {}): OrderRefunds => ({
  attempts: [attempt()],
  refunds: [],
  disputes: [],
  has_fakturas: true,
  can_refund: true,
  recollect_allowed: true,
  ...extra,
})

function open(extra: Partial<OrderRefunds> = {}) {
  view.value = orderView(extra)
  return mountWithPlugins(RefundOrderDialog, {
    props: { open: true, orderId: 'order-1' },
    global: { stubs: { teleport: true } },
  })
}

const q = (w: ReturnType<typeof open>, id: string) => w.find(`[data-testid="${id}"]`)

async function fill(w: ReturnType<typeof open>, amount: string | null, reason: string) {
  if (amount !== null) await q(w, 'refund-amount').setValue(amount)
  await q(w, 'refund-reason').setValue(reason)
}

describe('RefundOrderDialog', () => {
  beforeEach(() => {
    mutate.mockReset().mockResolvedValue({ refund: {}, replayed: false, awaiting_confirmation: false })
    Object.values(toast).forEach((f) => f.mockReset())
  })

  it('prefills the refundable amount, links the payment in Stripe and explains what happens', () => {
    const w = open()
    expect((q(w, 'refund-amount').element as HTMLInputElement).value).toBe('450.00')
    expect(q(w, 'refund-dashboard-link').attributes('href')).toBe('https://dashboard.stripe.com/test/payments/pi_synthetic')
    expect(q(w, 'refund-dashboard-link').attributes('rel')).toContain('noopener')
    const text = q(w, 'refund-explain').text()
    expect(text).toContain('admin.refund.explainMoney')
    expect(text).toContain('admin.refund.explainCredit')
  })

  it('preselects a duplicate capture and labels it', async () => {
    const w = open({
      attempts: [attempt({ id: 'first' }), attempt({ id: 'dup', duplicate: true, refundable: 450, created_at: '2026-06-15T10:05:00Z' })],
    })
    await nextTick()
    const select = q(w, 'refund-attempt').element as HTMLSelectElement
    expect(select.value).toBe('dup')
    expect(q(w, 'refund-attempt').text()).toContain('admin.refund.duplicate')
    expect(q(w, 'refund-outcome').exists()).toBe(false)
    // The pass issues no document for a duplicate capture, so none is promised.
    expect(q(w, 'refund-explain').text()).not.toContain('admin.refund.explainCredit')
  })

  it('requires an amount within what is refundable and a reason, and sends nothing until both are right', async () => {
    const w = open()
    await q(w, 'refund-confirm').trigger('submit')
    await q(w, 'refund-confirm').trigger('click')
    await flushPromises()
    expect(mutate).not.toHaveBeenCalled()
    expect(q(w, 'refund-reason-error').text()).toBe('admin.refund.errors.reasonRequired')

    await fill(w, '450,01', 'Dobbeltbetaling')
    await w.find('form').trigger('submit')
    expect(q(w, 'refund-amount-error').text()).toContain('admin.refund.errors.amountTooHigh')
    await fill(w, 'abc', 'Dobbeltbetaling')
    await w.find('form').trigger('submit')
    expect(q(w, 'refund-amount-error').text()).toBe('admin.refund.errors.amountInvalid')
    expect(mutate).not.toHaveBeenCalled()
  })

  it('offers credit or collect-again only for a full refund, defaulting to credit', async () => {
    const w = open()
    expect(q(w, 'refund-outcome').exists()).toBe(true)
    expect((q(w, 'refund-outcome-credit').element as HTMLInputElement).checked).toBe(true)

    await q(w, 'refund-amount').setValue('100')
    expect(q(w, 'refund-outcome').exists()).toBe(false)
    await q(w, 'refund-amount').setValue('450')
    expect(q(w, 'refund-outcome').exists()).toBe(true)
  })

  it('hides collect-again when the server says it is not allowed, and when no faktura was paid', () => {
    expect(open({ attempts: [attempt({ recollect_allowed: false })] }).find('[data-testid="refund-outcome"]').exists()).toBe(false)
    expect(open({ has_fakturas: false }).find('[data-testid="refund-outcome"]').exists()).toBe(false)
  })

  it('sends the chosen outcome with a full refund and none with a partial one', async () => {
    const w = open()
    await fill(w, null, 'Feil beløp')
    await q(w, 'refund-outcome-recollect').setValue(true)
    expect(q(w, 'refund-explain').text()).not.toContain('admin.refund.explainCredit')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(mutate).toHaveBeenLastCalledWith(
      expect.objectContaining({ orderId: 'order-1', attemptId: 'attempt-1', amount: 450, reason: 'Feil beløp', outcome: 'recollect' }),
    )

    await fill(w, '100', 'Feil beløp')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(mutate).toHaveBeenLastCalledWith(expect.objectContaining({ amount: 100, outcome: undefined }))
  })

  it('keeps one request key across retries after a failed answer, and tells the person to retry safely', async () => {
    const w = open()
    mutate.mockRejectedValueOnce(new ApiError(500, 'boom')).mockRejectedValueOnce(new TypeError('network'))
    await fill(w, null, 'Dobbeltbetaling')

    await w.find('form').trigger('submit')
    await flushPromises()
    expect(q(w, 'refund-error').text()).toBe('admin.refund.errors.generic')
    await w.find('form').trigger('submit')
    await flushPromises()
    await w.find('form').trigger('submit')
    await flushPromises()

    const keys = mutate.mock.calls.map((c) => c[0].requestKey)
    expect(keys).toHaveLength(3)
    expect(new Set(keys).size).toBe(1)
    expect(keys[0]).toMatch(/^[0-9a-f-]{36}$/)
    expect(toast.success).toHaveBeenCalledTimes(1)
  })

  it('makes a new key for a refund Stripe refused, and for a dialog opened again', async () => {
    const w = open()
    mutate.mockRejectedValueOnce(new ApiError(502, 'refused', 'refund_failed'))
    await fill(w, null, 'Dobbeltbetaling')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(q(w, 'refund-error').text()).toBe('admin.refund.errors.refund_failed')
    await w.find('form').trigger('submit')
    await flushPromises()
    const [refused, retried] = mutate.mock.calls.map((c) => c[0].requestKey)
    expect(retried).not.toBe(refused)

    await w.setProps({ open: false })
    await w.setProps({ open: true })
    await fill(w, null, 'Dobbeltbetaling')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(mutate.mock.calls[2][0].requestKey).not.toBe(retried)
  })

  it('shows a server refusal in words and keeps the dialog open', async () => {
    const w = open()
    mutate.mockRejectedValueOnce(new ApiError(409, 'x', 'disputed'))
    await fill(w, null, 'Dobbeltbetaling')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(q(w, 'refund-error').text()).toBe('admin.refund.errors.disputed')
    expect(w.emitted('update:open')).toBeUndefined()
  })

  it('reports success, tells the list to reload and closes; an unconfirmed refund says so', async () => {
    const w = open()
    await fill(w, null, 'Dobbeltbetaling')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(toast.success).toHaveBeenCalledWith('admin.refund.toast.done')
    expect(w.emitted('refunded')).toHaveLength(1)
    const closes = w.emitted('update:open') ?? []
    expect(closes[closes.length - 1]).toEqual([false])

    mutate.mockResolvedValueOnce({ refund: {}, replayed: false, awaiting_confirmation: true })
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(toast.info).toHaveBeenCalledWith('admin.refund.toast.awaiting')
  })

  it('says why nothing can be refunded instead of showing a form', () => {
    const none = open({ attempts: [attempt({ refundable: 0, can_refund: false })], can_refund: false })
    expect(none.find('[data-testid="refund-nothing"]').text()).toBe('admin.refund.nothingToRefund')
    expect(none.find('form').exists()).toBe(false)

    const disputed = open({ attempts: [attempt({ can_refund: false, disputed: true })], can_refund: false })
    expect(disputed.find('[data-testid="refund-nothing"]').text()).toBe('admin.refund.errors.disputed')
  })

  it('lists earlier refunds, a Dashboard one by its source', () => {
    const refund = (id: string, createdBy: string | null, reason: string): PaymentRefund => ({
      id, attempt_id: 'attempt-1', provider: 'stripe', amount: 100, currency: 'NOK', status: 'succeeded', reason,
      outcome: null, failure_reason: null, provider_refund_id: null, created_by: createdBy, request_key: null,
      created_at: '2026-06-16T10:00:00Z', updated_at: '2026-06-16T10:00:00Z',
    })
    const w = open({ refunds: [refund('r1', 'user-1', 'Dobbeltbetaling'), refund('r2', null, 'Stripe Dashboard')] })
    const text = q(w, 'refund-history').text()
    expect(text).toContain('Dobbeltbetaling')
    expect(text).toContain('admin.refund.historyDashboard')
  })
})
