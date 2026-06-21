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

export function createTestRouter(initialRoute?: string) {
  const history = createMemoryHistory()
  // Seed the initial location before the router navigates, so components
  // that branch on the route (e.g. NavBar's hero vs standard nav on '/')
  // render the right variant from the first paint.
  if (initialRoute) history.replace(initialRoute)
  return createRouter({ history, routes })
}

export function mountWithPlugins<T extends Component>(
  component: T,
  options: ComponentMountingOptions<T> & { piniaOptions?: TestingOptions; initialRoute?: string } = {},
) {
  const { piniaOptions, initialRoute, ...mountOptions } = options
  const router = createTestRouter(initialRoute)
  const pinia = createTestingPinia({
    createSpy: () => vi.fn(),
    ...piniaOptions,
  })

  const wrapper = mount(component, {
    ...mountOptions,
    global: {
      plugins: [pinia, router, ...(mountOptions.global?.plugins ?? [])],
      stubs: {
        ...(mountOptions.global?.stubs ?? {}),
      },
      ...mountOptions.global,
    },
  })

  return wrapper
}

export function createMockAuthStore(overrides: {
  user?: { id: string; name: string; email: string; roles: string[] } | null
  isAuthenticated?: boolean
} = {}) {
  return {
    user: overrides.user ?? null,
    isAuthenticated: overrides.isAuthenticated ?? overrides.user !== null,
    logout: vi.fn(),
    hasRole: vi.fn((role: string) => overrides.user?.roles.includes(role) ?? false),
    checkSession: vi.fn(),
    requestMagicLink: vi.fn(),
  }
}
