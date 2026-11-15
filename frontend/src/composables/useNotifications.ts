import { ref, computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import type { components } from '@/types/api'

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

export type TestPushResult = components['schemas']['TestPushResult']

/**
 * Sends a test push to the signed-in user's own devices (BRY-682). Errors
 * carry `code` `no_push_subscription` (409) or `push_not_configured` (503).
 */
export function useTestPush() {
  const client = useApiClient()

  return useMutation({
    mutationFn: async (): Promise<TestPushResult> =>
      unwrap(await client.POST('/api/v1/notifications/test')),
  })
}

/** Thrown by subscribe() when the browser or the user refuses notifications. */
export class PushPermissionError extends Error {
  constructor(public permission: NotificationPermission) {
    super(`notification permission ${permission}`)
    this.name = 'PushPermissionError'
  }
}

/** Thrown by subscribe() when the server has no VAPID key. */
export class PushNotConfiguredError extends Error {
  constructor() {
    super('VAPID key not configured')
    this.name = 'PushNotConfiguredError'
  }
}

export function usePushSubscription() {
  const client = useApiClient()
  const isSupported = ref('serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window)
  const isSubscribed = ref(false)
  const isLoading = ref(false)
  // Fetched ahead (checkSubscription, on mount) so the click handler does
  // not have to await the network before asking for permission.
  let vapidKey: string | null = null

  async function loadVapidKey(): Promise<string | null> {
    if (vapidKey) return vapidKey
    try {
      const { vapid_key } = unwrap(await client.GET('/api/v1/push/vapid-key'))
      vapidKey = vapid_key || null
    } catch {
      vapidKey = null
    }
    return vapidKey
  }

  async function checkSubscription() {
    if (!isSupported.value) return
    void loadVapidKey()
    try {
      const reg = await navigator.serviceWorker.ready
      const sub = await reg.pushManager.getSubscription()
      isSubscribed.value = !!sub
    } catch {
      isSubscribed.value = false
    }
  }

  /**
   * Must be called straight from the click handler. iOS only shows the
   * permission prompt inside the user gesture, so Notification.requestPermission()
   * is the first call, with no await before it; the subscription follows.
   */
  async function subscribe() {
    if (!isSupported.value) return
    const permission = Notification.requestPermission()
    isLoading.value = true
    try {
      const granted = await permission
      if (granted !== 'granted') throw new PushPermissionError(granted)

      const key = vapidKey ?? (await loadVapidKey())
      if (!key) throw new PushNotConfiguredError()

      const reg = await navigator.serviceWorker.ready
      const sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(key) as BufferSource,
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
