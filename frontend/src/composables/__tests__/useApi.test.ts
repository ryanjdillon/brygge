import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useApi, ApiError } from '@/composables/useApi'
import { useAuthStore } from '@/stores/auth'

vi.unmock('vue-i18n')
vi.unmock('@tanstack/vue-query')

describe('useApi', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    // Mock fetch before creating pinia so checkSession resolves cleanly
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('', { status: 401 }),
    )
    setActivePinia(createPinia())
  })

  it('fetchApi sends credentials include', async () => {
    const auth = useAuthStore()
    await auth.ready

    const fetchMock = vi.mocked(globalThis.fetch)
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ data: 'ok' }), { status: 200 }),
    )

    const callsBefore = fetchMock.mock.calls.length
    const { fetchApi } = useApi()
    await fetchApi('/api/test')

    const calledOptions = fetchMock.mock.calls[callsBefore][1]
    expect(calledOptions?.credentials).toBe('include')
  })

  it('fetchApi handles 401 by triggering logout', async () => {
    const auth = useAuthStore()
    await auth.ready
    const logoutSpy = vi.spyOn(auth, 'logout')

    vi.mocked(globalThis.fetch).mockResolvedValueOnce(
      new Response('Unauthorized', { status: 401 }),
    )

    const { fetchApi } = useApi()

    await expect(fetchApi('/api/test')).rejects.toThrow(ApiError)
    expect(logoutSpy).toHaveBeenCalled()
  })

  it('fetchApi throws ApiError on non-ok responses', async () => {
    const auth = useAuthStore()
    await auth.ready

    vi.mocked(globalThis.fetch).mockResolvedValueOnce(
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
    const auth = useAuthStore()
    await auth.ready

    const fetchMock = vi.mocked(globalThis.fetch)
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ id: 1 }), { status: 200 }),
    )

    const callsBefore = fetchMock.mock.calls.length
    const { fetchApi } = useApi()
    await fetchApi('/api/test', {
      method: 'POST',
      body: JSON.stringify({ name: 'test' }),
    })

    const calledHeaders = fetchMock.mock.calls[callsBefore][1]?.headers as Headers
    expect(calledHeaders.get('Content-Type')).toBe('application/json')
  })

  it('fetchApi parses JSON error with code', async () => {
    const auth = useAuthStore()
    await auth.ready

    vi.mocked(globalThis.fetch).mockResolvedValueOnce(
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
    const auth = useAuthStore()
    await auth.ready

    vi.mocked(globalThis.fetch).mockResolvedValueOnce(
      new Response(null, { status: 204 }),
    )

    const { fetchApi } = useApi()
    const result = await fetchApi('/api/test')
    expect(result).toBeUndefined()
  })
})
