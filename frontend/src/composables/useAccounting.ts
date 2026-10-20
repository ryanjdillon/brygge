import { computed, type Ref } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import type { VippsPayoutSweepResult } from '@/composables/useBankImports'

const BASE = '/api/v1/admin/accounting'

// Delegate to the shared step-up-aware client so TOTP-expiry 403s drive
// the verify redirect / re-prompt instead of surfacing a bare 403.
async function apiFetch<T>(path: string, opts?: RequestInit): Promise<T> {
  return useApi().fetchApi<T>(path, opts)
}

export interface Account {
  id: string
  club_id: string
  code: string
  name: string
  account_type: 'asset' | 'liability' | 'revenue' | 'expense'
  parent_code: string
  is_system: boolean
  is_active: boolean
  mva_eligible: 'eligible' | 'ineligible' | 'partial' | 'not_applicable'
  description: string
  sort_order: number
  // What books to this account by default (list endpoint only): active
  // price items by name, and the price-item categories mapped to it.
  price_items?: AccountPriceItem[]
  price_categories?: string[]
}

export interface AccountPriceItem {
  id: string
  name: string
  category: string
}

export interface FiscalPeriod {
  id: string
  club_id: string
  year: number
  start_date: string
  end_date: string
  status: 'open' | 'closed'
  closed_by: string | null
  closed_at: string | null
  created_at: string
}

export interface JournalLine {
  id: string
  journal_entry_id: string
  account_id: string
  account_code: string
  account_name: string
  account_type: string
  debit: number
  credit: number
  description: string
  mva_amount: number
  mva_eligible: string
}

export interface JournalEntry {
  id: string
  club_id: string
  fiscal_period_id: string
  entry_number: number
  entry_date: string
  description: string
  status: 'draft' | 'posted' | 'voided'
  source: string
  source_id: string | null
  source_table: string | null
  attachment_url: string | null
  created_by: string
  posted_by: string | null
  posted_at: string | null
  voided_by: string | null
  voided_at: string | null
  created_at: string
  lines?: JournalLine[]
}

export interface SkippedItem {
  ref: string
  reason: string
}

export interface SyncResult {
  synced: number
  skipped: number
  reversed?: number
  skipped_details?: SkippedItem[]
}

export interface CreateJournalLineInput {
  account_code: string
  debit: number
  credit: number
  description: string
  mva_amount: number
}

export interface CreateJournalEntryInput {
  fiscal_period_id: string
  entry_date: string
  description: string
  lines: CreateJournalLineInput[]
}

export function useAccountsList() {
  return useQuery({
    queryKey: ['accounting', 'accounts'],
    queryFn: () => apiFetch<Account[]>(`${BASE}/accounts`),
  })
}

export function useSeedAccounts() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () =>
      apiFetch<{ seeded: number }>(`${BASE}/accounts/seed`, { method: 'POST' }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['accounting', 'accounts'] })
    },
  })
}

export function useCreateAccount() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: {
      code: string
      name: string
      account_type: string
      parent_code: string
      mva_eligible: string
      description: string
      sort_order: number
    }) =>
      apiFetch<{ id: string }>(`${BASE}/accounts`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['accounting', 'accounts'] })
    },
  })
}

