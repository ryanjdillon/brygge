import { computed, type Ref } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'

const BASE = '/api/v1/admin/accounting'

// Delegate to the shared step-up-aware client so an expired admin-TOTP
// window (403 totp_required) redirects to the verify page and a stale
// fresh-TOTP window (403 totp_fresh_required) re-prompts and retries,
// instead of surfacing a bare 403.
async function fetchJSON<T>(path: string, opts?: RequestInit): Promise<T> {
  return useApi().fetchApi<T>(path, opts)
}

export interface BankFormat {
  code: string
}

export interface BankImportRow {
  id: string
  date: string
  description: string
  amount: number
  balance: number | null
  reference: string
  kid_number: string
  counterpart: string
  journal_entry_id: string | null
  auto_matched: boolean
}

export interface BankImportResult {
  id: string
  filename: string
  format: string
  rows_total: number
  imported: number
  skipped_dup: number
  matched: number
  transfers: number
  closed_periods?: string[]
  bank_account_code: string
  auto_matched: boolean
  // Present only on a full-account overwrite reconcile.
  deleted?: number
  skipped_booked?: number
  // Rows whose auto-match failed; they stay unmatched for manual Tildel.
  row_errors?: BankImportRowError[] | null
  // Of row_errors, the KID matches whose faktura was already settled.
  kid_skipped_settled?: number
  // In-flight rows from a statement exported while a transfer was under way
  // (BRY-378): refreshed in place with the settled text, or dismissed as
  // superseded; ambiguous ones are left for Tildel.
  refreshed?: number
  // Provisional rows whose settled row carried the same reference but
  // another amount or a date more than two days later; left for Tildel.
  reference_mismatches?: ReferenceMismatch[] | null
  superseded?: number
  provisional_ambiguous?: ProvisionalBankRow[] | null
  // Latest row date in the statement, and the provisional rows it still
  // holds on that day.
  last_row_date?: string
  // KID-less deposits booked by bank auto-assign; absent when it is off.
  auto_assign?: AutoAssignResult | null
  provisional_rows?: number
  // Stripe charges and fees booked after the import; absent when none waited.
  stripe_booking?: StripeBookResult | null
  // The Stripe payout sweep after that booking; absent when there was nothing to look at.
  stripe_payouts?: StripePayoutSweepResult | null
}

export interface ReferenceMismatch {
  row_id: string
  reference: string
  date: string
  amount: number
  new_date: string
  new_amount: number
}

export interface ProvisionalBankRow {
  row_id: string
  date: string
  amount: number
  candidates: number
}

export interface BankImportRowError {
  row_id: string
  date: string
  amount: number
  reason: string
}

export interface VippsImportSummary {
  id: string
  filename: string
  msn: string
  row_count: number
  created_at: string
}

export interface VippsImportRow {
  id: string
  row_type: 'belastning' | 'fee' | 'payout' | 'other'
  tx_at: string | null
  booking_date: string | null
  amount: number
  fee: number
  net_amount: number
  customer_name: string
  customer_phone_masked: string
  message: string
  psp_ref: string
  order_id: string
  settlement_number: string
  payout_account: string
  scheduled_payout_date: string | null
  journal_entry_id: string | null
}

export interface AutoAssignItem {
  source?: 'vipps' | 'bank'
  row_id: string
  date: string
  amount: number
  rule: 'member_invoice' | 'price_item'
  target: string
  entry_id: string
  // Bank deposits only: the faktura and how the payer was recognised.
  invoice_number?: number
  match_pass?: string
}

// Payments booked without review after an import or sync: Vipps charges
// (BRY-340) or KID-less bank deposits (BRY-366).
export interface AutoAssignResult {
  source?: 'vipps' | 'bank'
  enabled: boolean
  assigned: AutoAssignItem[]
  needs_review: number
  // Bank only: deposits left in Tildel, counted by reason.
  by_reason?: Record<string, number>
  // Set when the run itself failed; what it booked first is still listed.
  error?: string
}

export interface VippsPayoutSweepItem {
  settlement_number: string
  msn: string
  bank_row_id?: string
  reason?: string
}

// What a sync cleared on its own once a settlement's charges were all booked
// (BRY-340); waiting/skipped/failed carry the reason a payout stays open.
export interface VippsPayoutSweepResult {
  cleared: VippsPayoutSweepItem[]
  waiting: VippsPayoutSweepItem[]
  skipped: VippsPayoutSweepItem[]
  failed: VippsPayoutSweepItem[]
  // Set when the sweep itself could not run; the lists hold what it got through.
  error?: string
}

export interface VippsImportResult {
  id: string
  filename: string
  msn: string
  rows_total: number
  imported: number
  skipped_dup: number
  auto_assign?: AutoAssignResult | null
  payouts?: VippsPayoutSweepResult | null
}

export interface VippsReconcileLine {
  vipps_row_id?: string
  kind: 'bank_in' | 'receivable' | 'revenue' | 'fee' | 'clearing'
  account_code: string
  debit: number
  credit: number
  description: string
  customer_name?: string
  resolved_member_id?: string
  resolved: boolean
}

