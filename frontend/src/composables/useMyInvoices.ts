import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import type { components } from '@/types/api'

export type MemberInvoice = components['schemas']['MemberInvoice']

// The current member's own invoices. The backend scopes the result to
// the authenticated user, so members only ever see their own fakturas.
export function useMyInvoices() {
  const client = useApiClient()

  const query = useQuery({
    queryKey: ['portal', 'invoices'],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/members/me/invoices')) ?? [],
  })

  const invoices = computed<MemberInvoice[]>(() => query.data.value ?? [])
  // "Unpaid" is whatever the member still owes; a credited or 0 kr
  // (fully exempted) faktura is settled even without a payment.
  const unpaid = computed(() => invoices.value.filter((i) => i.owed))
  const paid = computed(() => invoices.value.filter((i) => !i.owed))

  return { ...query, invoices, unpaid, paid }
}

// An invoice is overdue when it is unpaid and its due date is in the past.
export function isOverdue(inv: MemberInvoice): boolean {
  if (!inv.owed || inv.paid || inv.credited || inv.credit_note) return false
  const due = new Date(inv.due_date)
  if (Number.isNaN(due.getTime())) return false
  return due.getTime() < Date.now()
}

// A faktura that nets to 0 kr (e.g. a fully exempted board slip) needs no
// payment; the portal shows it as such and hides its KID.
export function isNothingDue(inv: MemberInvoice): boolean {
  return !inv.credit_note && inv.total_amount === 0
}

// One status vocabulary for every member-facing invoice badge (the full
// list and the dashboard chips). A credit note is money the club owes
// back, and a faktura it settles is done; neither reads as unpaid or
// overdue (BRY-342). A card refund repays the card at once, so its credit
// note is not credit owed and a faktura it credited reads as refunded
// (BRY-498). The key is the suffix of portal.invoices.status.*.
export type InvoiceStatus =
  | 'refund_credit_note'
  | 'credit_note'
  | 'refunded'
  | 'credited'
  | 'nothing_due'
  | 'paid'
  | 'received'
  | 'overdue'
  | 'unpaid'

export function invoiceStatus(inv: MemberInvoice): InvoiceStatus {
  if (inv.credit_note && inv.refund_document) return 'refund_credit_note'
  if (inv.credit_note) return 'credit_note'
  if (inv.refund_state === 'full') return 'refunded'
  if (inv.credited) return 'credited'
  if (isNothingDue(inv)) return 'nothing_due'
  if (inv.paid) return 'paid'
  if (inv.payment_received) return 'received'
  if (isOverdue(inv)) return 'overdue'
  return 'unpaid'
}

const statusTone: Record<InvoiceStatus, string> = {
  refund_credit_note: 'bg-sky-100 text-sky-800',
  credit_note: 'bg-sky-100 text-sky-800',
  refunded: 'bg-sky-100 text-sky-800',
  credited: 'bg-green-100 text-green-800',
  nothing_due: 'bg-green-100 text-green-800',
  paid: 'bg-green-100 text-green-800',
  received: 'bg-blue-100 text-blue-800',
  overdue: 'bg-red-100 text-red-800',
  unpaid: 'bg-amber-100 text-amber-800',
}

export function invoiceStatusClass(inv: MemberInvoice): string {
  return statusTone[invoiceStatus(inv)]
}
