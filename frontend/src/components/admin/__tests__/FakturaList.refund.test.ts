import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import FakturaList from '@/components/admin/FakturaList.vue'

// Card refunds in the faktura list (BRY-498): the refund and dispute chips,
// the credit notes that did it, a refund's credit note never read as credit
// owed, and "Ny faktura" on a faktura the refunds credited in full.

const ensureFreshTotp = vi.fn(async () => true)
const totpAwareFetch = vi.fn()
const askConfirm = vi.fn(async () => true)
const mutateAsync = vi.fn()

vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp, totpAwareFetch }),
}))
vi.mock('@/stores/confirm', () => ({
  useConfirm: () => askConfirm,
  useConfirmStore: () => ({ choiceValue: '' }),
}))
vi.mock('@/composables/useReissueAfterRefund', async (orig) => ({
  ...(await orig<typeof import('@/composables/useReissueAfterRefund')>()),
  useReissueAfterRefund: () => ({ mutateAsync }),
}))

function row(over: Record<string, unknown>) {
  return {
    id: 'x',
    invoice_number: 1,
    user_id: null,
    member_name: 'Synthetic Member',
    member_email: 'synthetic@example.test',
    total_amount: 450,
    issue_date: '2026-05-08',
    due_date: '2026-05-22',
    price_item_name: '',
    fiscal_year: 2026,
    description: '',
    created_at: '2026-05-08T00:00:00Z',
    sent_at: '2026-05-08T00:00:00Z',
    last_reminder_at: null,
    status: 'open',
    paid: true,
    online_paid_at: '2026-05-09T00:00:00Z',
    online_payment_opt_in: null,
    credits_invoice_number: null,
    credit_state: null,
    credited_by_invoice_number: null,
    refund_state: 'none',
    refunded_amount: 0,
    refund_credit_notes: [],
    can_reissue: false,
    reissued_invoice_number: null,
    reissue_of_invoice_number: null,
    dispute_status: null,
    ...over,
  }
}

const note = (id: string, number: number, amount: number) => ({ id, number, amount, has_pdf: true })

const rows = [
  row({ id: 'plain', invoice_number: 10 }),
  row({
    id: 'full',
    invoice_number: 11,
    credit_state: 'refunded',
    refund_state: 'full',
    refunded_amount: 450,
    refund_credit_notes: [note('cn1', 14, 200), note('cn2', 15, 250)],
    can_reissue: true,
    payment_url: 'https://dashboard.stripe.com/payments/pi_1',
  }),
  row({
    id: 'part',
    invoice_number: 12,
    refund_state: 'partial',
    refunded_amount: 90,
    refund_credit_notes: [note('cn3', 16, 90)],
  }),
  row({
    id: 'done',
    invoice_number: 13,
    credit_state: 'refunded',
    refund_state: 'full',
    refunded_amount: 450,
    refund_credit_notes: [note('cn4', 17, 450)],
    reissued_invoice_number: 18,
  }),
  row({ id: 'cn1', invoice_number: 14, total_amount: -200, credit_state: 'refund', credits_invoice_number: 11, refund_document: true, paid: false, online_paid_at: null }),
  row({ id: 'new', invoice_number: 18, paid: false, online_paid_at: null, reissue_of_invoice_number: 13, dispute_status: 'needs_response', dispute_url: 'https://dashboard.stripe.com/disputes/dp_1' }),
]

const asTreasurer = { initialState: { auth: { user: { roles: ['treasurer'] } } } }

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

async function mountSent() {
  const w = mountWithPlugins(FakturaList, { props: { status: 'sent' }, piniaOptions: asTreasurer })
  await flushPromises()
  return w
}

// The row of a faktura, found by its number chip.
function rowOf(w: Awaited<ReturnType<typeof mountSent>>, number: number) {
  const tr = w.findAll('tbody tr').find((r) => r.find('span.font-mono').text() === String(number))
  if (!tr) throw new Error(`no row ${number}`)
  return tr
}

