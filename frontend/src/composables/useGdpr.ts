import { computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import type { components } from '@/types/api'

export type DeletionRequest = components['schemas']['DeletionRequest']
export type Consent = components['schemas']['Consent']
export type LegalDocument = components['schemas']['LegalDocument']

export function useDeletionStatus() {
  const { fetchApi } = useApi()
  return useQuery({
    queryKey: ['deletion-status'],
    queryFn: () => fetchApi<DeletionRequest>('/api/v1/members/me/delete-request'),
    retry: false,
  })
}

export function useRequestDeletion() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => fetchApi('/api/v1/members/me/delete-request', { method: 'POST' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['deletion-status'] }),
  })
}

export function useCancelDeletion() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => fetchApi('/api/v1/members/me/delete-request', { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['deletion-status'] }),
  })
}

export function useDataExport() {
  const { fetchApi } = useApi()
  return useMutation({
    mutationFn: async () => {
      const data = await fetchApi<Record<string, unknown>>('/api/v1/members/me/data-export')
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = 'data-export.json'
      a.click()
      URL.revokeObjectURL(url)
    },
  })
}

export function useMyConsents() {
  const { fetchApi } = useApi()
  const query = useQuery({
    queryKey: ['my-consents'],
    queryFn: () => fetchApi<{ consents: Consent[] }>('/api/v1/members/me/consents'),
  })
  const consents = computed(() => query.data.value?.consents ?? [])
  return { ...query, consents }
}

export function useRecordConsent() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (payload: { consent_type: string; version: string }) =>
      fetchApi('/api/v1/members/me/consent', { method: 'POST', body: JSON.stringify(payload) }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['my-consents'] }),
  })
}

export function useLegalDocument(docType: string) {
  const { fetchApi } = useApi()
  return useQuery({
    queryKey: ['legal', docType],
    queryFn: () => fetchApi<LegalDocument>(`/api/v1/legal/${docType}`),
    retry: false,
  })
}

export function useAdminDeletionRequests() {
  const { fetchApi } = useApi()
  const query = useQuery({
    queryKey: ['admin', 'deletion-requests'],
    queryFn: () => fetchApi<{ requests: (DeletionRequest & { user_name: string; user_email: string })[] }>('/api/v1/admin/gdpr/deletion-requests'),
  })
  const requests = computed(() => query.data.value?.requests ?? [])
  return { ...query, requests }
}

export function useProcessDeletion() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      fetchApi(`/api/v1/admin/gdpr/deletion-requests/${id}/process`, { method: 'POST' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin', 'deletion-requests'] }),
  })
}

export function useAdminLegalDocuments() {
  const { fetchApi } = useApi()
  const query = useQuery({
    queryKey: ['admin', 'legal-documents'],
    queryFn: () => fetchApi<{ documents: LegalDocument[] }>('/api/v1/admin/gdpr/legal'),
  })
  const documents = computed(() => query.data.value?.documents ?? [])
  return { ...query, documents }
}

export function useCreateLegalDocument() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (payload: { doc_type: string; version: string; content: string; publish: boolean }) =>
      fetchApi('/api/v1/admin/gdpr/legal', { method: 'POST', body: JSON.stringify(payload) }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin', 'legal-documents'] }),
  })
}
