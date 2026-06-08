import { computed, type Ref } from 'vue'
import { useQuery } from '@tanstack/vue-query'

const BASE = '/api/v1/admin/accounting'

async function fetchJSON<T>(path: string, opts?: RequestInit): Promise<T> {
  const res = await fetch(path, { credentials: 'include', ...opts })
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw new Error(body.error ?? body.detail ?? res.statusText)
  }
  return res.json()
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

export interface VippsImportResult {
  id: string
  filename: string
  msn: string
  rows_total: number
  imported: number
  skipped_dup: number
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
}

export async function runBankSync(): Promise<BankSyncResult> {
  return fetchJSON<BankSyncResult>(`${BASE}/bank-sync`, { method: 'POST' })
}
