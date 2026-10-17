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
