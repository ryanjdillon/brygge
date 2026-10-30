import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { computed, type Ref } from 'vue'
import { useApi } from '@/composables/useApi'
import { ApiError } from '@/lib/errors'
import { useFiscalPeriods } from '@/composables/useAccounting'

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
  // Whose payout the row carries: a Vipps settlement or a Stripe payout
  // (the club's payout descriptor). Such a row is booked only by that
  // provider's payout clearing.
  provider_payout?: ProviderPayout | null
  // For a Stripe payout: what its clearing waits for, or why it is left to
  // a person (server-provided text).
  provider_payout_reason?: string
  // A Stripe payout no sweep will clear (manual, ambiguous, no payout): book
  // it by hand in the ledger and dismiss the row.
  provider_payout_by_hand?: boolean
  // An in-flight "Overførsel" from a statement exported on the day the
  // transfer was under way; a re-export of that day replaces it (BRY-378).
  provisional?: boolean
  // Another unbooked row looks like it belongs with this one (BRY-380):
  // "strong" rows must be booked together or confirmed as separate.
  partner_hint?: PartnerStrength | null
  // Dated in a reference year (BRY-418): only booking an outgoing row to a
  // cost account, and dismissing it, are left.
  reference_period?: boolean
}

export type ProviderPayout = 'vipps' | 'stripe'

export type PartnerStrength = 'strong' | 'weak'
export type PartnerReason = 'same_account' | 'same_name' | 'reverses' | 'nets_to_price'

// A row suggested as belonging with another; `net` is what one bilag for
// both would book.
export interface PartnerSuggestion {
  id: string
  row_date: string
  amount: number
  counterpart: string
  description: string
  strength: PartnerStrength
  reasons: PartnerReason[]
  days_apart: number
  net: number
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
  // The faktura's member; '' when it has none.
  user_id: string
  // Bank rows only: the row's payer already resolves to this member, so
  // there is nothing to remember for this faktura.
  payer_resolves_here?: boolean
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
  // 'price_item': the amount fits the price items named in why_tooltip.
  reason?: 'price_item'
}

export interface BankRowSuggestions {
  invoices: InvoiceSuggestion[]
  accounts: AccountSuggestion[]
  // Incoming bank rows only.
  payer_alias?: PayerAliasState
}

// How a bank row's payer resolves today (BRY-383). taken means the name is
// some member's own name or alias, without saying whose; which candidate the
// payer already resolves to is each faktura's payer_resolves_here.
export interface PayerAliasState {
  payer: string
  pass: 'alias' | 'name' | 'none'
  taken: boolean
  own_club: boolean
}

type AliasCandidate = Pick<InvoiceSuggestion, 'user_id' | 'payer_resolves_here'>

export interface PayerAliasFields {
  remember_payer: boolean
  confirm_alias_override: boolean
}

/**
 * Whether Tildel offers "Hugs betalaren" for a payer: there is a name, it is
 * not the club, and some candidate faktura belongs to a member the payer
 * does not already resolve to. Otherwise there is nothing to learn.
 */
export function payerAliasOffered(
  state: PayerAliasState | null | undefined,
  candidates: AliasCandidate[],
): boolean {
  if (!state || !state.payer.trim() || state.own_club) return false
  return candidates.some((c) => !!c.user_id && !c.payer_resolves_here)
}

/**
 * The request fields for assigning to inv. A faktura of the member the payer
 * already resolves to, or one without a member, never remembers; ticking the
 * box for a taken name confirms the override.
 */
export function payerAliasFields(
  state: PayerAliasState | null | undefined,
  offered: boolean,
  checked: boolean,
  inv: AliasCandidate,
): PayerAliasFields {
  const remember = !!state && offered && checked && !!inv.user_id && !inv.payer_resolves_here
  return { remember_payer: remember, confirm_alias_override: remember && !!state?.taken }
}

