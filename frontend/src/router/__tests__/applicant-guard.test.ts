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
