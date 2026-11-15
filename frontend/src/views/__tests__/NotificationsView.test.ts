import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import { ApiError } from '@/lib/errors'
import NotificationsView from '@/views/portal/NotificationsView.vue'
import { PushPermissionError, PushNotConfiguredError } from '@/composables/useNotifications'

const isSupported = ref(true)
const isSubscribed = ref(false)
const sendTestMutate = vi.fn()
const subscribeMock = vi.fn()
const unsubscribeMock = vi.fn()
const platform = { appleMobile: false, standalone: false }

vi.mock('lucide-vue-next', () => ({
  Bell: { template: '<span data-icon="bell" />' },
  BellOff: { template: '<span data-icon="bell-off" />' },
  Send: { template: '<span data-icon="send" />' },
  Smartphone: { template: '<span data-icon="smartphone" />' },
}))

vi.mock('@/composables/useNotifications', () => ({
  useNotificationPreferences: () => ({
    categories: ref([
      { category: 'payment_reminder', enabled: true, required: true, default: true },
      { category: 'slip_offer', enabled: true, required: false, default: true },
      { category: 'booking_confirm', enabled: false, required: false, default: true },
      { category: 'new_document', enabled: false, required: false, default: false },
    ]),
    isLoading: ref(false),
  }),
  useUpdatePreference: () => ({
    mutate: vi.fn(),
  }),
  usePushSubscription: () => ({
    isSupported,
    isSubscribed,
    isLoading: ref(false),
    checkSubscription: vi.fn(),
    subscribe: subscribeMock,
    unsubscribe: unsubscribeMock,
  }),
  useTestPush: () => ({ mutate: sendTestMutate, isPending: ref(false) }),
  PushPermissionError: class PushPermissionError extends Error {},
  PushNotConfiguredError: class PushNotConfiguredError extends Error {},
}))

vi.mock('@/lib/pwa', () => ({
  isAppleMobile: () => platform.appleMobile,
  isStandaloneDisplay: () => platform.standalone,
}))

type MutateOpts = { onSuccess?: (r: unknown) => void; onError?: (e: unknown) => void }

beforeEach(() => {
  isSupported.value = true
  isSubscribed.value = false
  platform.appleMobile = false
  platform.standalone = false
  sendTestMutate.mockReset()
  subscribeMock.mockReset().mockResolvedValue(undefined)
  unsubscribeMock.mockReset().mockResolvedValue(undefined)
})

describe('NotificationsView', () => {
  it('renders without errors', () => {
    const wrapper = mountWithPlugins(NotificationsView)
    expect(wrapper.exists()).toBe(true)
  })

  it('renders notification title', () => {
    const wrapper = mountWithPlugins(NotificationsView)
    expect(wrapper.find('h2').text()).toBeTruthy()
  })

  it('renders push toggle button', () => {
    const wrapper = mountWithPlugins(NotificationsView)
    const buttons = wrapper.findAll('button')
    expect(buttons.length).toBeGreaterThan(0)
  })

  it('renders category preference toggles', () => {
    const wrapper = mountWithPlugins(NotificationsView)
    const switches = wrapper.findAll('[role="switch"]')
    expect(switches.length).toBe(4)
  })

  it('disables toggle for required categories', () => {
    const wrapper = mountWithPlugins(NotificationsView)
    const switches = wrapper.findAll('[role="switch"]')
    expect(switches[0].attributes('disabled')).toBeDefined()
    expect(switches[1].attributes('disabled')).toBeUndefined()
  })
})

