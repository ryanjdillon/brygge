import { describe, it, expect, beforeEach, vi } from 'vitest'
import { createRouter, createMemoryHistory, type Router } from 'vue-router'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'
import { guardRoute, router as appRouter } from '@/router'
import { APPLICANT_COMPLETE_PATH, isApplicantOnly } from '@/lib/applicantAccess'

vi.unmock('vue-i18n')
vi.unmock('@tanstack/vue-query')

const page = { template: '<div />' }

function createTestRouter(): Router {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: page },
      { path: '/join', component: page },
      { path: '/join/confirm', component: page },
      { path: '/vilkar/:docType', component: page },
      { path: '/login', component: page },
      { path: '/betal/:token', component: page },
      {
        path: APPLICANT_COMPLETE_PATH,
        component: page,
        meta: { requiresAuth: true, applicantAllowed: true, applicantOnly: true },
      },
      {
        path: '/portal',
        component: page,
        meta: { requiresAuth: true },
        children: [
          { path: '', component: page },
          { path: 'directory', component: page },
          { path: 'documents', component: page },
          { path: 'profile', component: page, meta: { applicantAllowed: true } },
          { path: 'invoices', component: page, meta: { applicantAllowed: true } },
        ],
      },
      {
        path: '/admin',
        component: page,
        meta: { requiresAuth: true, requiresAdmin: true },
      },
    ],
  })
  router.beforeEach(guardRoute)
  return router
}

async function signIn(roles: string[]) {
  const auth = useAuthStore()
  await auth.ready
  auth.user = {
    id: '1', firstName: 'Test', lastName: '', name: 'Test', email: 'test@example.com', clubId: 'c1', roles,
    totpEnabled: false, totpVerifiedAt: null, hasPasskey: false, mfaMethod: '', preferredLanguage: null, clubDefaultLanguage: 'nb',
  }
}

describe('isApplicantOnly', () => {
  it.each([
    [['applicant'], true],
    [['applicant', 'applicant'], true],
    [[], false],
    [['member'], false],
    [['applicant', 'member'], false],
    [['applicant', 'board'], false],
  ])('%j -> %s', (roles, want) => {
    expect(isApplicantOnly(roles)).toBe(want)
  })
})

describe('applicant guard', () => {
  let router: Router

  beforeEach(() => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('', { status: 401 }))
    setActivePinia(createPinia())
    router = createTestRouter()
  })

  it.each(['/portal', '/portal/directory', '/portal/documents', '/admin'])(
    'sends an applicant from %s to the completion page',
    async (path) => {
      await signIn(['applicant'])
      await router.push(path)
      await router.isReady()
      expect(router.currentRoute.value.path).toBe(APPLICANT_COMPLETE_PATH)
    },
  )

  it.each(['/portal/profile', '/portal/invoices', APPLICANT_COMPLETE_PATH, '/', '/join', '/betal/abc'])(
    'lets an applicant open %s',
    async (path) => {
      await signIn(['applicant'])
      await router.push(path)
      await router.isReady()
      expect(router.currentRoute.value.path).toBe(path)
    },
  )

  it('leaves a member on member pages and sends them away from the completion page', async () => {
    await signIn(['member'])
    await router.push('/portal/directory')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/portal/directory')

    await router.push(APPLICANT_COMPLETE_PATH)
    expect(router.currentRoute.value.path).toBe('/portal')
  })

  it('does not hold back a board member who is also an applicant', async () => {
    await signIn(['applicant', 'board'])
    await router.push('/portal/directory')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/portal/directory')
  })

  it('still sends a signed-out visitor to login', async () => {
    await router.push(APPLICANT_COMPLETE_PATH)
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/login')
  })
})

describe('applicant pages in the app router', () => {
  it.each(['/portal/profile', '/portal/invoices', '/portal/privacy', '/portal/privacy-policy', APPLICANT_COMPLETE_PATH])(
    '%s is open to applicants',
    (path) => {
      expect(appRouter.resolve(path).meta.applicantAllowed).toBe(true)
    },
  )

  it.each(['/portal', '/portal/directory', '/portal/documents', '/portal/waiting-list', '/portal/slip-sharing', '/portal/security'])(
    '%s is closed to applicants',
    (path) => {
      expect(appRouter.resolve(path).meta.applicantAllowed).toBeFalsy()
    },
  )
})

describe('the completion page', () => {
  it('renders inside the portal layout and requires a session', () => {
    const resolved = appRouter.resolve(APPLICANT_COMPLETE_PATH)
    expect(resolved.matched[0].path).toBe('/portal')
    expect(resolved.meta.requiresAuth).toBe(true)
    expect(resolved.meta.applicantOnly).toBe(true)
  })
})

describe('the public join routes', () => {
  const JOIN_PATHS = ['/join', '/join/confirm?token=abc', '/vilkar/statutes']

  beforeEach(() => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('', { status: 401 }))
    setActivePinia(createPinia())
  })

  it.each(JOIN_PATHS)('%s needs no session in the app router', (path) => {
    const resolved = appRouter.resolve(path)
    expect(resolved.matched.length).toBeGreaterThan(0)
    expect(resolved.meta.requiresAuth).toBeFalsy()
  })

  it.each(JOIN_PATHS)('a signed-out visitor reaches %s', async (path) => {
    const router = createTestRouter()
    await router.push(path)
    await router.isReady()
    expect(router.currentRoute.value.fullPath).toBe(path)
  })

  it.each(JOIN_PATHS)('an applicant reaches %s', async (path) => {
    const router = createTestRouter()
    await signIn(['applicant'])
    await router.push(path)
    await router.isReady()
    expect(router.currentRoute.value.fullPath).toBe(path)
  })

  it('lets a member open /join, where the page says they already are one', async () => {
    const router = createTestRouter()
    await signIn(['member'])
    await router.push('/join')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/join')
  })

  it('keeps the completion page apart from the public join pages', () => {
    expect(appRouter.resolve('/join').matched[0].path).toBe('/join')
    expect(appRouter.resolve('/join/confirm').matched[0].path).toBe('/join/confirm')
    expect(appRouter.resolve(APPLICANT_COMPLETE_PATH).matched[0].path).toBe('/portal')
  })

  it('names the join routes', () => {
    expect(appRouter.resolve({ name: 'join' }).path).toBe('/join')
    expect(appRouter.resolve({ name: 'join-confirm' }).path).toBe('/join/confirm')
    expect(appRouter.resolve({ name: 'join-complete' }).path).toBe('/join/complete')
    expect(APPLICANT_COMPLETE_PATH).toBe('/join/complete')
  })

  it('sends the old Norwegian address to the join form', async () => {
    await appRouter.push('/bli-medlem')
    expect(appRouter.currentRoute.value.path).toBe('/join')
  })

  it.each(['/bli-medlem/stadfest', '/bli-medlem/fullfor'])('no longer serves %s', (path) => {
    expect(appRouter.resolve(path).matched).toHaveLength(0)
  })
})
