import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'

// When invoice/payment data was last refreshed from the bank for the
// caller's club (the most recent bank-statement import). Invoice paid/unpaid
// status only changes when a bank import is reconciled, so this is the
// freshness signal shown on the faktura and economy surfaces. Auth-only on
// the backend, so both the member portal and admin views can read it.
export function usePaymentDataUpdatedAt() {
  const query = useQuery({
    queryKey: ['payment-data-updated-at'],
    queryFn: async () => {
      const res = await fetch('/api/v1/members/me/invoices/updated-at', {
        credentials: 'include',
      })
      if (!res.ok) throw new Error('failed')
      const json = (await res.json()) as { updated_at: string | null }
      return json.updated_at
    },
  })

  const updatedAt = computed(() => query.data.value ?? null)
  return { ...query, updatedAt }
}
