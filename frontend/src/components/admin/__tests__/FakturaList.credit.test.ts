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
    online_paid_at: null,
    online_payment_opt_in: null,
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

// The credit actions are money moves, shown to the money role only (BRY-372).
const asTreasurer = { initialState: { auth: { user: { roles: ['treasurer'] } } } }

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
    const w = mountWithPlugins(FakturaList, { props: { status: 'imported' }, piniaOptions: asTreasurer })
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
        jsonResponse({ items: [{ id: 'cand', invoice_number: 9, total_amount: 2300, issue_date: '2025-05-08', paid: false, settled: false, imported: true }] }),
      )
      .mockResolvedValueOnce(jsonResponse({ outcome: 'settled_pair', credit_note_number: 11, invoice_number: 9 }))
    confirmStore.choiceValue = 'cand'
    const w = mountWithPlugins(FakturaList, { props: { status: 'imported' }, piniaOptions: asTreasurer })
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
        jsonResponse({ items: [{ id: 'cand', invoice_number: 9, total_amount: 2300, issue_date: '2025-05-08', paid: false, settled: false, imported: true }] }),
      )
      .mockResolvedValueOnce(jsonResponse({ code: 'partial_credit', error: 'partial' }, 409))
    confirmStore.choiceValue = 'cand'
    const w = mountWithPlugins(FakturaList, { props: { status: 'imported' }, piniaOptions: asTreasurer })
    await flushPromises()
    await w.get('[data-testid="credit-settle"]').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('admin.faktura.credit.error.partial_credit')
  })
})

// Manual faktura search (BRY-393): when no faktura shares the credit note's
// payer and amount, or the treasurer wants another one, the list opens a
// search across the club's fakturas.
describe('FakturaList credit-note faktura search', () => {
  const hit = (over: Record<string, unknown>) => ({
    id: 'hit',
    invoice_number: 42,
    total_amount: 2300,
    issue_date: '2025-04-01',
    paid: false,
    settled: false,
    imported: true,
    payer_name: 'Synthetic Payer',
    same_amount: true,
    same_payer: false,
    ...over,
  })
  const mountList = () =>
    mountWithPlugins(FakturaList, {
      props: { status: 'imported' },
      piniaOptions: asTreasurer,
      global: { stubs: { teleport: true } },
    })

  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({ items: rows, status: 'imported' })))
    totpAwareFetch.mockReset()
    askConfirm.mockClear()
    ensureFreshTotp.mockClear()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('opens the search when nothing matches and links a credit note without a payer directly', async () => {
    totpAwareFetch
      .mockResolvedValueOnce(jsonResponse({ items: [], credit_note_has_payer: false }))
      .mockResolvedValueOnce(jsonResponse({ items: [hit({})], credit_note_has_payer: false }))
      .mockResolvedValueOnce(jsonResponse({ outcome: 'settled_pair', credit_note_number: 11, invoice_number: 42 }))
    const w = mountList()
    await flushPromises()

    await w.get('[data-testid="credit-settle"]').trigger('click')
    await flushPromises()

    expect(ensureFreshTotp).toHaveBeenCalled()
    expect(askConfirm).not.toHaveBeenCalled()
    expect(w.get('[data-testid="credit-search-notice"]').text()).toBe('admin.faktura.credit.noCandidates')
    expect(totpAwareFetch.mock.calls[1][0]).toBe('/api/v1/admin/financials/invoices/cn-open/credit-candidates?any=1')
    // No payer on the credit note, so there is nobody to mismatch.
    expect(w.text()).not.toContain('admin.faktura.credit.otherPayer')

    await w.get('[data-testid="credit-search-pick"]').trigger('click')
    await flushPromises()

    expect(askConfirm).not.toHaveBeenCalled()
    const [url, init] = totpAwareFetch.mock.calls[2]
    expect(url).toBe('/api/v1/admin/financials/invoices/cn-open/credit-link')
    expect(JSON.parse(init.body)).toEqual({ invoice_id: 'hit', manual_pick: true })
    expect(w.find('[data-testid="credit-search-hit"]').exists()).toBe(false)
  })

  it('asks before linking a faktura of another payer and sends the confirmation', async () => {
    totpAwareFetch
      .mockResolvedValueOnce(
        jsonResponse({ items: [{ id: 'cand', invoice_number: 9, total_amount: 2300, issue_date: '2025-05-08', paid: false, settled: false, imported: true }] }),
      )
      .mockResolvedValueOnce(jsonResponse({ items: [hit({})], credit_note_has_payer: true }))
      .mockResolvedValueOnce(jsonResponse({ outcome: 'settled_pair', credit_note_number: 11, invoice_number: 42 }))
    confirmStore.choiceValue = '__search__'
    const w = mountList()
    await flushPromises()

    await w.get('[data-testid="credit-settle"]').trigger('click')
    await flushPromises()
    const choice = (askConfirm.mock.calls[0] as unknown as [{ choice: { options: { label: string }[] } }])[0].choice
    expect(choice.options[choice.options.length - 1].label).toBe('admin.faktura.credit.searchOption')
    expect(w.text()).toContain('admin.faktura.credit.otherPayer')

    await w.get('[data-testid="credit-search-pick"]').trigger('click')
    await flushPromises()

    expect(askConfirm).toHaveBeenCalledTimes(2)
    expect((askConfirm.mock.calls[1] as unknown as [{ title: string }])[0].title).toBe(
      'admin.faktura.credit.differentPayerTitle',
    )
    expect(JSON.parse(totpAwareFetch.mock.calls[2][1].body)).toEqual({
      invoice_id: 'hit',
      manual_pick: true,
      confirm_other_member: true,
    })
  })

  it('debounces typing, says when the list is cut, and shows a failed search', async () => {
    totpAwareFetch
      .mockResolvedValueOnce(jsonResponse({ items: [], credit_note_has_payer: false }))
      .mockResolvedValueOnce(jsonResponse({ items: [hit({})], truncated: true, credit_note_has_payer: false }))
      .mockRejectedValueOnce(new Error('network down'))
    const w = mountList()
    await flushPromises()
    await w.get('[data-testid="credit-settle"]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid="credit-search-truncated"]').text()).toBe('admin.faktura.credit.searchTruncated')

    const input = w.get('[data-testid="credit-search-input"]')
    await input.setValue('4')
    await input.setValue('42')
    expect(totpAwareFetch).toHaveBeenCalledTimes(2)
    await new Promise((r) => setTimeout(r, 250))
    await flushPromises()

    expect(totpAwareFetch).toHaveBeenCalledTimes(3)
    expect(totpAwareFetch.mock.calls[2][0]).toBe('/api/v1/admin/financials/invoices/cn-open/credit-candidates?any=1&q=42')
    expect(w.text()).toContain('network down')
    expect(w.find('[data-testid="credit-search-truncated"]').exists()).toBe(false)
  })

  it('does not offer a faktura of a different amount and shows a refusal in the search', async () => {
    totpAwareFetch
      .mockResolvedValueOnce(jsonResponse({ items: [], credit_note_has_payer: false }))
      .mockResolvedValueOnce(
        jsonResponse({
          items: [hit({ id: 'odd', same_amount: false, total_amount: 300 }), hit({})],
          credit_note_has_payer: false,
        }),
      )
      .mockResolvedValueOnce(jsonResponse({ code: 'booking_mismatch', error: 'mismatch' }, 409))
    const w = mountList()
    await flushPromises()
    await w.get('[data-testid="credit-settle"]').trigger('click')
    await flushPromises()

    const picks = w.findAll('[data-testid="credit-search-pick"]')
    expect(picks[0].attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('admin.faktura.credit.otherAmount')

    await picks[1].trigger('click')
    await flushPromises()
    expect(w.get('[data-testid="credit-search-error"]').text()).toBe('admin.faktura.credit.error.booking_mismatch')
  })
})

