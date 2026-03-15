import { vi } from 'vitest'
import { mount, type ComponentMountingOptions } from '@vue/test-utils'
import { createTestingPinia, type TestingOptions } from '@pinia/testing'
import { createRouter, createMemoryHistory } from 'vue-router'
import { type Component } from 'vue'

export { mount, shallowMount } from '@vue/test-utils'

const routes = [
  { path: '/', component: { template: '<div>Home</div>' } },
  { path: '/calendar', component: { template: '<div>Calendar</div>' } },
  { path: '/weather', component: { template: '<div>Weather</div>' } },
  { path: '/directions', component: { template: '<div>Directions</div>' } },
  { path: '/contact', component: { template: '<div>Contact</div>' } },
  { path: '/pricing', component: { template: '<div>Pricing</div>' } },
  { path: '/join', component: { template: '<div>Join</div>' } },
  { path: '/merchandise', component: { template: '<div>Merchandise</div>' } },
  { path: '/login', component: { template: '<div>Login</div>' } },
  { path: '/portal', component: { template: '<div>Portal</div>' }, meta: { requiresAuth: true } },
  { path: '/admin', component: { template: '<div>Admin</div>' }, meta: { requiresAuth: true, requiresAdmin: true } },
]

export function createTestRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes,
  })
}

export function mountWithPlugins<T extends Component>(
  component: T,
  options: ComponentMountingOptions<T> & { piniaOptions?: TestingOptions } = {},
) {
  const { piniaOptions, ...mountOptions } = options
  const router = createTestRouter()
  const pinia = createTestingPinia({
    createSpy: () => vi.fn(),
    ...piniaOptions,
  })

  return mount(component, {
    ...mountOptions,
    global: {
      plugins: [pinia, router, ...(mountOptions.global?.plugins ?? [])],
      stubs: {
        ...(mountOptions.global?.stubs ?? {}),
      },
      ...mountOptions.global,
    },
  })
}

export function createMockAuthStore(overrides: {
  user?: { id: string; name: string; email: string; roles: string[] } | null
  accessToken?: string | null
  isAuthenticated?: boolean
} = {}) {
  return {
    user: overrides.user ?? null,
    accessToken: overrides.accessToken ?? null,
    isAuthenticated: overrides.isAuthenticated ?? overrides.user !== null,
    login: vi.fn(),
    logout: vi.fn(),
    hasRole: vi.fn((role: string) => overrides.user?.roles.includes(role) ?? false),
  }
}
