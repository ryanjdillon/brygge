import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import FakturaList from '@/components/admin/FakturaList.vue'

// Credit-note handling in the faktura list (BRY-342): credit notes and the
// fakturas they settle are never past due, and an unlinked credit note can
// be settled against a candidate faktura.

const ensureFreshTotp = vi.fn(async () => true)
const totpAwareFetch = vi.fn()
const askConfirm = vi.fn(async () => true)
const confirmStore = { choiceValue: '' }

vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp, totpAwareFetch }),
}))
vi.mock('@/stores/confirm', () => ({
  useConfirm: () => askConfirm,
  useConfirmStore: () => confirmStore,
}))

function row(over: Record<string, unknown>) {
  return {
    id: 'x',
    invoice_number: 1,
    user_id: null, // no member link, so the row renders without the admin-users route
    member_name: 'Synthetic Member',
    member_email: 'synthetic@example.test',
    total_amount: 450,
    issue_date: '2025-05-08',
    due_date: '2025-05-22',
    price_item_name: '',
    fiscal_year: 2025,
    description: '',
    created_at: '2025-05-08T00:00:00Z',
    sent_at: null,
    last_reminder_at: null,
    status: 'open',
    paid: false,
    vipps_paid_at: null,
    vipps_payment_link_opt_in: null,
    credits_invoice_number: null,
    credit_state: null,
    credited_by_invoice_number: null,
    ...over,
  }
}

const rows = [
  row({ id: 'owed', invoice_number: 10, total_amount: 450 }),
  row({ id: 'cn-open', invoice_number: 11, total_amount: -2300, credit_state: 'unlinked' }),
  row({ id: 'credited', invoice_number: 12, total_amount: 800, credited_by_invoice_number: 13 }),
  row({ id: 'cn-owed', invoice_number: 13, total_amount: -800, credit_state: 'credit_owed', credits_invoice_number: 12 }),
]

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('FakturaList credit notes', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({ items: rows, status: 'imported' })))
    totpAwareFetch.mockReset()
    askConfirm.mockClear()
    ensureFreshTotp.mockClear()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('never shows credit notes or credited fakturas as past due', async () => {
    const w = mountWithPlugins(FakturaList, { props: { status: 'imported' } })
    await flushPromises()
    const chips = w.findAll('span.font-mono')
    const byNumber = Object.fromEntries(chips.map((c) => [c.text(), c.classes().join(' ')]))
    expect(byNumber['10']).toContain('bg-red-100') // owed and overdue
    expect(byNumber['11']).toContain('bg-sky-100') // credit note
    expect(byNumber['12']).toContain('bg-green-100') // settled by credit
    expect(byNumber['13']).toContain('bg-sky-100')
    expect(w.get('[data-testid="credit-chip"]').text()).toContain('2')
    const badges = w.findAll('[data-testid="credit-badge"]').map((b) => b.text())
    expect(badges).toEqual(['admin.faktura.credit.state.unlinked', 'admin.faktura.credit.state.credit_owed'])
  })

  it('settles an unlinked credit note against the chosen candidate after a step-up', async () => {
    totpAwareFetch
      .mockResolvedValueOnce(
        jsonResponse({ items: [{ id: 'cand', invoice_number: 9, total_amount: 2300, issue_date: '2025-05-08', paid: false, imported: true }] }),
      )
      .mockResolvedValueOnce(jsonResponse({ outcome: 'settled_pair', credit_note_number: 11, invoice_number: 9 }))
    confirmStore.choiceValue = 'cand'
    const w = mountWithPlugins(FakturaList, { props: { status: 'imported' } })
    await flushPromises()

    await w.get('[data-testid="credit-settle"]').trigger('click')
    await flushPromises()

    expect(ensureFreshTotp).toHaveBeenCalled()
    expect(totpAwareFetch.mock.calls[0][0]).toBe('/api/v1/admin/financials/invoices/cn-open/credit-candidates')
    const [url, init] = totpAwareFetch.mock.calls[1]
    expect(url).toBe('/api/v1/admin/financials/invoices/cn-open/credit-link')
    expect(JSON.parse(init.body)).toEqual({ invoice_id: 'cand' })
  })

  it('reports why a link was refused', async () => {
    totpAwareFetch
      .mockResolvedValueOnce(
        jsonResponse({ items: [{ id: 'cand', invoice_number: 9, total_amount: 2300, issue_date: '2025-05-08', paid: false, imported: true }] }),
      )
      .mockResolvedValueOnce(jsonResponse({ code: 'partial_credit', error: 'partial' }, 409))
    confirmStore.choiceValue = 'cand'
    const w = mountWithPlugins(FakturaList, { props: { status: 'imported' } })
    await flushPromises()
    await w.get('[data-testid="credit-settle"]').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('admin.faktura.credit.error.partial_credit')
  })
})