export interface VippsReconcilePreview {
  bank_row_id: string
  bank_amount: number
  bank_date: string
  settlement_number: string
  msn: string
  payout_amount: number
  total_charges: number
  total_fees: number
  unresolved_count: number
  balanced: boolean
  reason?: string
  period_year: number
  period_closed: boolean
  lines: VippsReconcileLine[]
}

export function useBankFormats() {
  return useQuery({
    queryKey: ['accounting', 'bank-formats'],
    queryFn: () => fetchJSON<string[]>(`${BASE}/bank-formats`),
    staleTime: 60 * 60 * 1000,
  })
}

export function useBankImport(importID: Ref<string | undefined>) {
  return useQuery({
    queryKey: computed(() => ['accounting', 'bank-import', importID.value]),
    enabled: computed(() => !!importID.value),
    queryFn: () => fetchJSON<BankImportRow[]>(`${BASE}/bank-import/${importID.value}`),
  })
}

export interface BankImportSummary {
  id: string
  filename: string
  format: string
  account_code: string
  row_count: number
  matched_count: number
  created_at: string
}

export function useBankImportsList() {
  return useQuery({
    queryKey: ['accounting', 'bank-imports'],
    queryFn: () => fetchJSON<BankImportSummary[]>(`${BASE}/bank-imports`),
  })
}

export function useBankRowsByAccount(
  accountCode: Ref<string | undefined>,
  from: Ref<string | undefined>,
  to: Ref<string | undefined>,
) {
  return useQuery({
    queryKey: computed(() => ['accounting', 'bank-rows', accountCode.value, from.value, to.value]),
    enabled: computed(() => !!accountCode.value && !!from.value && !!to.value),
    queryFn: () => {
      const params = new URLSearchParams({
        account_code: accountCode.value!,
        from: from.value!,
        to: to.value!,
      })
      return fetchJSON<BankImportRow[]>(`${BASE}/bank-rows?${params.toString()}`)
    },
  })
}

export function useVippsImports() {
  return useQuery({
    queryKey: ['accounting', 'vipps-imports'],
    queryFn: () => fetchJSON<VippsImportSummary[]>(`${BASE}/vipps-imports`),
  })
}

export function useVippsRowsByMSN(
  msn: Ref<string | undefined>,
  from: Ref<string | undefined>,
  to: Ref<string | undefined>,
) {
  return useQuery({
    queryKey: computed(() => ['accounting', 'vipps-rows', msn.value, from.value, to.value]),
    enabled: computed(() => !!msn.value && !!from.value && !!to.value),
    queryFn: () => {
      const params = new URLSearchParams({
        msn: msn.value!,
        from: from.value!,
        to: to.value!,
      })
      return fetchJSON<VippsImportRow[]>(`${BASE}/vipps-rows?${params.toString()}`)
    },
  })
}

export function useVippsImport(importID: Ref<string | undefined>) {
  return useQuery({
    queryKey: computed(() => ['accounting', 'vipps-import', importID.value]),
    enabled: computed(() => !!importID.value),
    queryFn: () => fetchJSON<VippsImportRow[]>(`${BASE}/vipps-imports/${importID.value}`),
  })
}

export async function uploadBankCSV(
  file: File,
  format: string,
  bankAccountCode: string,
): Promise<BankImportResult> {
  const fd = new FormData()
  fd.append('file', file)
  fd.append('format', format)
  fd.append('bank_account_code', bankAccountCode)
  return fetchJSON<BankImportResult>(`${BASE}/bank-import/`, { method: 'POST', body: fd })
}

// reimportBankCSV re-parses a corrected CSV for an existing import, replacing
// only its unmatched rows (matched rows with a booked bilag are never touched).
// Recovers rows that an older format manifest mis-parsed.
export async function reimportBankCSV(
  importID: string,
  file: File,
  format?: string,
): Promise<{
  id: string
  deleted: number
  reimported: number
  skipped_dup: number
  skipped_booked: number
  matched: number
}> {
  const fd = new FormData()
  fd.append('file', file)
  if (format) fd.append('format', format)
  return fetchJSON(`${BASE}/bank-import/${importID}/reimport`, { method: 'POST', body: fd })
}

// reimportAccountBankCSV does a full-account reconcile: it clears the account's
// unmatched rows within the CSV's date range, then imports + auto-matches the
// whole file. Booked rows are never touched. Fresh-TOTP gated on the server.
export async function reimportAccountBankCSV(
  file: File,
  format: string,
  bankAccountCode: string,
): Promise<BankImportResult> {
  const fd = new FormData()
  fd.append('file', file)
  fd.append('format', format)
  fd.append('bank_account_code', bankAccountCode)
  return fetchJSON<BankImportResult>(`${BASE}/bank-import/account-reimport`, {
    method: 'POST',
    body: fd,
  })
}

export async function reassignBankImport(
  importID: string,
  bankAccountCode: string,
): Promise<{ status: string; from?: string; to?: string }> {
  return fetchJSON(`${BASE}/bank-import/${importID}/account`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ bank_account_code: bankAccountCode }),
  })
}

