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
  const unpaid = computed(() => invoices.value.filter((i) => !i.paid))
  const paid = computed(() => invoices.value.filter((i) => i.paid))

  return { ...query, invoices, unpaid, paid }
}

// An invoice is overdue when it is unpaid and its due date is in the past.
export function isOverdue(inv: MemberInvoice): boolean {
  if (inv.paid) return false
  const due = new Date(inv.due_date)
  if (Number.isNaN(due.getTime())) return false
  return due.getTime() < Date.now()
}