/**
 * The payer state several rows share, or null when they have different
 * payers (compared trimmed and case-insensitively, which the server's
 * normalisation never splits further) or any is the club.
 */
export function sharedPayerAlias(states: (PayerAliasState | undefined)[]): PayerAliasState | null {
  const first = states[0]
  if (!first || !first.payer.trim()) return null
  const key = (p: string) => p.trim().toLowerCase()
  return states.every((st) => !!st && !st.own_club && key(st.payer) === key(first.payer)) ? first : null
}

/** True when an assign refused to remember a payer that is another member's. */
export function isAliasConflictError(e: unknown): boolean {
  return e instanceof ApiError && e.status === 409 && e.message === 'alias_conflict'
}

/**
 * True when an assign was refused because the faktura was imported from the
 * previous system and its receivable isn't in the opening balance yet (BRY-386).
 */
export function isImportedNoOpeningBalanceError(e: unknown): boolean {
  return e instanceof ApiError && e.status === 409 && e.code === 'imported_no_opening_balance'
}

/**
 * True when a booking was refused because it would put anything but a cost
 * into a reference year (BRY-418). Nothing was booked.
 */
export function isReferencePeriodError(e: unknown): boolean {
  return e instanceof ApiError && e.status === 409 && e.code === 'reference_period'
}

/**
 * True when an assign was refused because the faktura is credited in full:
 * booking a payment against it would credit receivables a second time
 * (BRY-508). Nothing was booked.
 */
export function isInvoiceCreditedError(e: unknown): boolean {
  return e instanceof ApiError && e.status === 409 && e.code === 'invoice_credited'
}

/** True when an assign was aimed at a credit note, which no payment can clear. */
export function isInvoiceNotDebtError(e: unknown): boolean {
  return e instanceof ApiError && e.status === 409 && e.code === 'invoice_not_debt'
}

/** True when an assign was aimed at a voided faktura, which no payment can clear. */
export function isInvoiceVoidedError(e: unknown): boolean {
  return e instanceof ApiError && e.status === 409 && e.code === 'invoice_voided'
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
  // Not a filter but a mode: the outgoing rows of reference years, which the
  // other views leave out entirely. It excludes the other groups.
  reference?: boolean
  // With reference: the dismissed rows instead of the open ones.
  referenceDismissed?: boolean
}

export const BANK_ROW_DIRECTIONS: BankRowDirection[] = ['incoming', 'outgoing', 'dismissed']
export const BANK_ROW_SOURCES: BankRowSource[] = ['bank', 'vipps']
export const BANK_ROW_FLAGS: BankRowFlag[] = ['duplicate', 'double_payment']

export function emptyBankRowFilter(): BankRowFilter {
  return { direction: null, source: null, flags: [], reference: false }
}

