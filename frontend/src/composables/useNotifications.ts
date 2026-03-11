import { ref, computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'

export interface NotificationPreference {
  category: string
  enabled: boolean
  required: boolean
  default_enabled: boolean
}

export interface NotificationConfig {
  category: string
  required: boolean
  lead_days: number | null
}

export function useNotificationPreferences() {
  const client = useApiClient()

  const query = useQuery({
    queryKey: ['notification-preferences'],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/members/me/notifications')),
  })

  const categories = computed(() => query.data.value?.categories ?? [])

  return { ...query, categories }
}

export function useUpdatePreference() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (payload: { category: string; enabled: boolean }) =>
      unwrap(await client.PUT('/api/v1/members/me/notifications', { body: payload as any })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['notification-preferences'] }),
  })
}

export function useNotificationConfig() {
  const client = useApiClient()

  const query = useQuery({
    queryKey: ['admin', 'notification-config'],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/admin/notifications/config')),
  })

  const categories = computed(() => query.data.value?.categories ?? [])

  return { ...query, categories }
}

export function useUpdateNotificationConfig() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (payload: { category: string; required: boolean; lead_days?: number }) =>
      unwrap(await client.PUT('/api/v1/admin/notifications/config', { body: payload as any })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin', 'notification-config'] }),
  })
}

export function useTestPush() {
  const client = useApiClient()

  return useMutation({
    mutationFn: async () =>
      unwrap(await client.POST('/api/v1/admin/notifications/test')),
  })
}

export function usePushSubscription() {
  const client = useApiClient()
  const isSupported = ref('serviceWorker' in navigator && 'PushManager' in window)
  const isSubscribed = ref(false)
  const isLoading = ref(false)

  async function checkSubscription() {
    if (!isSupported.value) return
    try {
      const reg = await navigator.serviceWorker.ready
      const sub = await reg.pushManager.getSubscription()
      isSubscribed.value = !!sub
    } catch {
      isSubscribed.value = false
    }
  }

  async function subscribe() {
    if (!isSupported.value) return
    isLoading.value = true
    try {
      const { vapid_key } = unwrap(await client.GET('/api/v1/push/vapid-key'))
      if (!vapid_key) throw new Error('VAPID key not configured')

      const reg = await navigator.serviceWorker.ready
      const sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(vapid_key) as BufferSource,
      })

      const json = sub.toJSON()
      await unwrap(await client.POST('/api/v1/push/subscribe', {
        body: {
          endpoint: sub.endpoint,
          keys: {
            p256dh: json.keys?.p256dh ?? '',
            auth: json.keys?.auth ?? '',
          },
        } as any,
      }))

      isSubscribed.value = true
    } finally {
      isLoading.value = false
    }
  }

  async function unsubscribe() {
    if (!isSupported.value) return
    isLoading.value = true
    try {
      const reg = await navigator.serviceWorker.ready
      const sub = await reg.pushManager.getSubscription()
      if (sub) {
        await unwrap(await client.DELETE('/api/v1/push/subscribe', {
          body: { endpoint: sub.endpoint } as any,
        }))
        await sub.unsubscribe()
      }
      isSubscribed.value = false
    } finally {
      isLoading.value = false
    }
  }

  return { isSupported, isSubscribed, isLoading, checkSubscription, subscribe, unsubscribe }
}

function urlBase64ToUint8Array(base64String: string): Uint8Array {
  const padding = '='.repeat((4 - (base64String.length % 4)) % 4)
  const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/')
  const raw = atob(base64)
  const arr = new Uint8Array(raw.length)
  for (let i = 0; i < raw.length; i++) arr[i] = raw.charCodeAt(i)
  return arr
}