describe('FakturaList card refunds', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({ items: rows, status: 'sent' })))
    ensureFreshTotp.mockClear()
    askConfirm.mockClear().mockResolvedValue(true)
    mutateAsync.mockReset()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('marks refunded and partly refunded fakturas with the chips and their credit notes', async () => {
    const w = await mountSent()
    const chips = (n: number) => rowOf(w, n).findAll('[data-testid="refund-state-chip"]').map((c) => c.attributes('data-kind'))
    expect(chips(10)).toEqual([])
    expect(chips(11)).toEqual(['refunded'])
    expect(chips(12)).toEqual(['partial'])
    expect(chips(18)).toEqual(['disputed'])

    expect(rowOf(w, 11).findAll('[data-testid="refund-credit-note"]').map((a) => a.attributes('href'))).toEqual([
      '/api/v1/admin/financials/invoices/cn1/pdf',
      '/api/v1/admin/financials/invoices/cn2/pdf',
    ])
    expect(rowOf(w, 11).get('a[data-testid="refund-state-chip"]').attributes('href')).toBe('https://dashboard.stripe.com/payments/pi_1')
    // A fully refunded faktura shows the refund, not a credit state badge.
    expect(rowOf(w, 11).find('[data-testid="credit-badge"]').exists()).toBe(false)
  })

  it('shows a refund credit note as a refund, never as credit owed to the member', async () => {
    const w = await mountSent()
    const badge = rowOf(w, 14).get('[data-testid="credit-badge"]')
    expect(badge.text()).toBe('admin.faktura.credit.state.refund')
    expect(badge.classes().join(' ')).not.toContain('amber')
    // A refund's credit note offers no settle, unlink or void.
    for (const id of ['credit-settle', 'credit-unlink']) expect(rowOf(w, 14).find(`[data-testid="${id}"]`).exists()).toBe(false)
  })

  it('links a reissued faktura both ways', async () => {
    const w = await mountSent()
    expect(rowOf(w, 13).get('[data-testid="reissued-as"]').text()).toBe('admin.refund.reissue.reissuedAs')
    expect(rowOf(w, 18).get('[data-testid="reissue-of"]').text()).toBe('admin.refund.reissue.reissueOf')
  })

  it('offers "Ny faktura" only where the server says it can be done', async () => {
    const w = await mountSent()
    const has = (n: number) => rowOf(w, n).find('[data-testid="reissue-after-refund-button"]').exists()
    expect([10, 11, 12, 13, 14, 18].map(has)).toEqual([false, true, false, false, false, false])
  })

  it('issues the new faktura after the step-up and a confirm, then says what became of it', async () => {
    mutateAsync.mockResolvedValue({ id: 'n', invoice_number: 19, sent_at: '2026-10-29T10:00:00Z' })
    const w = await mountSent()
    ;(fetch as ReturnType<typeof vi.fn>).mockClear()

    await rowOf(w, 11).get('[data-testid="reissue-after-refund-button"]').trigger('click')
    await flushPromises()

    expect(ensureFreshTotp).toHaveBeenCalled()
    expect(askConfirm).toHaveBeenCalledTimes(1)
    expect(mutateAsync).toHaveBeenCalledWith('full')
    expect(w.text()).toContain('admin.refund.reissue.done')
    // The list is read again so the new faktura and the link appear.
    expect(fetch).toHaveBeenCalled()
  })

  it('says so when the new faktura could not be sent', async () => {
    mutateAsync.mockResolvedValue({ id: 'n', invoice_number: 19, sent_at: null })
    const w = await mountSent()
    await rowOf(w, 11).get('[data-testid="reissue-after-refund-button"]').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('admin.refund.reissue.doneUnsent')
  })

  it.each([
    [['pdf_unavailable'], 'admin.refund.reissue.doneWarning'],
    [['club_details_unavailable'], 'admin.refund.reissue.doneNoClub'],
  ])('names the step that failed after the faktura was stored (%s)', async (warnings, key) => {
    mutateAsync.mockResolvedValue({ id: 'n', invoice_number: 19, sent_at: null, warnings })
    const w = await mountSent()
    await rowOf(w, 11).get('[data-testid="reissue-after-refund-button"]').trigger('click')
    await flushPromises()
    expect(w.text()).toContain(key)
  })
})