/** The comma-separated `kind` query value for a filter; '' means no filter. */
export function bankRowKindParam(f: BankRowFilter): string {
  if (f.reference) return f.referenceDismissed ? 'reference,dismissed' : 'reference'
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

// How many outgoing reference-year rows wait to be booked to a cost account;
// the Referanseår chip shows only while it is above zero.
export function useReferenceRowCount() {
  return useQuery({
    queryKey: ['bank-rows', 'unmatched', 'count', 'reference'],
    queryFn: async () => {
      const url = new URL(`${BASE}/unmatched/count`, window.location.origin)
      url.searchParams.set('kind', 'reference')
      const body = await useApi().fetchApi<{ count: number }>(url.toString())
      return body.count
    },
    refetchOnWindowFocus: true,
  })
}

// The years marked as reference years, from the shared periods query.
export function useReferenceYears() {
  const { data: periods } = useFiscalPeriods()
  return { data: computed(() => new Set((periods.value ?? []).filter((p) => p.reference_only).map((p) => p.year))) }
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

  return { assignInvoice, bilag, dismiss, clearPayout, invalidate }
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

// Faktura and account suggestions for one Vipps charge, ranked like a
// bank row's.
export function useVippsChargeSuggestions(chargeId: Ref<string | null>) {
  return useQuery({
    queryKey: computed(() => ['vipps-charges', 'suggestions', chargeId.value]),
    enabled: computed(() => !!chargeId.value),
    queryFn: async () => {
      return useApi().fetchApi<BankRowSuggestions>(
        `/api/v1/admin/accounting/vipps-charges/${chargeId.value}/suggestions`,
      )
    },
  })
}

export async function fetchBankRowSuggestions(rowId: string): Promise<BankRowSuggestions> {
  return useApi().fetchApi<BankRowSuggestions>(`${BASE}/${rowId}/suggestions`)
}

export async function fetchPotentialInvoices(rowId: string, q: string, amount?: number): Promise<InvoiceSuggestion[]> {
  const url = new URL(`${BASE}/${rowId}/potential-invoices`, window.location.origin)
  if (q) url.searchParams.set('q', q)
  if (amount != null && amount > 0) url.searchParams.set('amount', String(amount))
  const body = await useApi().fetchApi<{ items: InvoiceSuggestion[] }>(url.toString())
  return body.items
}

export async function fetchBankRowPartners(rowId: string): Promise<PartnerSuggestion[]> {
  const body = await useApi().fetchApi<{ items: PartnerSuggestion[] }>(`${BASE}/${rowId}/partners`)
  return body.items
}

/** True when assign-account refused a row because it has a strong partner. */
export function isHasPartnerError(e: unknown): boolean {
  return e instanceof ApiError && e.status === 409 && e.message === 'has_partner'
}

export function useReconcileMutations() {
  const qc = useQueryClient()
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['bank-rows'] })
  }

  const assignInvoice = useMutation({
    mutationFn: ({ rowId, invoiceId, alias }: { rowId: string; invoiceId: string; alias?: PayerAliasFields }) =>
      useApi().fetchApi(`${BASE}/${rowId}/assign-invoice`, {
        method: 'POST',
        body: JSON.stringify({ invoice_id: invoiceId, ...alias }),
      }),
    onSuccess: invalidate,
  })

  const assignAccount = useMutation({
    mutationFn: ({
      rowId,
      accountCode,
      kind,
      description,
      confirmAlone,
    }: {
      rowId: string
      accountCode: string
      kind: 'expense' | 'revenue'
      description?: string
      confirmAlone?: boolean
    }) =>
      useApi().fetchApi(`${BASE}/${rowId}/assign-account`, {
        method: 'POST',
        body: JSON.stringify({ account_code: accountCode, kind, description, confirm_alone: !!confirmAlone }),
      }),
    onSuccess: invalidate,
  })

  // Rows that belong together, booked as one bilag for their net.
  const assignAccountGroup = useMutation({
    mutationFn: ({ rowIds, accountCode, description }: { rowIds: string[]; accountCode: string; description?: string }) =>
      useApi().fetchApi(`${BASE}/assign-account-group`, {
        method: 'POST',
        body: JSON.stringify({ row_ids: rowIds, account_code: accountCode, description }),
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

  // Only a dismissed reference-year row can be undismissed; every other
  // dismissal is permanent.
  const undismiss = useMutation({
    mutationFn: ({ rowId }: { rowId: string }) =>
      useApi().fetchApi(`${BASE}/${rowId}/undismiss`, { method: 'POST' }),
    onSuccess: invalidate,
  })

  return { assignInvoice, assignAccount, assignAccountGroup, dismiss, unassign, undismiss, invalidate }
}

export function useAssignMultiInvoiceMutation() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ rowIds, invoiceId, alias }: { rowIds: string[]; invoiceId: string; alias?: PayerAliasFields }) =>
      useApi().fetchApi(`${BASE}/assign-invoice-multi`, {
        method: 'POST',
        body: JSON.stringify({ row_ids: rowIds, invoice_id: invoiceId, ...alias }),
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
