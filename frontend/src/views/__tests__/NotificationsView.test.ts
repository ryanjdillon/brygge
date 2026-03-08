import { describe, it, expect, vi, beforeAll } from 'vitest'
import { ref } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import NotificationsView from '@/views/portal/NotificationsView.vue'

beforeAll(() => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  })
})

vi.mock('lucide-vue-next', () => ({
  Bell: { template: '<span data-icon="bell" />' },
  BellOff: { template: '<span data-icon="bell-off" />' },
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
    isSupported: ref(true),
    isSubscribed: ref(false),
    isLoading: ref(false),
    checkSubscription: vi.fn(),
    subscribe: vi.fn(),
    unsubscribe: vi.fn(),
  }),
}))

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
