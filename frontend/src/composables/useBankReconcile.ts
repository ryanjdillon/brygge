import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { computed, type Ref } from 'vue'
import { useApi } from '@/composables/useApi'

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
      const body = await useApi().fetchApi<{ items: BankRowSummary[] }>(url.toString())
      return body.items
    },
  })
}

export function useBankUnmatchedCount(year?: Ref<number | null>) {
  return useQuery({
    queryKey: computed(() => ['bank-rows', 'unmatched', 'count', year?.value ?? null]),
    queryFn: async () => {
      const url = new URL(`${BASE}/unmatched/count`, window.location.origin)
      if (year?.value) url.searchParams.set('year', String(year.value))
      const body = await useApi().fetchApi<{ count: number }>(url.toString())
      return body.count
    },
    refetchInterval: 30_000,
    refetchOnWindowFocus: true,
  })
}

export function useBankUnmatchedCountsByYear() {
  return useQuery({
    queryKey: ['bank-rows', 'unmatched', 'count-by-year'],
    queryFn: async () => {
      const body = await useApi().fetchApi<{ by_year: Record<string, number> }>(
        `${BASE}/unmatched/count-by-year`,
      )
      return body.by_year
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
      return useApi().fetchApi<BankRowSuggestions>(`${BASE}/${rowId.value}/suggestions`)
    },
  })
}

export async function fetchPotentialInvoices(rowId: string, q: string, amount?: number): Promise<InvoiceSuggestion[]> {
  const url = new URL(`${BASE}/${rowId}/potential-invoices`, window.location.origin)
  if (q) url.searchParams.set('q', q)
  if (amount != null && amount > 0) url.searchParams.set('amount', String(amount))
  const body = await useApi().fetchApi<{ items: InvoiceSuggestion[] }>(url.toString())
  return body.items
}

export function useReconcileMutations() {
  const qc = useQueryClient()
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['bank-rows'] })
  }

  const assignInvoice = useMutation({
    mutationFn: ({ rowId, invoiceId }: { rowId: string; invoiceId: string }) =>
      useApi().fetchApi(`${BASE}/${rowId}/assign-invoice`, {
        method: 'POST',
        body: JSON.stringify({ invoice_id: invoiceId }),
      }),
    onSuccess: invalidate,
  })

  const assignAccount = useMutation({
    mutationFn: ({ rowId, accountCode, kind, description }: { rowId: string; accountCode: string; kind: 'expense' | 'revenue'; description?: string }) =>
      useApi().fetchApi(`${BASE}/${rowId}/assign-account`, {
        method: 'POST',
        body: JSON.stringify({ account_code: accountCode, kind, description }),
      }),
    onSuccess: invalidate,
  })

  const dismiss = useMutation({
    mutationFn: ({ rowId, reason }: { rowId: string; reason: string }) =>
      useApi().fetchApi(`${BASE}/${rowId}/dismiss`, {
        method: 'POST',
        body: JSON.stringify({ reason }),
      }),
    onSuccess: invalidate,
  })

  const unassign = useMutation({
    mutationFn: ({ rowId }: { rowId: string }) =>
      useApi().fetchApi(`${BASE}/${rowId}/unassign`, {
        method: 'POST',
        body: JSON.stringify({ confirm: true }),
      }),
    onSuccess: invalidate,
  })

  return { assignInvoice, assignAccount, dismiss, unassign }
}

export function useAssignMultiInvoiceMutation() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ rowIds, invoiceId }: { rowIds: string[]; invoiceId: string }) =>
      useApi().fetchApi(`${BASE}/assign-invoice-multi`, {
        method: 'POST',
        body: JSON.stringify({ row_ids: rowIds, invoice_id: invoiceId }),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['bank-rows'] }),
  })
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
  'superseded',
] as const
export type DismissReason = (typeof DISMISS_REASONS)[number]
