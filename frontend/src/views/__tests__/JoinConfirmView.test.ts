import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createTestingPinia } from '@pinia/testing'
import { createRouter, createMemoryHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import JoinConfirmView from '@/views/JoinConfirmView.vue'

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

// The app's guard resolves the route before the page renders; mount the
// same way, so the token is in the query when the page sets up.
async function mountAt(query: string) {
  vi.spyOn(globalThis, 'fetch').mockImplementation(() => new Promise(() => {}))
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      ...['/', '/join', '/login', '/contact', '/betal/:token'].map((path) => ({ path, component: page })),
      { path: '/join/confirm', component: JoinConfirmView },
    ],
  })
  await router.push(`/join/confirm${query}`)
  await router.isReady()
  const wrapper = mount(JoinConfirmView, {
    global: { plugins: [createTestingPinia({ createSpy: vi.fn }), router] },
  })
  await flushPromises()
  return wrapper
}

describe('JoinConfirmView', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    client.GET.mockReset()
    client.POST.mockReset()
  })

  it('reads the token and only looks it up on load', async () => {
    client.GET.mockResolvedValue(ok({ valid: true, first_name: 'Kari' }))
    const wrapper = await mountAt('?token=tok-1')
    expect(client.GET).toHaveBeenCalledWith('/api/v1/join/confirm/{token}', { params: { path: { token: 'tok-1' } } })
    expect(client.POST).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="confirm-ready"]').text()).toContain('join.confirm.title')
    expect(wrapper.find('[data-testid="confirm-button"]').exists()).toBe(true)
  })

  it('confirms on the button, reloads the session and opens the pay page in the app', async () => {
    client.GET.mockResolvedValue(ok({ valid: true, first_name: 'Kari' }))
    client.POST.mockResolvedValue(ok({ checkout_url: `${window.location.origin}/betal/order-1`, order_token: 'order-1' }))
    const wrapper = await mountAt('?token=tok-1')
    const push = vi.spyOn(wrapper.vm.$router, 'push').mockResolvedValue(undefined)
    const auth = useAuthStore()

    await wrapper.find('[data-testid="confirm-button"]').trigger('click')
    await flushPromises()

    expect(client.POST).toHaveBeenCalledWith('/api/v1/join/confirm', { body: { token: 'tok-1' } })
    expect(auth.checkSession).toHaveBeenCalled()
    expect(push).toHaveBeenCalledWith('/betal/order-1')
  })

  it('treats a missing token as an invalid link without calling the server', async () => {
    const wrapper = await mountAt('')
    expect(client.GET).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="confirm-problem-join_token_invalid"]').exists()).toBe(true)
  })

  it.each([
    ['join_token_invalid', '/join'],
    ['join_token_used', '/login'],
    ['join_token_expired', '/join'],
  ])('shows the GET answer %s with its next step', async (code, to) => {
    client.GET.mockResolvedValue(ok({ valid: false, first_name: '', code }))
    const wrapper = await mountAt('?token=tok-1')
    expect(wrapper.find(`[data-testid="confirm-problem-${code}"]`).text()).toBe(`join.confirm.problems.${code}.body`)
    expect(wrapper.find('[data-testid="confirm-action"]').attributes('href')).toBe(to)
    expect(wrapper.find('[data-testid="confirm-button"]').exists()).toBe(false)
  })

  it.each([
    [410, 'join_token_invalid', '/join'],
    [410, 'join_token_used', '/login'],
    [410, 'join_token_expired', '/join'],
    [409, 'join_closed', '/contact'],
    [409, 'already_member', '/login'],
    [409, 'rejoin_blocked', '/contact'],
  ])('maps a %i %s from the confirm to its message', async (status, code, to) => {
    client.GET.mockResolvedValue(ok({ valid: true, first_name: 'Kari' }))
    client.POST.mockResolvedValue(fail(status, code))
    const wrapper = await mountAt('?token=tok-1')
    await wrapper.find('[data-testid="confirm-button"]').trigger('click')
    await flushPromises()
    expect(wrapper.find(`[data-testid="confirm-problem-${code}"]`).text()).toBe(`join.confirm.problems.${code}.body`)
    expect(wrapper.find('[data-testid="confirm-action"]').attributes('href')).toBe(to)
    expect(useAuthStore().checkSession).not.toHaveBeenCalled()
  })

  it('falls back to a generic message for anything else', async () => {
    client.GET.mockResolvedValue(ok({ valid: true, first_name: 'Kari' }))
    client.POST.mockResolvedValue(fail(500, ''))
    const wrapper = await mountAt('?token=tok-1')
    await wrapper.find('[data-testid="confirm-button"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="confirm-problem-failed"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="confirm-action"]').exists()).toBe(false)
  })
})
