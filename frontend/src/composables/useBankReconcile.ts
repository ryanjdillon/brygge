import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { computed, type Ref } from 'vue'
import { useApi } from '@/composables/useApi'

export interface BankRowSummary {
  id: string
  row_date: string
  amount: number
  kid_number: string
  reference: string
  counterpart: string
  description: string
  bank_account_code: string
  dismissed_at?: string | null
  dismissed_reason?: string | null
  likely_duplicate_of_matched: boolean
  possible_double_payment: boolean
  suggestion_count: number
  // True for a batched Vipps settlement payout ("Utb. N Vippsnr M"). Under the
  // per-charge model its individual charges are reconciled one by one and this
  // row auto-clears — so it renders as a quiet auto-clearing item.
  vipps_payout: boolean
  // An in-flight "Overførsel" from a statement exported on the day the
  // transfer was under way; a re-export of that day replaces it (BRY-378).
  provisional?: boolean
}

// A single individual Vipps settlement charge awaiting reconciliation — one
// line per payer in the Tildel queue under the per-charge model (BRY-227).
export interface VippsCharge {
  id: string
  source: 'vipps'
  date: string
  amount: number
  counterpart: string
  description: string
  reference: string
  suggestion_count: number
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

export type BankRowDirection = 'incoming' | 'outgoing' | 'dismissed'
export type BankRowSource = 'bank' | 'vipps'
export type BankRowFlag = 'duplicate' | 'double_payment'

// Tildel filter: at most one direction and one source, plus any flags.
// The groups combine with AND on the server.
export interface BankRowFilter {
  direction: BankRowDirection | null
  source: BankRowSource | null
  flags: BankRowFlag[]
}

export const BANK_ROW_DIRECTIONS: BankRowDirection[] = ['incoming', 'outgoing', 'dismissed']
export const BANK_ROW_SOURCES: BankRowSource[] = ['bank', 'vipps']
export const BANK_ROW_FLAGS: BankRowFlag[] = ['duplicate', 'double_payment']

export function emptyBankRowFilter(): BankRowFilter {
  return { direction: null, source: null, flags: [] }
}

/** The comma-separated `kind` query value for a filter; '' means no filter. */
export function bankRowKindParam(f: BankRowFilter): string {
  return [f.direction, f.source, ...f.flags].filter(Boolean).join(',')
}

export function useBankUnmatchedRows(filter: Ref<BankRowFilter>, q: Ref<string>, year: Ref<number | null>) {
  const kind = computed(() => bankRowKindParam(filter.value))
  return useQuery({
    queryKey: computed(() => ['bank-rows', 'unmatched', kind.value, q.value, year.value]),
    queryFn: async () => {
      const url = new URL(`${BASE}/unmatched`, window.location.origin)
      if (kind.value) url.searchParams.set('kind', kind.value)
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

export interface MissingVippsSettlement {
  bank_row_id: string
  date: string
  amount: number
  settlement_number: string
  msn: string
}

// Vipps payouts sitting unmatched because their settlement export hasn't been
// imported — can't be split into member payments until it is.
export function useMissingVippsSettlements() {
  return useQuery({
    queryKey: ['accounting', 'vipps-missing-settlements'],
    queryFn: () =>
      useApi().fetchApi<MissingVippsSettlement[]>(
        '/api/v1/admin/accounting/vipps-missing-settlements',
      ),
  })
}

// Whether the club is on the per-charge Vipps reconciliation model — gates
// whether the Tildel tab surfaces individual charges + quiet payout items.
export function useVippsPerChargeEnabled() {
  return useQuery({
    queryKey: ['accounting', 'vipps-per-charge-enabled'],
    queryFn: async () => {
      const body = await useApi().fetchApi<{ vipps_per_charge_enabled?: boolean }>(
        '/api/v1/admin/settings/site',
      )
      return !!body.vipps_per_charge_enabled
    },
    staleTime: 300_000,
  })
}

// Individual unreconciled Vipps charges for the Tildel queue (per-charge model).
export function useUnreconciledVippsCharges(enabled: Ref<boolean>) {
  return useQuery({
    queryKey: ['vipps-charges', 'unmatched'],
    enabled,
    queryFn: async () => {
      const body = await useApi().fetchApi<{ items: VippsCharge[] }>(
        '/api/v1/admin/accounting/vipps-charges/unmatched',
      )
      return body.items
    },
    refetchOnWindowFocus: true,
  })
}

export interface VippsPayoutClearResult {
  ready: boolean
  settlement_number: string
  msn: string
  unreconciled_charges: number
  already_cleared: boolean
  clearing_balance: number
}

// Actions on individual Vipps charges + the settlement payout auto-clear.
export function useVippsChargeMutations() {
  const qc = useQueryClient()
  const CBASE = '/api/v1/admin/accounting/vipps-charges'
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['vipps-charges'] })
    qc.invalidateQueries({ queryKey: ['bank-rows'] })
  }

  const assignInvoice = useMutation({
    mutationFn: ({ rowId, invoiceId }: { rowId: string; invoiceId: string }) =>
      useApi().fetchApi(`${CBASE}/${rowId}/assign-invoice`, {
        method: 'POST',
        body: JSON.stringify({ invoice_id: invoiceId }),
      }),
    onSuccess: invalidate,
  })

  const bilag = useMutation({
    mutationFn: ({ rowId, accountCode, description }: { rowId: string; accountCode: string; description?: string }) =>
      useApi().fetchApi(`${CBASE}/${rowId}/assign-account`, {
        method: 'POST',
        body: JSON.stringify({ account_code: accountCode, description }),
      }),
    onSuccess: invalidate,
  })

  const dismiss = useMutation({
    mutationFn: ({ rowId, reason }: { rowId: string; reason: string }) =>
      useApi().fetchApi(`${CBASE}/${rowId}/dismiss`, {
        method: 'POST',
        body: JSON.stringify({ reason }),
      }),
    onSuccess: invalidate,
  })

  const clearPayout = useMutation({
    mutationFn: ({ rowId }: { rowId: string }) =>
      useApi().fetchApi<VippsPayoutClearResult>(
        `/api/v1/admin/accounting/bank-rows/${rowId}/clear-vipps-payout`,
        { method: 'POST' },
      ),
    onSuccess: invalidate,
  })

  return { assignInvoice, bilag, dismiss, clearPayout }
}

// Candidate invoices for assigning a Vipps charge to a faktura.
export async function fetchVippsChargePotentialInvoices(chargeId: string, q: string): Promise<InvoiceSuggestion[]> {
  const url = new URL(
    `/api/v1/admin/accounting/vipps-charges/${chargeId}/potential-invoices`,
    window.location.origin,
  )
  if (q) url.searchParams.set('q', q)
  const body = await useApi().fetchApi<{ items: InvoiceSuggestion[] }>(url.toString())
  return body.items
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

export interface RefundPendingRow {
  id: string
  row_date: string
  amount: number
  counterpart: string
  description: string
  kid_number: string
  dismissed_reason: string
  dismissed_at: string
  bank_account_code: string
  import_id: string
}

export interface RefundOutboundCandidate {
  id: string
  row_date: string
  amount: number
  counterpart: string
  description: string
}

export function usePendingRefunds() {
  return useQuery({
    queryKey: ['bank-rows', 'pending-refunds'],
    queryFn: async () => {
      const body = await useApi().fetchApi<{ items: RefundPendingRow[] }>(`${BASE}/pending-refunds`)
      return body.items
    },
    refetchInterval: 30_000,
    refetchOnWindowFocus: true,
  })
}

export function usePendingRefundCount() {
  return useQuery({
    queryKey: ['bank-rows', 'pending-refunds', 'count'],
    queryFn: async () => {
      const body = await useApi().fetchApi<{ count: number }>(`${BASE}/pending-refunds/count`)
      return body.count
    },
    refetchInterval: 30_000,
    refetchOnWindowFocus: true,
  })
}

export function useSuggestRefundOutbound(rowId: Ref<string | null>) {
  return useQuery({
    queryKey: computed(() => ['bank-rows', 'suggest-refund-outbound', rowId.value]),
    enabled: computed(() => !!rowId.value),
    queryFn: async () => {
      const body = await useApi().fetchApi<{ items: RefundOutboundCandidate[] }>(
        `${BASE}/${rowId.value}/suggest-refund-outbound`,
      )
      return body.items
    },
  })
}

export function usePairRefundMutation() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ rowId, outboundRowId }: { rowId: string; outboundRowId: string }) =>
      useApi().fetchApi(`${BASE}/${rowId}/pair-refund`, {
        method: 'POST',
        body: JSON.stringify({ outbound_row_id: outboundRowId }),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['bank-rows'] })
    },
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
