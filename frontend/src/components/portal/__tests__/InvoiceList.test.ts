import { describe, it, expect } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import InvoiceList from '@/components/portal/InvoiceList.vue'
import type { MemberInvoice } from '@/composables/useMyInvoices'

// A far-future due date keeps an unpaid invoice out of the overdue branch.
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
    price_item_name: 'Havneplass',
    description: 'Årsavgift',
    has_pdf: true,
    ...overrides,
  }
}

function chip(wrapper: ReturnType<typeof mountWithPlugins>) {
  return wrapper.get('span.inline-flex')
}

describe('InvoiceList status chip (BRY-276)', () => {
  it('shows the paid state when a payment is linked', () => {
    const w = mountWithPlugins(InvoiceList, { props: { invoices: [invoice({ paid: true })] } })
    expect(chip(w).text()).toBe('portal.invoices.status.paid')
    expect(chip(w).classes()).toContain('bg-green-100')
  })

  it('shows the received state for a Vipps pay-link payment awaiting settlement', () => {
    const w = mountWithPlugins(InvoiceList, {
      props: { invoices: [invoice({ payment_received: true })] },
    })
    expect(chip(w).text()).toBe('portal.invoices.status.received')
    expect(chip(w).classes()).toContain('bg-blue-100')
  })

  it('keeps the paid state authoritative over a received marker', () => {
    const w = mountWithPlugins(InvoiceList, {
      props: { invoices: [invoice({ paid: true, payment_received: true })] },
    })
    expect(chip(w).text()).toBe('portal.invoices.status.paid')
  })

  it('shows received ahead of overdue when both would apply', () => {
    const w = mountWithPlugins(InvoiceList, {
      props: { invoices: [invoice({ payment_received: true, due_date: '2000-01-01' })] },
    })
    expect(chip(w).text()).toBe('portal.invoices.status.received')
  })

  it('falls back to overdue and unpaid when nothing is received', () => {
    const overdue = mountWithPlugins(InvoiceList, {
      props: { invoices: [invoice({ due_date: '2000-01-01' })] },
    })
    expect(chip(overdue).text()).toBe('portal.invoices.status.overdue')
    expect(chip(overdue).classes()).toContain('bg-red-100')

    const unpaid = mountWithPlugins(InvoiceList, { props: { invoices: [invoice()] } })
    expect(chip(unpaid).text()).toBe('portal.invoices.status.unpaid')
    expect(chip(unpaid).classes()).toContain('bg-amber-100')
  })
})
