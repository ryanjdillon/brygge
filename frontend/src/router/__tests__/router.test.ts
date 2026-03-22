import { describe, it, expect, beforeEach, vi } from 'vitest'
import { createRouter, createMemoryHistory, type Router } from 'vue-router'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'

vi.unmock('vue-i18n')
vi.unmock('@tanstack/vue-query')

function createTestRouter(): Router {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div>Home</div>' } },
      { path: '/calendar', component: { template: '<div>Calendar</div>' } },
      { path: '/contact', component: { template: '<div>Contact</div>' } },
      { path: '/join', component: { template: '<div>Join</div>' } },
      { path: '/login', component: { template: '<div>Login</div>' } },
      {
        path: '/portal',
        component: { template: '<div>Portal</div>' },
        meta: { requiresAuth: true },
      },
      {
        path: '/admin',
        component: { template: '<div>Admin</div>' },
        meta: { requiresAuth: true, requiresAdmin: true },
      },
    ],
  })

  router.beforeEach((to) => {
    const auth = useAuthStore()

    if (to.meta.requiresAuth && !auth.isAuthenticated) {
      return { path: '/login' }
    }

    if (to.meta.requiresAdmin && !auth.hasRole('admin') && !auth.hasRole('board')) {
      return { path: '/' }
    }
  })

  return router
}

describe('router guards', () => {
  let router: Router

  beforeEach(() => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('', { status: 401 }),
    )
    setActivePinia(createPinia())
    router = createTestRouter()
  })

  it('public routes are accessible without auth', async () => {
    await router.push('/')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/')

    await router.push('/calendar')
    expect(router.currentRoute.value.path).toBe('/calendar')

    await router.push('/contact')
    expect(router.currentRoute.value.path).toBe('/contact')

    await router.push('/join')
    expect(router.currentRoute.value.path).toBe('/join')
  })

  it('/portal redirects to /login when unauthenticated', async () => {
    await router.push('/portal')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/login')
  })

  it('/admin redirects to / when user lacks admin/board role', async () => {
    const auth = useAuthStore()
    await auth.ready
    auth.user = { id: '1', name: 'Test', email: 'test@example.com', clubId: 'c1', roles: ['member'] }

    await router.push('/admin')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/')
  })

  it('/admin redirects to /login when unauthenticated', async () => {
    await router.push('/admin')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/login')
  })

  it('/portal is accessible when authenticated', async () => {
    const auth = useAuthStore()
    await auth.ready
    auth.user = { id: '1', name: 'Test', email: 'test@example.com', clubId: 'c1', roles: ['member'] }

    await router.push('/portal')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/portal')
  })

  it('/admin is accessible with admin role', async () => {
    const auth = useAuthStore()
    await auth.ready
    auth.user = { id: '1', name: 'Test', email: 'test@example.com', clubId: 'c1', roles: ['admin'] }

    await router.push('/admin')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/admin')
  })

  it('/admin is accessible with board role', async () => {
    const auth = useAuthStore()
    await auth.ready
    auth.user = { id: '1', name: 'Test', email: 'test@example.com', clubId: 'c1', roles: ['board'] }

    await router.push('/admin')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/admin')
  })
})
