import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { APPLICANT_COMPLETE_PATH } from '@/lib/applicantAccess'

const push = vi.fn()
const currentRoute = { value: { path: '/portal' } }
vi.mock('@/router', () => ({ router: { push, currentRoute } }))

vi.unmock('vue-i18n')
vi.unmock('@tanstack/vue-query')

const refusal = () =>
  new Response(JSON.stringify({ error: 'membership_not_completed' }), {
    status: 403,
    headers: { 'Content-Type': 'application/json' },
  })

describe('membership_not_completed', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    push.mockReset()
    currentRoute.value.path = '/portal'
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('', { status: 401 }))
    setActivePinia(createPinia())
  })

  it('the typed API client routes to the completion page', async () => {
    const { useApiClient } = await import('@/lib/apiClient')
    vi.mocked(globalThis.fetch).mockResolvedValueOnce(refusal())
    // Node's Request needs an absolute URL; the browser resolves the relative one.
    const res = await useApiClient().GET('/api/v1/members/directory', { baseUrl: 'http://localhost' })
    expect(res.response.status).toBe(403)
    expect(push).toHaveBeenCalledWith(APPLICANT_COMPLETE_PATH)
  })

  it('fetchApi routes to the completion page and still rejects', async () => {
    const { useAuthStore } = await import('@/stores/auth')
    await useAuthStore().ready
    const { useApi, ApiError } = await import('@/composables/useApi')
    vi.mocked(globalThis.fetch).mockResolvedValueOnce(refusal())
    await expect(useApi().fetchApi('/api/v1/documents')).rejects.toThrow(ApiError)
    expect(push).toHaveBeenCalledWith(APPLICANT_COMPLETE_PATH)
  })

  it('does not navigate when already on the completion page', async () => {
    currentRoute.value.path = APPLICANT_COMPLETE_PATH
    const { routeIfMembershipIncomplete } = await import('@/lib/membershipGate')
    expect(await routeIfMembershipIncomplete(403, 'membership_not_completed')).toBe(true)
    expect(push).not.toHaveBeenCalled()
  })

  it('leaves other 403s alone', async () => {
    const { routeIfMembershipIncomplete } = await import('@/lib/membershipGate')
    expect(await routeIfMembershipIncomplete(403, 'totp_required')).toBe(false)
    expect(await routeIfMembershipIncomplete(401, 'membership_not_completed')).toBe(false)
    expect(push).not.toHaveBeenCalled()
  })
})
