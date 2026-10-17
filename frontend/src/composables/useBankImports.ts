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

export interface VippsAutoAssignItem {
  row_id: string
  date: string
  amount: number
  rule: 'member_invoice' | 'price_item'
  target: string
  entry_id: string
}

// Charges booked without review after an import or Synk Vipps (BRY-340).
export interface VippsAutoAssignResult {
  enabled: boolean
  assigned: VippsAutoAssignItem[]
  needs_review: number
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
}

export interface VippsImportResult {
  id: string
  filename: string
  msn: string
  rows_total: number
  imported: number
  skipped_dup: number
  auto_assign?: VippsAutoAssignResult | null
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
  vipps_reconciled: number
  vipps_unbalanced: number
  transfers_linked: number
  closed_periods: string[]
  vipps_payouts?: VippsPayoutSweepResult | null
}

export async function runBankSync(): Promise<BankSyncResult> {
  return fetchJSON<BankSyncResult>(`${BASE}/bank-sync`, { method: 'POST' })
}

export interface VippsResyncResult {
  scanned: number
  resynced: number
  skipped: number
  failed: string[]
  auto_assign?: VippsAutoAssignResult | null
  payouts?: VippsPayoutSweepResult | null
}

export async function runVippsResync(): Promise<VippsResyncResult> {
  return fetchJSON<VippsResyncResult>(`${BASE}/vipps-resync`, { method: 'POST' })
}

// Undoes a charge booking so it returns to Tildel. Behind fresh TOTP.
export async function unassignVippsCharge(rowID: string): Promise<{ status: string }> {
  return fetchJSON(`${BASE}/vipps-charges/${rowID}/unassign`, { method: 'POST' })
}
