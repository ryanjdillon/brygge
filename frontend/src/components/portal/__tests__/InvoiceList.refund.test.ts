import { describe, it, expect } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import InvoiceList from '@/components/portal/InvoiceList.vue'
import { invoiceStatus, invoiceStatusClass, type MemberInvoice } from '@/composables/useMyInvoices'

// What a member sees of a card refund (BRY-498): a clear status and the credit
// notes, never a credit the club owes, never an unpaid or overdue faktura.

const FUTURE = '2999-01-01'

function invoice(overrides: Partial<MemberInvoice> = {}): MemberInvoice {
  return {
    id: 'inv-1',
    invoice_number: 70,
    kid_number: '1234567890',
    total_amount: 450,
    issue_date: '2026-01-01',
    due_date: FUTURE,
    sent_at: '2026-01-01T00:00:00Z',
    paid: true,
    payment_received: false,
    credit_note: false,
    credited: false,
    owed: false,
    price_item_name: 'Gjesteplass',
    description: '',
    has_pdf: true,
    ...overrides,
  }
}

describe('member invoice status after a card refund', () => {
  it.each([
    ['refunded in full (also credited)', invoice({ credited: true, refund_state: 'full', refunded_amount: 450, due_date: '2020-01-01' }), 'refunded'],
    ['refund credit note, not credit owed', invoice({ credit_note: true, refund_document: true, total_amount: -450, paid: false }), 'refund_credit_note'],
    ['refunded in part stays paid', invoice({ refund_state: 'partial', refunded_amount: 90 }), 'paid'],
    ['a manual credit note', invoice({ credit_note: true, total_amount: -450, paid: false }), 'credit_note'],
  ])('%s', (_name, inv, status) => {
    expect(invoiceStatus(inv)).toBe(status)
    expect(invoiceStatusClass(inv)).toBeTruthy()
  })
})

describe('InvoiceList card refunds', () => {
  const credit = (id: string, number: number, has_pdf = true) => ({ id, number, amount: 450, has_pdf })

  it('says a fully refunded faktura was paid back, with a link to each credit note', () => {
    const w = mountWithPlugins(InvoiceList, {
      props: {
        invoices: [invoice({ credited: true, refund_state: 'full', refunded_amount: 450, refund_credit_notes: [credit('cn1', 71), credit('cn2', 72, false)] })],
      },
    })
    expect(w.get('span.inline-flex').text()).toBe('portal.invoices.status.refunded')
    const note = w.get('[data-testid="refund-note"]')
    expect(note.text()).toContain('portal.invoices.refundedFull')
    const links = note.findAll('a')
    expect(links.map((a) => a.attributes('href'))).toEqual(['/api/v1/members/me/invoices/cn1/pdf'])
    expect(links[0].text()).toBe('portal.invoices.creditNoteNumber')
    // A credit note without a PDF yet is named, not linked.
    expect(note.text()).toContain('portal.invoices.creditNoteNumber')
    expect(note.findAll('a')).toHaveLength(1)
  })

  it('names the amount of a partial refund on a faktura that stays paid', () => {
    const w = mountWithPlugins(InvoiceList, {
      props: { invoices: [invoice({ refund_state: 'partial', refunded_amount: 90, refund_credit_notes: [credit('cn1', 71)] })] },
    })
    expect(w.get('span.inline-flex').text()).toBe('portal.invoices.status.paid')
    expect(w.get('[data-testid="refund-note"]').text()).toContain('portal.invoices.refundedPartly')
  })

  it('explains a refund credit note as repaid to the card', () => {
    const w = mountWithPlugins(InvoiceList, {
      props: { invoices: [invoice({ total_amount: -450, credit_note: true, refund_document: true, paid: false })] },
    })
    expect(w.get('span.inline-flex').text()).toBe('portal.invoices.status.refund_credit_note')
    expect(w.get('[data-testid="refund-note"]').text()).toBe('portal.invoices.refundCreditNote')
  })

  it('adds nothing to a faktura no refund touched', () => {
    const w = mountWithPlugins(InvoiceList, { props: { invoices: [invoice({ refund_state: 'none' }), invoice({ id: 'b' })] } })
    expect(w.find('[data-testid="refund-note"]').exists()).toBe(false)
  })
})