export async function uploadVippsCSV(file: File): Promise<VippsImportResult> {
  const fd = new FormData()
  fd.append('file', file)
  return fetchJSON<VippsImportResult>(`${BASE}/vipps-imports/`, { method: 'POST', body: fd })
}

export async function previewVippsReconcile(rowID: string): Promise<VippsReconcilePreview> {
  return fetchJSON<VippsReconcilePreview>(`${BASE}/bank-rows/${rowID}/reconcile-vipps/`)
}

export async function confirmVippsReconcile(
  rowID: string,
  lines: VippsReconcileLine[],
): Promise<{ journal_entry_id: string }> {
  return fetchJSON(`${BASE}/bank-rows/${rowID}/reconcile-vipps/confirm`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ lines }),
  })
}

export interface BankSyncResult {
  kid_matched: number
  // KID-cascade rows whose faktura was already settled once locked; left in Tildel.
  kid_skipped_settled?: number
  vipps_reconciled: number
  vipps_unbalanced: number
  transfers_linked: number
  closed_periods: string[]
  vipps_payouts?: VippsPayoutSweepResult | null
  provisional_superseded?: number
  provisional_ambiguous?: ProvisionalBankRow[] | null
  // KID-less deposits booked by bank auto-assign; absent when it is off.
  auto_assign?: AutoAssignResult | null
  // Stripe charges and fees booked by the last pass; absent when none waited.
  stripe_booking?: StripeBookResult | null
  // The Stripe payout sweep after that pass; absent when there was nothing to look at.
  stripe_payouts?: StripePayoutSweepResult | null
}

export async function runBankSync(): Promise<BankSyncResult> {
  return fetchJSON<BankSyncResult>(`${BASE}/bank-sync`, { method: 'POST' })
}

export interface VippsResyncResult {
  scanned: number
  resynced: number
  skipped: number
  failed: string[]
  auto_assign?: AutoAssignResult | null
  payouts?: VippsPayoutSweepResult | null
}

export async function runVippsResync(): Promise<VippsResyncResult> {
  return fetchJSON<VippsResyncResult>(`${BASE}/vipps-resync`, { method: 'POST' })
}

// One booking pass over the Stripe balance (accounting.StripeBookResult).
export interface StripeBookResult {
  charges_booked: number
  fees_booked: number
  // Refunds and refund failures booked this pass.
  refunds_booked?: number
  // Rows held for review this pass, by review reason.
  needs_review: Record<string, number>
  // Rows left unbooked for now (closed fiscal period); retried next pass.
  deferred: { balance_transaction_id: string; date: string; reason: string }[]
  errors?: string[] | null
  // Another Stripe sync was running, so nothing was booked.
  busy?: boolean
  // The pass itself failed; server-provided text.
  error?: string
}

// One Stripe payout (or a Stripe deposit no payout matches, without
// payout_id) the payout sweep looked at.
export interface StripePayoutSweepItem {
  payout_id?: string
  amount: number
  arrival_date?: string
  bank_row_id?: string
  bank_date?: string
  journal_entry_id?: string
  // Server-provided text: what it waits for, or why it is left to a person.
  reason?: string
}

// What the Stripe payout sweep cleared, and why the rest stays open.
export interface StripePayoutSweepResult {
  cleared: StripePayoutSweepItem[]
  waiting: StripePayoutSweepItem[]
  skipped: StripePayoutSweepItem[]
  failed: StripePayoutSweepItem[]
  error?: string
}

export interface StripeSyncResult {
  fetched: number
  inserted: number
  needs_review: Record<string, number>
  payouts_seen: number
  payouts_linked: number
  members_linked: number
  errors?: string[] | null
  booking: StripeBookResult | null
  payouts: StripePayoutSweepResult | null
}

// Ingests the Stripe balance, books what is ready and clears the payouts
// that are ready. Behind fresh TOTP.
export async function runStripeSync(): Promise<StripeSyncResult> {
  return fetchJSON<StripeSyncResult>(`${BASE}/stripe/sync`, { method: 'POST' })
}

// Whether this instance has Stripe credentials, so the Stripe sync is offered.
export function useStripeConfigured() {
  return useQuery({
    queryKey: ['accounting', 'stripe-configured'],
    queryFn: async () =>
      !!(await fetchJSON<{ stripe_configured?: boolean }>('/api/v1/admin/settings/site')).stripe_configured,
    staleTime: 300_000,
  })
}

// Undoes a charge booking so it returns to Tildel. Behind fresh TOTP.
export async function unassignVippsCharge(rowID: string): Promise<{ status: string }> {
  return fetchJSON(`${BASE}/vipps-charges/${rowID}/unassign`, { method: 'POST' })
}

// Undoes a bank row booking so it returns to Tildel. Behind fresh TOTP.
export async function unassignBankRow(rowID: string): Promise<{ status: string }> {
  return fetchJSON(`${BASE}/bank-rows/${rowID}/unassign`, { method: 'POST' })
}
