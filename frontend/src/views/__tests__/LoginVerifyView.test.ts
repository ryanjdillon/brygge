import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createTestingPinia } from '@pinia/testing'
import { createRouter, createMemoryHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import LoginVerifyView from '@/views/LoginVerifyView.vue'

const client = { GET: vi.fn(), POST: vi.fn() }

vi.mock('@/lib/apiClient', async () => {
  const actual = await vi.importActual<typeof import('@/lib/apiClient')>('@/lib/apiClient')
  return { useApiClient: () => client, unwrap: actual.unwrap, ApiError: actual.ApiError }
})

function ok(data: unknown) {
  return { data, response: new Response(null, { status: 200 }) }
}
function fail(status: number, code: string) {
  return { error: { error: 'refused', code }, response: new Response(null, { status }) }
}

const page = { template: '<div />' }

// Makes the stubbed checkSession load a session, as the real one does after a
// successful login.
function signsIn(auth: ReturnType<typeof useAuthStore>) {
  vi.mocked(auth.checkSession).mockImplementation(async () => {
    auth.user = { id: 'u-1', roles: ['member'] } as unknown as typeof auth.user
  })
}

// The app's guard resolves the route before the page renders; mount the
// same way, so the token is in the query when the page sets up.
async function mountAt(query: string) {
  const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(() => new Promise(() => {}))
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      ...['/', '/login', '/portal'].map((path) => ({ path, component: page })),
      { path: '/login/verify', component: LoginVerifyView },
    ],
  })
  await router.push(`/login/verify${query}`)
  await router.isReady()
  const wrapper = mount(LoginVerifyView, {
    global: { plugins: [createTestingPinia({ createSpy: vi.fn }), router] },
  })
  await flushPromises()
  return { wrapper, router, fetchSpy }
}

