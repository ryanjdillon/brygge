import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// The API client answers synchronously-resolving promises; unwrap passes the
// data through.
const GET = vi.fn()
const POST = vi.fn()
const DELETE = vi.fn()
vi.mock('@/lib/apiClient', () => ({
  useApiClient: () => ({ GET, POST, DELETE }),
  unwrap: (r: { data: unknown }) => r.data,
}))

import { usePushSubscription, PushPermissionError, PushNotConfiguredError } from '@/composables/useNotifications'

// A valid base64url P-256 point is not needed: the fake pushManager ignores it.
const VAPID = 'BEl62iUYgUivxIkv69yViEuiBIa-Ib9-SkvMeAtA3LFgDzkrxZJjSgSnfckjBJuBkr3qBUYIHBQFLXYp5Nksh8U'

let events: string[]
let permissionAnswer: NotificationPermission
const pushSubscribe = vi.fn()

function installBrowser() {
  events = []
  const fakeSub = {
    endpoint: 'https://web.push.apple.com/QGtest',
    toJSON: () => ({ keys: { p256dh: 'p', auth: 'a' } }),
    unsubscribe: vi.fn(),
  }
  pushSubscribe.mockReset().mockImplementation(async () => {
    events.push('pushManager.subscribe')
    return fakeSub
  })
  const reg = {
    pushManager: {
      getSubscription: vi.fn(async () => null),
      subscribe: pushSubscribe,
    },
  }
  Object.defineProperty(navigator, 'serviceWorker', {
    configurable: true,
    value: {
      get ready() {
        events.push('serviceWorker.ready')
        return Promise.resolve(reg)
      },
    },
  })
  Object.defineProperty(window, 'PushManager', { configurable: true, value: function PushManager() {} })
  Object.defineProperty(window, 'Notification', {
    configurable: true,
    value: {
      permission: 'default',
      requestPermission: vi.fn(() => {
        events.push('requestPermission')
        return Promise.resolve(permissionAnswer)
      }),
    },
  })
}

beforeEach(() => {
  permissionAnswer = 'granted'
  installBrowser()
  GET.mockReset().mockImplementation(async () => {
    events.push('GET vapid-key')
    return { data: { vapid_key: VAPID } }
  })
  POST.mockReset().mockResolvedValue({ data: { status: 'subscribed' } })
  DELETE.mockReset().mockResolvedValue({ data: {} })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('usePushSubscription.subscribe (BRY-682)', () => {
  it('asks for permission first, synchronously inside the click', async () => {
    const push = usePushSubscription()
    await push.checkSubscription() // mount: fetches the key ahead
    events = []

    const pending = push.subscribe()
    // Nothing awaited yet: the permission prompt must already be requested.
    expect(events).toEqual(['requestPermission'])
    await pending

    expect(events).toEqual(['requestPermission', 'serviceWorker.ready', 'pushManager.subscribe'])
    expect(GET).toHaveBeenCalledTimes(1) // only the prefetch
    expect(POST).toHaveBeenCalledWith('/api/v1/push/subscribe', expect.anything())
    expect(push.isSubscribed.value).toBe(true)
  })

  it('fetches the key after the permission when it was not prefetched', async () => {
    const push = usePushSubscription()
    await push.subscribe()
    expect(events.slice(0, 2)).toEqual(['requestPermission', 'GET vapid-key'])
    expect(push.isSubscribed.value).toBe(true)
  })

  it('throws PushPermissionError when the user denies, without subscribing', async () => {
    permissionAnswer = 'denied'
    const push = usePushSubscription()
    await expect(push.subscribe()).rejects.toBeInstanceOf(PushPermissionError)
    expect(pushSubscribe).not.toHaveBeenCalled()
    expect(push.isSubscribed.value).toBe(false)
    expect(push.isLoading.value).toBe(false)
  })

  it('throws PushNotConfiguredError when the server has no key', async () => {
    GET.mockResolvedValue({ data: { vapid_key: '' } })
    const push = usePushSubscription()
    await expect(push.subscribe()).rejects.toBeInstanceOf(PushNotConfiguredError)
    expect(pushSubscribe).not.toHaveBeenCalled()
  })

  it('passes a pushManager failure to the caller', async () => {
    pushSubscribe.mockRejectedValueOnce(new Error('AbortError'))
    const push = usePushSubscription()
    await expect(push.subscribe()).rejects.toThrow('AbortError')
    expect(push.isSubscribed.value).toBe(false)
  })
})