describe('NotificationsView test push (BRY-682)', () => {
  it('shows the test button only once the device is subscribed', () => {
    const wrapper = mountWithPlugins(NotificationsView)
    expect(wrapper.find('[data-testid="send-test-push"]').exists()).toBe(false)
  })

  it('sends a test push and shows the per-subscription counts', async () => {
    isSubscribed.value = true
    sendTestMutate.mockImplementation((_v: unknown, opts: MutateOpts) =>
      opts.onSuccess?.({ sent: 1, removed: 1, failed: 0, total: 2 }),
    )
    const wrapper = mountWithPlugins(NotificationsView)
    const button = wrapper.find('[data-testid="send-test-push"]')
    expect(button.exists()).toBe(true)
    expect(button.classes()).toContain('min-h-11')

    await button.trigger('click')
    await flushPromises()

    expect(sendTestMutate).toHaveBeenCalledTimes(1)
    const result = wrapper.find('[data-testid="test-push-result"]')
    expect(result.text()).toContain('notifications.test.sent')
    expect(result.text()).toContain('notifications.test.removed')
    expect(result.text()).not.toContain('notifications.test.failed')
  })

  it('explains a missing subscription from the error code', async () => {
    isSubscribed.value = true
    sendTestMutate.mockImplementation((_v: unknown, opts: MutateOpts) =>
      opts.onError?.(new ApiError(409, 'no device', 'no_push_subscription')),
    )
    const wrapper = mountWithPlugins(NotificationsView)
    await wrapper.find('[data-testid="send-test-push"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="test-push-result"]').text()).toBe('notifications.test.noSubscription')
  })

  it('explains a server without push configured', async () => {
    isSubscribed.value = true
    sendTestMutate.mockImplementation((_v: unknown, opts: MutateOpts) =>
      opts.onError?.(new ApiError(503, 'not configured', 'push_not_configured')),
    )
    const wrapper = mountWithPlugins(NotificationsView)
    await wrapper.find('[data-testid="send-test-push"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="test-push-result"]').text()).toBe('notifications.test.notConfigured')
  })
})

describe('NotificationsView iPhone help (BRY-682)', () => {
  it('shows the Home Screen steps on iOS in a Safari tab', () => {
    platform.appleMobile = true
    isSupported.value = false
    const wrapper = mountWithPlugins(NotificationsView)
    const help = wrapper.find('[data-testid="ios-push-help"]')
    expect(help.exists()).toBe(true)
    expect(help.findAll('li')).toHaveLength(4)
    expect(help.text()).toContain('notifications.iosStep2')
    expect(wrapper.text()).toContain('notifications.iosNeedsHomeScreen')
  })

  it('hides the help when opened from the Home Screen', () => {
    platform.appleMobile = true
    platform.standalone = true
    const wrapper = mountWithPlugins(NotificationsView)
    expect(wrapper.find('[data-testid="ios-push-help"]').exists()).toBe(false)
  })

  it('hides the help on other platforms', () => {
    const wrapper = mountWithPlugins(NotificationsView)
    expect(wrapper.find('[data-testid="ios-push-help"]').exists()).toBe(false)
  })
})

describe('NotificationsView push switch errors (BRY-682)', () => {
  function enableButton(wrapper: ReturnType<typeof mountWithPlugins>) {
    return wrapper.findAll('button').find((b) => b.text() === 'notifications.enable')!
  }

  it('subscribes straight from the click', async () => {
    const wrapper = mountWithPlugins(NotificationsView)
    await enableButton(wrapper).trigger('click')
    expect(subscribeMock).toHaveBeenCalledTimes(1)
    await flushPromises()
    expect(wrapper.find('[data-testid="push-error"]').exists()).toBe(false)
  })

  it.each([
    [new PushPermissionError('denied'), 'notifications.pushDenied'],
    [new PushNotConfiguredError(), 'notifications.test.notConfigured'],
    [new Error('AbortError'), 'notifications.pushSubscribeFailed'],
  ])('shows why turning on failed (%o)', async (err, key) => {
    subscribeMock.mockRejectedValueOnce(err)
    const wrapper = mountWithPlugins(NotificationsView)
    await enableButton(wrapper).trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="push-error"]').text()).toBe(key)
  })

  it('shows when turning off failed', async () => {
    isSubscribed.value = true
    unsubscribeMock.mockRejectedValueOnce(new Error('network'))
    const wrapper = mountWithPlugins(NotificationsView)
    const off = wrapper.findAll('button').find((b) => b.text() === 'notifications.disable')!
    await off.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="push-error"]').text()).toBe('notifications.pushUnsubscribeFailed')
  })
})