describe('LoginVerifyView', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    client.GET.mockReset()
    client.POST.mockReset()
  })

  it('makes no request on load and drops the token from the address', async () => {
    const { wrapper, router, fetchSpy } = await mountAt('?token=tok-1&lang=nb')
    expect(client.POST).not.toHaveBeenCalled()
    expect(client.GET).not.toHaveBeenCalled()
    // The auth store reads the session when it is created; nothing else is
    // fetched, and nothing that writes.
    for (const [url, init] of fetchSpy.mock.calls) {
      expect(String(url)).not.toContain('/auth/verify')
      expect(init?.method ?? 'GET').toBe('GET')
    }
    expect(router.currentRoute.value.query.token).toBeUndefined()
    expect(router.currentRoute.value.query.lang).toBe('nb')
    expect(router.currentRoute.value.fullPath).not.toContain('tok-1')
    expect(wrapper.find('[data-testid="verify-ready"]').text()).toContain('login.verify.title')
  })

  it('logs in on the button with the token it kept, reloads the session and opens the portal', async () => {
    client.POST.mockResolvedValue(ok({ status: 'logged_in' }))
    const { wrapper, router } = await mountAt('?token=tok-1')
    const replace = vi.spyOn(router, 'replace')
    const auth = useAuthStore()
    signsIn(auth)

    await wrapper.find('[data-testid="verify-button"]').trigger('click')
    await flushPromises()

    expect(client.POST).toHaveBeenCalledTimes(1)
    expect(client.POST).toHaveBeenCalledWith('/api/v1/auth/verify', { body: { token: 'tok-1' } })
    expect(auth.checkSession).toHaveBeenCalled()
    expect(replace).toHaveBeenCalledWith('/portal')
  })

  it('asks for the link from the email again when there is no token, without calling the server', async () => {
    // A reload after the token was dropped from the address lands here.
    const { wrapper } = await mountAt('')
    expect(client.POST).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="verify-problem-login_token_missing"]').text()).toBe(
      'login.verify.problems.login_token_missing.body',
    )
    expect(wrapper.find('[data-testid="verify-problem-login_token_invalid"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="verify-button"]').exists()).toBe(false)
  })

  it('shows an error with a retry when the login worked but the session does not load', async () => {
    client.POST.mockResolvedValue(ok({ status: 'logged_in' }))
    const { wrapper, router } = await mountAt('?token=tok-1')
    const replace = vi.spyOn(router, 'replace')
    const auth = useAuthStore()

    await wrapper.find('[data-testid="verify-button"]').trigger('click')
    await flushPromises()

    expect(auth.checkSession).toHaveBeenCalledTimes(1)
    expect(replace).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="verify-session-failed"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="verify-session-portal"]').attributes('href')).toBe('/portal')

    // The retry loads the session again and opens the portal; it does not
    // send the spent token a second time.
    signsIn(auth)
    await wrapper.find('[data-testid="verify-session-retry"]').trigger('click')
    await flushPromises()
    expect(client.POST).toHaveBeenCalledTimes(1)
    expect(auth.checkSession).toHaveBeenCalledTimes(2)
    expect(replace).toHaveBeenCalledWith('/portal')
  })

  it.each(['login_token_invalid', 'login_token_used', 'login_token_expired'])(
    'shows %s with a link to request a new one',
    async (code) => {
      client.POST.mockResolvedValue(fail(410, code))
      const { wrapper } = await mountAt('?token=tok-1')
      await wrapper.find('[data-testid="verify-button"]').trigger('click')
      await flushPromises()
      expect(wrapper.find(`[data-testid="verify-problem-${code}"]`).text()).toBe(`login.verify.problems.${code}.body`)
      expect(wrapper.find('[data-testid="verify-request-new"]').attributes('href')).toBe('/login')
      expect(useAuthStore().checkSession).not.toHaveBeenCalled()
    },
  )

  it('falls back to a generic message for anything else', async () => {
    client.POST.mockResolvedValue(fail(500, ''))
    const { wrapper } = await mountAt('?token=tok-1')
    await wrapper.find('[data-testid="verify-button"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="verify-problem-failed"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="verify-request-new"]').attributes('href')).toBe('/login')
  })
  // The limiter refuses before the token is read, so the page keeps it and
  // offers the login button again besides a new link.
  it.each([
    [429, 'rate_limited', 'too many requests'],
    [503, 'unavailable', 'rate_limit_unavailable'],
  ])('a %i shows %s and retries with the same token', async (status, problem, error) => {
    client.POST.mockResolvedValueOnce({ error: { error }, response: new Response(null, { status }) })
    const { wrapper, router } = await mountAt('?token=tok-1')
    const replace = vi.spyOn(router, 'replace')
    const auth = useAuthStore()

    await wrapper.find('[data-testid="verify-button"]').trigger('click')
    await flushPromises()
    expect(wrapper.find(`[data-testid="verify-problem-${problem}"]`).text()).toBe(`login.verify.problems.${problem}.body`)
    expect(wrapper.find('[data-testid="verify-request-new"]').attributes('href')).toBe('/login')
    const retry = wrapper.find('[data-testid="verify-retry"]')
    expect(retry.exists()).toBe(true)
    expect(retry.text()).toBe('login.verify.retry')
    expect(retry.classes()).toContain('min-h-11')
    expect(auth.checkSession).not.toHaveBeenCalled()

    client.POST.mockResolvedValueOnce(ok({ status: 'logged_in' }))
    signsIn(auth)
    await retry.trigger('click')
    await flushPromises()
    expect(client.POST).toHaveBeenCalledTimes(2)
    expect(client.POST).toHaveBeenLastCalledWith('/api/v1/auth/verify', { body: { token: 'tok-1' } })
    expect(replace).toHaveBeenCalledWith('/portal')
  })

  it('offers no retry for a spent token', async () => {
    client.POST.mockResolvedValue(fail(410, 'login_token_used'))
    const { wrapper } = await mountAt('?token=tok-1')
    await wrapper.find('[data-testid="verify-button"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="verify-retry"]').exists()).toBe(false)
  })
})
