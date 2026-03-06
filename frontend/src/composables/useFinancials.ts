import { computed, type Ref } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'

export interface FinancialSummary {
  total_dues_received: number
  total_outstanding: number
  total_overdue: number
  total_andel_collected: number
  total_booking_revenue: number
  year?: number
}

export interface Payment {
  id: string
  user_id: string
  user_name: string
  user_email: string
  type: string
  amount: number
  currency: string
  status: string
  description: string
  due_date?: string
  paid_at?: string
  vipps_reference: string
  created_at: string
}

export interface PaymentsListResponse {
  payments: Payment[]
  total: number
  page: number
  per_page: number
}

export interface OverduePayment {
  id: string
  user_id: string
  user_name: string
  user_email: string
  user_phone: string
  type: string
  amount: number
  currency: string
  description: string
  due_date: string
  days_overdue: number
}

export interface CreateInvoiceRequest {
  user_id: string
  type: string
  amount: number
  description: string
  due_date: string
}

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

function buildQuery(params: Record<string, string | number | undefined>): string {
  const parts: string[] = []
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== '') {
      parts.push(`${encodeURIComponent(key)}=${encodeURIComponent(String(value))}`)
    }
  }
  return parts.length > 0 ? `?${parts.join('&')}` : ''
}

export function useFinancialSummary(year?: Ref<number | undefined>) {
  const { fetchApi } = useApi()

  return useQuery({
    queryKey: computed(() => ['financials', 'summary', year?.value]),
    queryFn: () => {
      const query = year?.value ? `?year=${year.value}` : ''
      return fetchApi<FinancialSummary>(`/api/v1/admin/financials/summary${query}`)
    },
    staleTime: 2 * 60 * 1000,
  })
}

export function usePaymentsList(filters: Ref<PaymentsFilters>) {
  const { fetchApi } = useApi()

  return useQuery({
    queryKey: computed(() => ['financials', 'payments', filters.value]),
    queryFn: () => {
      const query = buildQuery(filters.value as Record<string, string | number | undefined>)
      return fetchApi<PaymentsListResponse>(`/api/v1/admin/financials/payments${query}`)
    },
    staleTime: 60 * 1000,
  })
}

export function usePaymentDetails(paymentId: Ref<string | undefined>) {
  const { fetchApi } = useApi()

  return useQuery({
    queryKey: computed(() => ['financials', 'payment', paymentId.value]),
    queryFn: () => fetchApi<Payment>(`/api/v1/admin/financials/payments/${paymentId.value}`),
    enabled: computed(() => !!paymentId.value),
  })
}

export function useOverduePayments() {
  const { fetchApi } = useApi()

  return useQuery({
    queryKey: ['financials', 'overdue'],
    queryFn: () => fetchApi<OverduePayment[]>('/api/v1/admin/financials/overdue'),
    staleTime: 2 * 60 * 1000,
  })
}

export function useCreateInvoice() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (data: CreateInvoiceRequest) =>
      fetchApi<Payment>('/api/v1/admin/financials/invoices', {
        method: 'POST',
        body: JSON.stringify(data),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['financials'] })
    },
  })
}

export function useExportCSV() {
  async function downloadCSV(filters: ExportFilters = {}) {
    const query = buildQuery(filters as Record<string, string | number | undefined>)
    const response = await fetch(`/api/v1/admin/financials/export${query}`, {
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
