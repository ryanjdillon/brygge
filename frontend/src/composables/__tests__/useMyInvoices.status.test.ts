import { describe, it, expect } from 'vitest'
import { invoiceStatus, invoiceStatusClass, type MemberInvoice } from '@/composables/useMyInvoices'

const PAST = '2020-01-01'
const FUTURE = '2999-01-01'

function invoice(overrides: Partial<MemberInvoice> = {}): MemberInvoice {
  return {
    id: 'inv-1',
    invoice_number: 70,
    kid_number: '1234567890',
    total_amount: 1500,
    issue_date: '2026-01-01',
    due_date: FUTURE,
    sent_at: '2026-01-01T00:00:00Z',
    paid: false,
    payment_received: false,
    credit_note: false,
    credited: false,
    owed: true,
    price_item_name: 'Havneplass',
    description: 'Årsavgift',
    has_pdf: true,
    ...overrides,
  }
}

// The dashboard chips and the invoice list share this vocabulary, so a
// credited faktura or a credit note can never read as unpaid in either.
describe('invoiceStatus', () => {
  it.each([
    ['credit note, even past due', invoice({ credit_note: true, total_amount: -1500, owed: false, due_date: PAST }), 'credit_note', 'bg-sky-100'],
    ['faktura settled by a credit note', invoice({ credited: true, owed: false, due_date: PAST }), 'credited', 'bg-green-100'],
    ['fully exempted 0 kr faktura', invoice({ total_amount: 0, owed: false, due_date: PAST }), 'nothing_due', 'bg-green-100'],
    ['paid', invoice({ paid: true, owed: false }), 'paid', 'bg-green-100'],
    ['Vipps payment received, not settled', invoice({ payment_received: true, owed: false }), 'received', 'bg-blue-100'],
    ['owed and past due', invoice({ due_date: PAST }), 'overdue', 'bg-red-100'],
    ['owed, not yet due', invoice(), 'unpaid', 'bg-amber-100'],
  ])('%s', (_name, inv, status, tone) => {
    expect(invoiceStatus(inv)).toBe(status)
    expect(invoiceStatusClass(inv)).toContain(tone)
  })
})
