import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import FakturaList from '@/components/admin/FakturaList.vue'

// A credit note asks nothing of its payer: the list shows no due date for it
// and never offers it for a reminder or a new KID.

const ensureFreshTotp = vi.fn(async () => true)
const totpAwareFetch = vi.fn()
const askConfirm = vi.fn(async () => false)
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
    user_id: null,
    member_name: 'Synthetic Member',
    member_email: 'synthetic@example.test',
    total_amount: 450,
    issue_date: '2025-05-08',
    due_date: '2025-05-22',
    price_item_name: '',
    fiscal_year: 2025,
    description: '',
    created_at: '2025-05-08T00:00:00Z',
    sent_at: '2025-05-08T00:00:00Z',
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
  row({ id: 'cn', invoice_number: 11, total_amount: -5, due_date: '2025-06-01', credit_state: 'refund', credits_invoice_number: 9 }),
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

describe('FakturaList credit note without payment', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({ items: rows, status: 'sent' })))
    askConfirm.mockClear()
    ensureFreshTotp.mockClear()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('shows no due date for a credit note', async () => {
    const w = await mountSent()
    const text = w.text()
    expect(text).toContain('2025-05-22')
    expect(text).not.toContain('2025-06-01')
  })

  it('leaves a credit note out of a bulk reminder', async () => {
    const w = await mountSent()
    await w.get('thead input[type="checkbox"]').setValue(true)
    const remind = w.findAll('button').find((b) => b.text().includes('admin.faktura.sent.reminderSelected'))
    await remind!.trigger('click')
    await flushPromises()

    const [opts] = askConfirm.mock.calls[0] as unknown as [{ body: string; details: string[] }]
    expect(opts.details).toHaveLength(1)
    expect(opts.details[0]).toContain('#10')
  })

  it('leaves a credit note out of a bulk KID reissue', async () => {
    const w = await mountSent()
    await w.get('thead input[type="checkbox"]').setValue(true)
    const reissue = w.findAll('button').find((b) => b.text().includes('admin.faktura.sent.reissueKidSelected'))
    await reissue!.trigger('click')
    await flushPromises()

    const [opts] = askConfirm.mock.calls[0] as unknown as [{ details: string[] }]
    expect(opts.details).toHaveLength(1)
    expect(opts.details[0]).toContain('#10')
  })
})
