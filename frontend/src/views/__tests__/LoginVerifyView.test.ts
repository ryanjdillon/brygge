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

const PEEK = '/api/v1/auth/verify/peek'
const VERIFY = '/api/v1/auth/verify'
const ACCOUNT = 'medlem@example.test'

type Reply = unknown | (() => unknown)

function ok(data: unknown) {
  return { data, response: new Response(null, { status: 200 }) }
}
function fail(status: number, code: string) {
  return { error: { error: 'refused', code }, response: new Response(null, { status }) }
}
function limited(status: number, error: string) {
  return { error: { error }, response: new Response(null, { status }) }
}

// Answers the page's POSTs by path: queued replies first, then the default.
// A reply that is a function is called (to throw, or to stay pending).
function answer(replies: { peek?: Reply[]; verify?: Reply[] }) {
  const queues: Record<string, Reply[]> = { [PEEK]: [...(replies.peek ?? [])], [VERIFY]: [...(replies.verify ?? [])] }
  client.POST.mockImplementation(async (path: string) => {
    const queue = queues[path]
    if (!queue || queue.length === 0) throw new Error(`unexpected POST ${path}`)
    const next = queue.length > 1 ? queue.shift() : queue[0]
    return typeof next === 'function' ? (next as () => unknown)() : next
  })
}

function posts(path: string) {
  return client.POST.mock.calls.filter(([p]) => p === path)
}

const page = { template: '<div />' }

