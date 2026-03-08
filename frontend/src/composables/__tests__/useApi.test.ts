import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useApi, ApiError } from '@/composables/useApi'
import { useAuthStore } from '@/stores/auth'

vi.unmock('vue-i18n')
vi.unmock('@tanstack/vue-query')

describe('useApi', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('fetchApi adds Authorization header when token exists', async () => {
    const auth = useAuthStore()
    auth.accessToken = 'test-token'

    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify({ data: 'ok' }), { status: 200 }),
    )

    const { fetchApi } = useApi()
    await fetchApi('/api/test')

    const calledHeaders = fetchSpy.mock.calls[0][1]?.headers as Headers
    expect(calledHeaders.get('Authorization')).toBe('Bearer test-token')
  })

  it('fetchApi works without auth token', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify({ data: 'ok' }), { status: 200 }),
    )

    const { fetchApi } = useApi()
    await fetchApi('/api/test')

    const calledHeaders = fetchSpy.mock.calls[0][1]?.headers as Headers
    expect(calledHeaders.get('Authorization')).toBeNull()
  })

  it('fetchApi handles 401 by triggering logout', async () => {
    const auth = useAuthStore()
    auth.accessToken = 'expired-token'
    const logoutSpy = vi.spyOn(auth, 'logout')

    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response('Unauthorized', { status: 401 }),
    )

    const { fetchApi } = useApi()

    await expect(fetchApi('/api/test')).rejects.toThrow(ApiError)
    expect(logoutSpy).toHaveBeenCalled()
  })

  it('fetchApi throws ApiError on non-ok responses', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response('Not Found', { status: 404 }),
    )

    const { fetchApi } = useApi()

    try {
      await fetchApi('/api/test')
      expect.fail('Should have thrown')
    } catch (err) {
      expect(err).toBeInstanceOf(ApiError)
      expect((err as ApiError).status).toBe(404)
      expect((err as ApiError).message).toBe('Request failed')
    }
  })

  it('fetchApi sets Content-Type for requests with body', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify({ id: 1 }), { status: 200 }),
    )

    const { fetchApi } = useApi()
    await fetchApi('/api/test', {
      method: 'POST',
      body: JSON.stringify({ name: 'test' }),
    })

    const calledHeaders = fetchSpy.mock.calls[0][1]?.headers as Headers
    expect(calledHeaders.get('Content-Type')).toBe('application/json')
  })

  it('fetchApi parses JSON error with code', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'Rate limited', code: 'RATE_LIMITED' }), {
        status: 429,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchApi } = useApi()

    try {
      await fetchApi('/api/test')
      expect.fail('Should have thrown')
    } catch (err) {
      expect(err).toBeInstanceOf(ApiError)
      expect((err as ApiError).status).toBe(429)
      expect((err as ApiError).message).toBe('Rate limited')
      expect((err as ApiError).code).toBe('RATE_LIMITED')
    }
  })

  it('fetchApi returns undefined for 204 responses', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(null, { status: 204 }),
    )

    const { fetchApi } = useApi()
    const result = await fetchApi('/api/test')
    expect(result).toBeUndefined()
  })
})
