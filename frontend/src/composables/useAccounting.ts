import { computed, type Ref } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'

const BASE = '/api/v1/admin/accounting'

async function apiFetch<T>(path: string, opts?: RequestInit): Promise<T> {
  const res = await fetch(path, { credentials: 'include', ...opts })
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw new Error(body.detail ?? body.message ?? res.statusText)
  }
  return res.json()
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

export interface SyncResult {
  synced: number
  skipped: number
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

export function useJournalEntries(periodId: Ref<string>, status?: Ref<string>) {
  return useQuery({
    queryKey: ['accounting', 'journal', periodId, status],
    queryFn: () => {
      const params = new URLSearchParams()
      if (periodId.value) params.set('period_id', periodId.value)
      if (status?.value && status.value !== 'all') params.set('status', status.value)
      return apiFetch<JournalEntry[]>(`${BASE}/journal?${params}`)
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

export interface RebuildInvoiceBilagsResult {
  deleted: number
  resynced: number
  skipped: number
}

export function useRebuildInvoiceBilags() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { period_id: string }) =>
      apiFetch<RebuildInvoiceBilagsResult>(`${BASE}/sync/invoices/rebuild`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['accounting', 'journal'] })
    },
  })
}