export function useUpdateAccount() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string; name: string; description: string; mva_eligible: string }) =>
      apiFetch<{ message: string }>(`${BASE}/accounts/${id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['accounting', 'accounts'] })
    },
  })
}

export function useFiscalPeriods() {
  return useQuery({
    queryKey: ['accounting', 'periods'],
    queryFn: () => apiFetch<FiscalPeriod[]>(`${BASE}/periods`),
  })
}

export function useCreatePeriod() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { year: number }) =>
      apiFetch<FiscalPeriod>(`${BASE}/periods`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['accounting', 'periods'] })
    },
  })
}

export function useClosePeriod() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (periodId: string) =>
      apiFetch<{ message: string }>(`${BASE}/periods/${periodId}/close`, { method: 'POST' }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['accounting', 'periods'] })
    },
  })
}

export function useReopenPeriod() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (periodId: string) =>
      apiFetch<{ message: string }>(`${BASE}/periods/${periodId}/reopen`, { method: 'POST' }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['accounting', 'periods'] })
    },
  })
}

export interface JournalListParams {
  status?: Ref<string>
  source?: Ref<string>
  q?: Ref<string>
  sortBy?: Ref<string>
  sortDir?: Ref<string>
}

export function useJournalEntries(periodId: Ref<string>, listParams?: JournalListParams) {
  return useQuery({
    queryKey: computed(() => [
      'accounting', 'journal', periodId.value,
      listParams?.status?.value, listParams?.source?.value,
      listParams?.q?.value, listParams?.sortBy?.value, listParams?.sortDir?.value,
    ]),
    queryFn: () => {
      const p = new URLSearchParams()
      if (periodId.value) p.set('period_id', periodId.value)
      if (listParams?.status?.value && listParams.status.value !== 'all') p.set('status', listParams.status.value)
      if (listParams?.source?.value && listParams.source.value !== 'all') p.set('source', listParams.source.value)
      if (listParams?.q?.value) p.set('q', listParams.q.value)
      if (listParams?.sortBy?.value) p.set('sort_by', listParams.sortBy.value)
      if (listParams?.sortDir?.value) p.set('sort_dir', listParams.sortDir.value)
      return apiFetch<JournalEntry[]>(`${BASE}/journal?${p}`)
    },
    enabled: computed(() => !!periodId.value),
  })
}

export function useCreateJournalEntry() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateJournalEntryInput) =>
      apiFetch<JournalEntry>(`${BASE}/journal`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['accounting', 'journal'] })
    },
  })
}

export interface ReceiptData {
  total_amount: number
  net_amount: number
  mva_amount: number
  vendor: string
  date: string
  description: string
}

export function useParseReceipt() {
  return useMutation({
    mutationFn: (file: File) => {
      const form = new FormData()
      form.append('file', file)
      return apiFetch<ReceiptData>(`${BASE}/journal/parse-receipt`, {
        method: 'POST',
        body: form,
      })
    },
  })
}

export function useUploadJournalAttachment() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ entryId, file }: { entryId: string; file: File }) => {
      const form = new FormData()
      form.append('file', file)
      return apiFetch<{ attachment_url: string }>(`${BASE}/journal/${entryId}/attachment`, {
        method: 'POST',
        body: form,
      })
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['accounting', 'journal'] })
    },
  })
}

export function fetchJournalAttachmentUrl(entryId: string): Promise<string> {
  return apiFetch<{ url: string }>(`${BASE}/journal/${entryId}/attachment`).then(r => r.url)
}

export function useGetJournalEntry(entryId: Ref<string>) {
  return useQuery({
    queryKey: ['accounting', 'journal', 'detail', entryId],
    queryFn: () => apiFetch<JournalEntry>(`${BASE}/journal/${entryId.value}`),
    enabled: computed(() => !!entryId.value),
  })
}

export function usePostEntry() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (entryId: string) =>
      apiFetch<{ message: string }>(`${BASE}/journal/${entryId}/post`, { method: 'POST' }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['accounting', 'journal'] })
    },
  })
}

export function useVoidEntry() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (entryId: string) =>
      apiFetch<JournalEntry>(`${BASE}/journal/${entryId}/void`, { method: 'POST' }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['accounting', 'journal'] })
    },
  })
}

// Deleting is limited to drafts that no bank or Vipps row points at; the
// server answers 409 otherwise. Behind fresh TOTP, so call ensureFreshTotp()
// before mutating.
export function useDeleteDraftEntry() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (entryId: string) =>
      apiFetch<void>(`${BASE}/journal/${entryId}`, { method: 'DELETE' }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['accounting', 'journal'] })
    },
  })
}

export function useSyncPayments() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { period_id: string }) =>
      apiFetch<SyncResult>(`${BASE}/sync/payments`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['accounting', 'journal'] })
    },
  })
}

export function useSyncInvoices() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { period_id: string }) =>
      apiFetch<SyncResult>(`${BASE}/sync/invoices`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['accounting', 'journal'] })
    },
  })
}

export interface ResplitMove {
  account_code: string
  amount: number
}

export interface ResplitResult {
  invoices: { invoice_number: number; moves: ResplitMove[] }[]
  account_totals: ResplitMove[]
  posted: number
  skipped_details?: SkippedItem[] | null
}

// Preview is side-effect free; apply books correction entries and is
// behind fresh TOTP on the server, so call ensureFreshTotp() first.
export function previewResplitInvoiceBilags(periodId: string) {
  return apiFetch<ResplitResult>(`${BASE}/sync/invoices/resplit/preview`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ period_id: periodId }),
  })
}

export interface AccountTotal {
  account_code: string
  debit: number
  credit: number
}

export interface VippsBacklogResult {
  year: number
  applied: boolean
  auto_assign_enabled: boolean
  drafts: number
  posted: number
  posted_totals: AccountTotal[]
  reclassified: VippsBacklogReclass[]
  refused: SkippedItem[]
  // Refused charges parked on 1500 with no faktura that may be released to
  // the Vipps queue; each is also in refused.
  releasable: VippsBacklogRelease[]
  // Reclassifications left out of the apply's reclassify_entry_ids; their
  // drafts are untouched and offered again next time.
  unchecked: number
  // Sent ids that were no reclassification at apply time; skipped.
  stale_ids: string[]
  payouts?: VippsPayoutSweepResult | null
}

export interface VippsBacklogReclass {
  entry_id: string
  row_id: string
  entry_number: number
  date: string
  amount: number
  from: string
  to: string
  rule: string
}

export interface VippsBacklogRelease {
  ref: string
  row_id: string
  entry_number: number
  date: string
  amount: number
}

// Releases a refused backlog draft through the Vipps unassign; the flag
// makes the server re-check that the charge paid no faktura. Behind fresh
// TOTP on the server, so call ensureFreshTotp() first.
export function useReleaseVippsBacklogCharge() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (rowId: string) =>
      apiFetch<{ status: string }>(`${BASE}/vipps-charges/${rowId}/unassign`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ released_from_backlog: true }),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['accounting', 'journal'] })
    },
  })
}

// Preview is side-effect free; apply posts the period's Vipps drafts and is
// behind fresh TOTP on the server, so call ensureFreshTotp() first.
export function previewVippsDraftBacklog(periodId: string) {
  return apiFetch<VippsBacklogResult>(`${BASE}/sync/vipps-drafts/preview`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ period_id: periodId }),
  })
}

export function useApplyVippsDraftBacklog() {
  const qc = useQueryClient()
  return useMutation({
    // reclassify_entry_ids absent rebooks every reclassification; present,
    // only those listed whose target still equals reclassify_targets[id],
    // leaving the other drafts untouched.
    mutationFn: (body: {
      period_id: string
      reclassify_entry_ids?: string[]
      reclassify_targets?: Record<string, string>
    }) =>
      apiFetch<VippsBacklogResult>(`${BASE}/sync/vipps-drafts`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['accounting', 'journal'] })
    },
  })
}

export function useApplyResplitInvoiceBilags() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { period_id: string }) =>
      apiFetch<ResplitResult>(`${BASE}/sync/invoices/resplit`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['accounting', 'journal'] })
    },
  })
}
