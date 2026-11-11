import { describe, it, expect, beforeEach, vi } from 'vitest'
import { createRouter, createMemoryHistory, type Router } from 'vue-router'
import { setActivePinia, createPinia } from 'pinia'
import { guardRoute } from '@/router'

vi.unmock('vue-i18n')
vi.unmock('@tanstack/vue-query')

const page = { template: '<div />' }

function createTestRouter(): Router {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: page },
      { path: '/login', component: page },
      { path: '/admin/accounting/journal', component: page, meta: { requiresAuth: true, requiresAdmin: true } },
    ],
  })
  router.beforeEach(guardRoute)
  return router
}

describe('guard: a signed-out visitor keeps the page asked for', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(null, { status: 401 }))
  })

  it('sends the visitor to /login with the full path in ?redirect=', async () => {
    const router = createTestRouter()
    await router.push('/admin/accounting/journal?openPeriod=2027')
    expect(router.currentRoute.value.path).toBe('/login')
    expect(router.currentRoute.value.query.redirect).toBe('/admin/accounting/journal?openPeriod=2027')
  })
})