describe('FakturaList for a board member (BRY-372)', () => {
  // Rows 11 (unlinked) and 13 (credit owed) carry the credit actions.
  const rowActions = [
    'admin.faktura.sent.resend',
    'admin.payLink.title',
    'admin.faktura.credit.settleAction',
    'admin.faktura.credit.unlinkAction',
    'admin.invoiceDrafts.void',
  ]
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({ items: rows, status: 'sent' })))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('lists sent fakturas without send, void, credit or pay-link controls', async () => {
    const w = mountWithPlugins(FakturaList, {
      props: { status: 'sent' },
      piniaOptions: { initialState: { auth: { user: { roles: ['member', 'board'] } } } },
    })
    await flushPromises()
    expect(w.findAll('span.font-mono').map((c) => c.text())).toContain('10')
    expect(w.find('[title="admin.invoiceDrafts.previewPdf"]').exists()).toBe(true)
    for (const key of rowActions) {
      expect(w.find(`[title="${key}"]`).exists(), key).toBe(false)
    }
    expect(w.find('select[title="admin.faktura.sent.onlinePayment.label"]').exists()).toBe(false)
  })

  it('shows the same row actions to the treasurer', async () => {
    const w = mountWithPlugins(FakturaList, { props: { status: 'sent' }, piniaOptions: asTreasurer })
    await flushPromises()
    expect(w.find('select[title="admin.faktura.sent.onlinePayment.label"]').exists()).toBe(true)
    for (const key of rowActions) {
      expect(w.find(`[title="${key}"]`).exists(), key).toBe(true)
    }
  })

  it('shows a refund credit note with its faktura and offers no link, unlink or void', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({
      items: [
        row({ id: 'refund-cn', invoice_number: 21, total_amount: -90, sent_at: '2025-05-08T00:00:00Z', credit_state: 'refund', credits_invoice_number: 20, refund_document: true }),
        row({ id: 'order-cn', invoice_number: 22, total_amount: -300, sent_at: '2025-05-08T00:00:00Z', credit_state: 'refund', refund_document: true }),
      ],
      status: 'sent',
    })))
    const w = mountWithPlugins(FakturaList, { props: { status: 'sent' }, piniaOptions: asTreasurer })
    await flushPromises()
    const badges = w.findAll('[data-testid="credit-badge"]').map((b) => b.text())
    expect(badges).toEqual(['admin.faktura.credit.state.refund', 'admin.faktura.credit.state.refund_order'])
    expect(w.find('[data-testid="credit-settle"]').exists()).toBe(false)
    expect(w.find('[data-testid="credit-unlink"]').exists()).toBe(false)
    expect(w.find('button[title="admin.invoiceDrafts.void"]').exists()).toBe(false)
  })
})
