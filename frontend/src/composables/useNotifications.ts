import { ref, computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'

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
  const { fetchApi } = useApi()

  const query = useQuery({
    queryKey: ['notification-preferences'],
    queryFn: () => fetchApi<{ categories: NotificationPreference[] }>('/api/v1/members/me/notifications'),
  })

  const categories = computed(() => query.data.value?.categories ?? [])

  return { ...query, categories }
}

export function useUpdatePreference() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (payload: { category: string; enabled: boolean }) =>
      fetchApi('/api/v1/members/me/notifications', {
        method: 'PUT',
        body: JSON.stringify(payload),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['notification-preferences'] }),
  })
}

export function useNotificationConfig() {
  const { fetchApi } = useApi()

  const query = useQuery({
    queryKey: ['admin', 'notification-config'],
    queryFn: () => fetchApi<{ categories: NotificationConfig[] }>('/api/v1/admin/notifications/config'),
  })

  const categories = computed(() => query.data.value?.categories ?? [])

  return { ...query, categories }
}

export function useUpdateNotificationConfig() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (payload: { category: string; required: boolean; lead_days?: number }) =>
      fetchApi('/api/v1/admin/notifications/config', {
        method: 'PUT',
        body: JSON.stringify(payload),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin', 'notification-config'] }),
  })
}

export function useTestPush() {
  const { fetchApi } = useApi()

  return useMutation({
    mutationFn: () => fetchApi('/api/v1/admin/notifications/test', { method: 'POST' }),
  })
}

export function usePushSubscription() {
  const { fetchApi } = useApi()
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
      const { public_key } = await fetchApi<{ public_key: string }>('/api/v1/push/vapid-key')
      if (!public_key) throw new Error('VAPID key not configured')

      const reg = await navigator.serviceWorker.ready
      const sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(public_key) as BufferSource,
      })

      const json = sub.toJSON()
      await fetchApi('/api/v1/push/subscribe', {
        method: 'POST',
        body: JSON.stringify({
          endpoint: sub.endpoint,
          keys: {
            p256dh: json.keys?.p256dh ?? '',
            auth: json.keys?.auth ?? '',
          },
        }),
      })

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
        await fetchApi('/api/v1/push/subscribe', {
          method: 'DELETE',
          body: JSON.stringify({ endpoint: sub.endpoint }),
        })
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