// Makes the stubbed checkSession load a session, as the real one does after a
// successful login.
function signsIn(auth: ReturnType<typeof useAuthStore>) {
  vi.mocked(auth.checkSession).mockImplementation(async () => {
    auth.user = { id: 'u-1', email: ACCOUNT, roles: ['member'] } as unknown as typeof auth.user
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

  it('only peeks on load, shows the account and drops the token from the address', async () => {
    answer({ peek: [ok({ account: ACCOUNT })] })
    const { wrapper, router, fetchSpy } = await mountAt('?token=tok-1&lang=nb')

    expect(client.POST).toHaveBeenCalledTimes(1)
    expect(client.POST).toHaveBeenCalledWith(PEEK, { body: { token: 'tok-1' } })
    expect(posts(VERIFY)).toHaveLength(0)
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

    const ready = wrapper.find('[data-testid="verify-ready"]')
    expect(ready.text()).toContain('login.verify.title')
    expect(ready.text()).toContain('login.verify.accountLabel')
    expect(wrapper.find('[data-testid="verify-account"]').text()).toBe(ACCOUNT)
    expect(wrapper.find('[data-testid="verify-button"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="verify-not-you"]').attributes('href')).toBe('/login')
  })

  it('shows no button while the peek is running', async () => {
    answer({ peek: [() => new Promise(() => {})] })
    const { wrapper } = await mountAt('?token=tok-1')
    expect(wrapper.find('[data-testid="verify-peeking"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="verify-button"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="verify-account"]').exists()).toBe(false)
  })

  it('wraps a long address instead of overflowing', async () => {
    const long = `${'a-very-long-local-part-that-goes-on'.repeat(3)}@a-long-subdomain.example.test`
    answer({ peek: [ok({ account: long })] })
    const { wrapper } = await mountAt('?token=tok-1')
    const shown = wrapper.find('[data-testid="verify-account"]')
    expect(shown.text()).toBe(long)
    expect(shown.classes()).toContain('break-all')
  })

  it('logs in on the button with the token it kept, reloads the session and opens the portal', async () => {
    answer({ peek: [ok({ account: ACCOUNT })], verify: [ok({ status: 'logged_in' })] })
    const { wrapper, router } = await mountAt('?token=tok-1')
    const replace = vi.spyOn(router, 'replace')
    const auth = useAuthStore()
    signsIn(auth)

    await wrapper.find('[data-testid="verify-button"]').trigger('click')
    await flushPromises()

    expect(posts(VERIFY)).toEqual([[VERIFY, { body: { token: 'tok-1' } }]])
    expect(posts(PEEK)).toHaveLength(1)
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
    answer({ peek: [ok({ account: ACCOUNT })], verify: [ok({ status: 'logged_in' })] })
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
    expect(posts(VERIFY)).toHaveLength(1)
    expect(auth.checkSession).toHaveBeenCalledTimes(2)
    expect(replace).toHaveBeenCalledWith('/portal')
  })

  // A link the peek refuses goes straight to the problem: no button, no
  // login attempt.
  it.each(['login_token_invalid', 'login_token_used', 'login_token_expired'])(
    'a peek refused with %s shows the problem on load',
    async (code) => {
      answer({ peek: [fail(410, code)] })
      const { wrapper } = await mountAt('?token=tok-1')
      expect(wrapper.find(`[data-testid="verify-problem-${code}"]`).text()).toBe(`login.verify.problems.${code}.body`)
      expect(wrapper.find('[data-testid="verify-request-new"]').attributes('href')).toBe('/login')
      expect(wrapper.find('[data-testid="verify-button"]').exists()).toBe(false)
      expect(wrapper.find('[data-testid="verify-retry"]').exists()).toBe(false)
      expect(wrapper.find('[data-testid="verify-account"]').exists()).toBe(false)
      expect(posts(VERIFY)).toHaveLength(0)
    },
  )

  // When the peek gets no answer the page offers to ask again, never the
  // login button: an attacker could spend the visitor's peek budget to get
  // a button without an account line.
  it.each([
    ['429', () => limited(429, 'too many requests'), 'rate_limited'],
    ['503', () => limited(503, 'rate_limit_unavailable'), 'unavailable'],
    ['a network error', () => Promise.reject(new TypeError('Failed to fetch')), 'failed'],
    ['a 500', () => fail(500, ''), 'failed'],
    ['a 200 without an account', () => ok({}), 'failed'],
  ])('a peek that gets %s offers a retry and no button', async (_name, reply, kind) => {
    answer({ peek: [reply, ok({ account: ACCOUNT })] })
    const { wrapper } = await mountAt('?token=tok-1')

    expect(wrapper.find(`[data-testid="verify-peek-problem-${kind}"]`).text()).toBe(
      `login.verify.peekFailed.${kind}.body`,
    )
    expect(wrapper.find('[data-testid="verify-button"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="verify-account"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="verify-request-new"]').attributes('href')).toBe('/login')
    const retry = wrapper.find('[data-testid="verify-peek-retry"]')
    expect(retry.classes()).toContain('min-h-11')

    await retry.trigger('click')
    await flushPromises()
    expect(posts(PEEK)).toHaveLength(2)
    expect(posts(VERIFY)).toHaveLength(0)
    expect(wrapper.find('[data-testid="verify-account"]').text()).toBe(ACCOUNT)
    expect(wrapper.find('[data-testid="verify-button"]').exists()).toBe(true)
  })

  it('warns when the visitor is signed in as someone else', async () => {
    answer({ peek: [ok({ account: ACCOUNT })] })
    const { wrapper } = await mountAt('?token=tok-1')
    expect(wrapper.find('[data-testid="verify-other-user"]').exists()).toBe(false)

    const auth = useAuthStore()
    auth.user = { id: 'u-2', email: 'annen@example.test', roles: ['member'] } as unknown as typeof auth.user
    await flushPromises()
    const warning = wrapper.find('[data-testid="verify-other-user"]')
    expect(warning.exists()).toBe(true)
    expect(warning.text()).toContain('login.verify.otherUser')
    // The button stays: the visitor may mean to switch accounts.
    expect(wrapper.find('[data-testid="verify-button"]').exists()).toBe(true)

    // The same account, whatever its case, is no warning.
    auth.user = { id: 'u-1', email: 'Medlem@Example.test', roles: ['member'] } as unknown as typeof auth.user
    await flushPromises()
    expect(wrapper.find('[data-testid="verify-other-user"]').exists()).toBe(false)
  })

  it.each(['login_token_invalid', 'login_token_used', 'login_token_expired'])(
    'a login refused with %s shows it with a link to request a new one',
    async (code) => {
      answer({ peek: [ok({ account: ACCOUNT })], verify: [fail(410, code)] })
      const { wrapper } = await mountAt('?token=tok-1')
      await wrapper.find('[data-testid="verify-button"]').trigger('click')
      await flushPromises()
      expect(wrapper.find(`[data-testid="verify-problem-${code}"]`).text()).toBe(`login.verify.problems.${code}.body`)
      expect(wrapper.find('[data-testid="verify-request-new"]').attributes('href')).toBe('/login')
      expect(useAuthStore().checkSession).not.toHaveBeenCalled()
    },
  )

  it('falls back to a generic message for anything else', async () => {
    answer({ peek: [ok({ account: ACCOUNT })], verify: [fail(500, '')] })
    const { wrapper } = await mountAt('?token=tok-1')
    await wrapper.find('[data-testid="verify-button"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="verify-problem-failed"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="verify-request-new"]').attributes('href')).toBe('/login')
  })

  // The limiter refuses before the token is read, so the page keeps it and
  // offers the login again besides a new link (BRY-662).
  it.each([
    [429, 'rate_limited', 'too many requests'],
    [503, 'unavailable', 'rate_limit_unavailable'],
  ])('a login that gets %i shows %s and retries with the same token', async (status, problem, error) => {
    answer({ peek: [ok({ account: ACCOUNT })], verify: [limited(status, error), ok({ status: 'logged_in' })] })
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
    // The retry logs in, so the account it logs in as is shown with it.
    expect(wrapper.find('[data-testid="verify-account"]').text()).toBe(ACCOUNT)
    expect(auth.checkSession).not.toHaveBeenCalled()

    signsIn(auth)
    await retry.trigger('click')
    await flushPromises()
    expect(posts(VERIFY)).toHaveLength(2)
    expect(client.POST).toHaveBeenLastCalledWith(VERIFY, { body: { token: 'tok-1' } })
    expect(replace).toHaveBeenCalledWith('/portal')
  })

  it('keeps the different-user warning with the login retry', async () => {
    answer({ peek: [ok({ account: ACCOUNT })], verify: [limited(429, 'too many requests')] })
    const { wrapper } = await mountAt('?token=tok-1')
    const auth = useAuthStore()
    auth.user = { id: 'u-2', email: 'annen@example.test', roles: ['member'] } as unknown as typeof auth.user
    await wrapper.find('[data-testid="verify-button"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="verify-retry"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="verify-account"]').text()).toBe(ACCOUNT)
    expect(wrapper.find('[data-testid="verify-other-user"]').exists()).toBe(true)
  })

  it('drops the token from the address before it peeks', async () => {
    let queryAtPeek: unknown = 'not peeked'
    const routerRef: { current?: ReturnType<typeof createRouter> } = {}
    client.POST.mockImplementation(async () => {
      queryAtPeek = routerRef.current?.currentRoute.value.query.token
      return ok({ account: ACCOUNT })
    })
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(() => new Promise(() => {}))
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        ...['/', '/login', '/portal'].map((path) => ({ path, component: page })),
        { path: '/login/verify', component: LoginVerifyView },
      ],
    })
    routerRef.current = router
    await router.push('/login/verify?token=tok-1')
    await router.isReady()
    mount(LoginVerifyView, { global: { plugins: [createTestingPinia({ createSpy: vi.fn }), router] } })
    await flushPromises()
    expect(client.POST).toHaveBeenCalledWith(PEEK, { body: { token: 'tok-1' } })
    expect(queryAtPeek).toBeUndefined()
    fetchSpy.mockRestore()
  })

  it('offers no retry for a spent token', async () => {
    answer({ peek: [ok({ account: ACCOUNT })], verify: [fail(410, 'login_token_used')] })
    const { wrapper } = await mountAt('?token=tok-1')
    await wrapper.find('[data-testid="verify-button"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="verify-retry"]').exists()).toBe(false)
  })
})
