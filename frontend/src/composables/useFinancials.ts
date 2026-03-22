import { computed, type Ref } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import type { components } from '@/types/api'

export type FinancialSummary = components['schemas']['FinancialSummary']
export type Payment = components['schemas']['Payment']
export type CreateInvoiceRequest = components['schemas']['CreateInvoiceRequest']

export interface PaymentsFilters {
  type?: string
  status?: string
  year?: number
  page?: number
  per_page?: number
}

export interface ExportFilters {
  type?: string
  status?: string
  year?: number
  start?: string
  end?: string
}

export function useFinancialSummary(year?: Ref<number | undefined>) {
  const client = useApiClient()

  return useQuery({
    queryKey: computed(() => ['financials', 'summary', year?.value]),
    queryFn: async () => {
      const query = year?.value ? { year: year.value } : {}
      return unwrap(await client.GET('/api/v1/admin/financials/summary', {
        params: { query } as any,
      }))
    },
    staleTime: 2 * 60 * 1000,
  })
}

export function usePaymentsList(filters: Ref<PaymentsFilters>) {
  const client = useApiClient()

  return useQuery({
    queryKey: computed(() => ['financials', 'payments', filters.value]),
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/admin/financials/payments', {
        params: { query: filters.value } as any,
      })),
    staleTime: 60 * 1000,
  })
}

export function useOverduePayments() {
  const client = useApiClient()

  return useQuery({
    queryKey: ['financials', 'overdue'],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/admin/financials/overdue')),
    staleTime: 2 * 60 * 1000,
  })
}

export function useCreateInvoice() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (data: CreateInvoiceRequest) =>
      unwrap(await client.POST('/api/v1/admin/financials/invoices', { body: data as any })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['financials'] })
    },
  })
}

export function useExportCSV() {
  async function downloadCSV(filters: ExportFilters = {}) {
    const query = new URLSearchParams()
    for (const [key, value] of Object.entries(filters)) {
      if (value !== undefined && value !== '') {
        query.set(key, String(value))
      }
    }
    const qs = query.toString()
    const response = await fetch(`/api/v1/admin/financials/export${qs ? `?${qs}` : ''}`, {
      headers: {
        Authorization: `Bearer ${localStorage.getItem('access_token')}`,
      },
    })
    if (!response.ok) throw new Error('Export failed')
    const blob = await response.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'payments_export.csv'
    a.click()
    URL.revokeObjectURL(url)
  }

  return { downloadCSV }
}
