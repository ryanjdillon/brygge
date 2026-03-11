import { computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import type { components } from '@/types/api'

export type DeletionRequest = components['schemas']['DeletionRequest']
export type Consent = components['schemas']['Consent']
export type LegalDocument = components['schemas']['LegalDocument']

export function useDeletionStatus() {
  const client = useApiClient()
  return useQuery({
    queryKey: ['deletion-status'],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/members/me/delete-request')),
    retry: false,
  })
}

export function useRequestDeletion() {
  const client = useApiClient()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async () =>
      unwrap(await client.POST('/api/v1/members/me/delete-request')),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['deletion-status'] }),
  })
}

export function useCancelDeletion() {
  const client = useApiClient()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async () =>
      unwrap(await client.DELETE('/api/v1/members/me/delete-request')),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['deletion-status'] }),
  })
}

export function useDataExport() {
  const client = useApiClient()
  return useMutation({
    mutationFn: async () => {
      const data = unwrap(await client.GET('/api/v1/members/me/data-export'))
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
  const client = useApiClient()
  const query = useQuery({
    queryKey: ['my-consents'],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/members/me/consents')),
  })
  const consents = computed(() => query.data.value?.consents ?? [])
  return { ...query, consents }
}

export function useRecordConsent() {
  const client = useApiClient()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (payload: { consent_type: string; version: string }) =>
      unwrap(await client.POST('/api/v1/members/me/consent', { body: payload as any })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['my-consents'] }),
  })
}

export function useLegalDocument(docType: 'terms' | 'privacy') {
  const client = useApiClient()
  return useQuery({
    queryKey: ['legal', docType],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/legal/{docType}', {
        params: { path: { docType } },
      })),
    retry: false,
  })
}

export function useAdminDeletionRequests() {
  const client = useApiClient()
  const query = useQuery({
    queryKey: ['admin', 'deletion-requests'],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/admin/gdpr/deletion-requests')),
  })
  const requests = computed(() => query.data.value?.requests ?? [])
  return { ...query, requests }
}

export function useProcessDeletion() {
  const client = useApiClient()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) =>
      unwrap(await client.POST('/api/v1/admin/gdpr/deletion-requests/{requestID}/process', {
        params: { path: { requestID: id } },
      })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin', 'deletion-requests'] }),
  })
}

export function useAdminLegalDocuments() {
  const client = useApiClient()
  const query = useQuery({
    queryKey: ['admin', 'legal-documents'],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/admin/gdpr/legal')),
  })
  const documents = computed(() => query.data.value?.documents ?? [])
  return { ...query, documents }
}

export function useCreateLegalDocument() {
  const client = useApiClient()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (payload: { doc_type: string; version: string; content: string; publish: boolean }) =>
      unwrap(await client.POST('/api/v1/admin/gdpr/legal', { body: payload as any })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin', 'legal-documents'] }),
  })
}
