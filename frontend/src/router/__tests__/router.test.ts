import { describe, it, expect, beforeEach, vi } from 'vitest'
import { createRouter, createMemoryHistory, type Router } from 'vue-router'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'
import { guardRoute, router as appRouter } from '@/router'

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
        children: [
          { path: 'accounting', component: { template: '<div>Books</div>' } },
          { path: 'dev/query', component: { template: '<div>Dev</div>' }, meta: { adminOnly: true } },
        ],
      },
    ],
  })

  router.beforeEach(guardRoute)

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
    auth.user = { id: '1', firstName: 'Test', lastName: '', name: 'Test', email: 'test@example.com', clubId: 'c1', roles: ['member'], totpEnabled: false, totpVerifiedAt: null, hasPasskey: false, mfaMethod: '', preferredLanguage: null, clubDefaultLanguage: 'nb' }

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
    auth.user = { id: '1', firstName: 'Test', lastName: '', name: 'Test', email: 'test@example.com', clubId: 'c1', roles: ['member'], totpEnabled: false, totpVerifiedAt: null, hasPasskey: false, mfaMethod: '', preferredLanguage: null, clubDefaultLanguage: 'nb' }

    await router.push('/portal')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/portal')
  })

  it('/admin is accessible with admin role', async () => {
    const auth = useAuthStore()
    await auth.ready
    auth.user = { id: '1', firstName: 'Test', lastName: '', name: 'Test', email: 'test@example.com', clubId: 'c1', roles: ['admin'], totpEnabled: false, totpVerifiedAt: null, hasPasskey: false, mfaMethod: '', preferredLanguage: null, clubDefaultLanguage: 'nb' }

    await router.push('/admin')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/admin')
  })

  it('/admin is accessible with board role', async () => {
    const auth = useAuthStore()
    await auth.ready
    auth.user = { id: '1', firstName: 'Test', lastName: '', name: 'Test', email: 'test@example.com', clubId: 'c1', roles: ['board'], totpEnabled: false, totpVerifiedAt: null, hasPasskey: false, mfaMethod: '', preferredLanguage: null, clubDefaultLanguage: 'nb' }

    await router.push('/admin')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/admin')
  })

  // BRY-372: every board role views the admin area; Developer and Site
  // pages (meta.adminOnly) stay with site admins.
  async function signIn(roles: string[]) {
    const auth = useAuthStore()
    await auth.ready
    auth.user = { id: '1', firstName: 'Test', lastName: '', name: 'Test', email: 'test@example.com', clubId: 'c1', roles, totpEnabled: false, totpVerifiedAt: null, hasPasskey: false, mfaMethod: '', preferredLanguage: null, clubDefaultLanguage: 'nb' }
  }

  it.each(['chair', 'vice_chair', 'secretary', 'deputy', 'harbor_master', 'treasurer', 'board'])(
    '%s views admin pages but not Developer pages',
    async (role) => {
      await signIn(['member', role])
      await router.push('/admin/accounting')
      await router.isReady()
      expect(router.currentRoute.value.path).toBe('/admin/accounting')

      await router.push('/admin/dev/query')
      expect(router.currentRoute.value.path).toBe('/admin')
    },
  )

  it('admin opens Developer pages', async () => {
    await signIn(['admin'])
    await router.push('/admin/dev/query')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/admin/dev/query')
  })

  it('a member opens neither', async () => {
    await signIn(['member', 'slip_holder'])
    await router.push('/admin/accounting')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/')
  })
})

describe('admin-only routes (BRY-372)', () => {
  const adminOnly = [
    '/admin/settings/general',
    '/admin/settings/security',
    '/admin/settings/site',
    '/admin/settings/faq',
    '/admin/map',
    '/admin/notifications',
    '/admin/gdpr',
    '/admin/dev/query',
    '/admin/dev/tokens',
  ]

  it.each(adminOnly)('%s is marked adminOnly', (path) => {
    expect(appRouter.resolve(path).meta.adminOnly).toBe(true)
  })

  it.each([
    '/admin/users',
    '/admin/accounting/faktura',
    '/admin/accounting/bank-imports',
    '/admin/economy/settings',
    '/admin/pricing',
    '/admin/harbor/settings',
  ])('%s is open to the board', (path) => {
    const meta = appRouter.resolve(path).meta
    expect(meta.requiresAdmin).toBe(true)
    expect(meta.adminOnly).toBeFalsy()
  })
})
