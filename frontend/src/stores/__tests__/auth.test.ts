import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'
import { i18n } from '@/i18n'

vi.unmock('vue-i18n')
vi.unmock('@tanstack/vue-query')

describe('auth store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('initial state is unauthenticated', () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response('', { status: 401 }),
    )
    const auth = useAuthStore()
    expect(auth.isAuthenticated).toBe(false)
    expect(auth.user).toBeNull()
  })

  it('checkSession sets user on success', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({
        user_id: '1',
        club_id: 'c1',
        roles: ['member'],
        full_name: 'Test User',
        email: 'test@example.com',
      }), { status: 200 }),
    )
    const auth = useAuthStore()
    await auth.ready

    expect(auth.isAuthenticated).toBe(true)
    expect(auth.user?.name).toBe('Test User')
  })

  it('logout clears state', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({
        user_id: '1',
        club_id: 'c1',
        roles: ['member'],
        full_name: 'Test',
        email: 'test@example.com',
      }), { status: 200 }),
    )
    const auth = useAuthStore()
    await auth.ready

    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response('', { status: 200 }),
    )
    await auth.logout()

    expect(auth.user).toBeNull()
    expect(auth.isAuthenticated).toBe(false)
  })

  it('hasRole returns true for matching role', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({
        user_id: '1',
        club_id: 'c1',
        roles: ['member', 'admin'],
        full_name: 'Test',
        email: 'test@example.com',
      }), { status: 200 }),
    )
    const auth = useAuthStore()
    await auth.ready

    expect(auth.hasRole('admin')).toBe(true)
    expect(auth.hasRole('member')).toBe(true)
  })

  it('hasRole returns false for non-matching role', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({
        user_id: '1',
        club_id: 'c1',
        roles: ['member'],
        full_name: 'Test',
        email: 'test@example.com',
      }), { status: 200 }),
    )
    const auth = useAuthStore()
    await auth.ready

    expect(auth.hasRole('admin')).toBe(false)
    expect(auth.hasRole('board')).toBe(false)
  })

  it('hasRole returns false when no user', () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response('', { status: 401 }),
    )
    const auth = useAuthStore()
    expect(auth.hasRole('member')).toBe(false)
  })

  it('checkSession clears user on failed response', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('', { status: 401 }),
    )
    const auth = useAuthStore()
    await auth.ready

    expect(auth.user).toBeNull()
    expect(auth.isAuthenticated).toBe(false)
  })
  describe('requestMagicLink refusals', () => {
    async function requestWith(response: Response) {
      vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response('', { status: 401 }))
      const auth = useAuthStore()
      await auth.ready
      vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(response)
      const ok = await auth.requestMagicLink('someone@example.test')
      return { ok, auth }
    }

    it.each([
      [429, 'login.errors.rateLimited', '{"error":"too many requests","retry_after":42}'],
      [503, 'login.errors.unavailable', '{"error":"rate_limit_unavailable"}'],
      [500, 'login.errors.sendFailed', '{"error":"internal error"}'],
    ])('a %i shows %s and never the raw body', async (status, key, body) => {
      const { ok, auth } = await requestWith(
        new Response(body, { status, headers: { 'Content-Type': 'application/json' } }),
      )
      expect(ok).toBe(false)
      expect(auth.loginError).toBe(i18n.global.t(key))
      expect(auth.loginError).not.toBe(key)
      expect(auth.loginError).not.toContain('{')
      expect(auth.loginError).not.toContain('error')
    })

    it('a network failure shows the connection message', async () => {
      vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response('', { status: 401 }))
      const auth = useAuthStore()
      await auth.ready
      vi.spyOn(globalThis, 'fetch').mockRejectedValueOnce(new TypeError('Failed to fetch'))
      expect(await auth.requestMagicLink('someone@example.test')).toBe(false)
      expect(auth.loginError).toBe(i18n.global.t('login.errors.network'))
    })
  })
})
