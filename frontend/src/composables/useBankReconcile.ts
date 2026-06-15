import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { computed, type Ref } from 'vue'
import { useFreshTotp } from '@/composables/useFreshTotp'

export interface BankRowSummary {
  id: string
  row_date: string
  amount: number
  kid_number: string
  counterpart: string
  description: string
  bank_account_code: string
  dismissed_at?: string | null
  dismissed_reason?: string | null
  likely_duplicate_of_matched: boolean
  possible_double_payment: boolean
}

export interface InvoiceSuggestion {
  invoice_id: string
  invoice_number: number
  member_name: string
  member_email: string
  price_item_name: string
  issue_date: string
  due_date: string
  total_amount: number
  kid_number: string
  score: number
  confidence_label: 'sterk' | 'sannsynleg' | 'svak' | 'potensiell'
  why_tooltip: string
}

export interface AccountSuggestion {
  code: string
  name: string
  account_type: string
  score: number
  confidence_label: 'sterk' | 'sannsynleg' | 'svak'
  why_tooltip: string
}

export interface BankRowSuggestions {
  invoices: InvoiceSuggestion[]
  accounts: AccountSuggestion[]
}

const BASE = '/api/v1/admin/accounting/bank-rows'

export type BankRowKind = 'all' | 'incoming' | 'outgoing' | 'dismissed' | 'bank' | 'vipps' | 'duplicate' | 'double_payment'

export function useBankUnmatchedRows(kind: Ref<BankRowKind>, q: Ref<string>, year: Ref<number | null>) {
  return useQuery({
    queryKey: computed(() => ['bank-rows', 'unmatched', kind.value, q.value, year.value]),
    queryFn: async () => {
      const url = new URL(`${BASE}/unmatched`, window.location.origin)
      if (kind.value && kind.value !== 'all') url.searchParams.set('kind', kind.value)
      if (q.value) url.searchParams.set('q', q.value)
      if (year.value) url.searchParams.set('year', String(year.value))
      const res = await fetch(url.toString(), { credentials: 'include' })
      if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
      const body = await res.json()
      return body.items as BankRowSummary[]
    },
  })
}

export function useBankUnmatchedCount() {
  return useQuery({
    queryKey: ['bank-rows', 'unmatched', 'count'],
    queryFn: async () => {
      const res = await fetch(`${BASE}/unmatched/count`, { credentials: 'include' })
      if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
      const body = await res.json()
      return body.count as number
    },
    refetchInterval: 30_000,
    refetchOnWindowFocus: true,
  })
}

export function useBankRowSuggestions(rowId: Ref<string | null>) {
  return useQuery({
    queryKey: computed(() => ['bank-rows', 'suggestions', rowId.value]),
    enabled: computed(() => !!rowId.value),
    queryFn: async () => {
      const res = await fetch(`${BASE}/${rowId.value}/suggestions`, { credentials: 'include' })
      if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
      return (await res.json()) as BankRowSuggestions
    },
  })
}

export async function fetchPotentialInvoices(rowId: string, q: string): Promise<InvoiceSuggestion[]> {
  const url = new URL(`${BASE}/${rowId}/potential-invoices`, window.location.origin)
  if (q) url.searchParams.set('q', q)
  const res = await fetch(url.toString(), { credentials: 'include' })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  const body = await res.json()
  return body.items as InvoiceSuggestion[]
}

export function useReconcileMutations() {
  const qc = useQueryClient()
  const { totpAwareFetch } = useFreshTotp()
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['bank-rows'] })
  }

  const assignInvoice = useMutation({
    mutationFn: async ({ rowId, invoiceId }: { rowId: string; invoiceId: string }) => {
      const res = await totpAwareFetch(`${BASE}/${rowId}/assign-invoice`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ invoice_id: invoiceId }),
      })
      if (!res.ok) throw new Error((await res.json().catch(() => ({})))?.error ?? res.statusText)
      return res.json()
    },
    onSuccess: invalidate,
  })

  const assignAccount = useMutation({
    mutationFn: async ({ rowId, accountCode, kind }: { rowId: string; accountCode: string; kind: 'expense' | 'revenue' }) => {
      const res = await totpAwareFetch(`${BASE}/${rowId}/assign-account`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ account_code: accountCode, kind }),
      })
      if (!res.ok) throw new Error((await res.json().catch(() => ({})))?.error ?? res.statusText)
      return res.json()
    },
    onSuccess: invalidate,
  })

  const dismiss = useMutation({
    mutationFn: async ({ rowId, reason }: { rowId: string; reason: string }) => {
      const res = await totpAwareFetch(`${BASE}/${rowId}/dismiss`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ reason }),
      })
      if (!res.ok) throw new Error((await res.json().catch(() => ({})))?.error ?? res.statusText)
      return res.json()
    },
    onSuccess: invalidate,
  })

  const unassign = useMutation({
    mutationFn: async ({ rowId }: { rowId: string }) => {
      const res = await totpAwareFetch(`${BASE}/${rowId}/unassign`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ confirm: true }),
      })
      if (!res.ok) throw new Error((await res.json().catch(() => ({})))?.error ?? res.statusText)
      return res.json()
    },
    onSuccess: invalidate,
  })

  return { assignInvoice, assignAccount, dismiss, unassign }
}

export const DISMISS_REASONS = [
  'bounced',
  'internal_transfer',
  'duplicate',
  'double_payment',
  'bank_fee',
  'refund_or_credit',
  'overpayment',
  'unidentifiable',
  'test_transaction',
] as const
export type DismissReason = (typeof DISMISS_REASONS)[number]
