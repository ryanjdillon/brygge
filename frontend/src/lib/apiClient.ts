import createClient, { type Middleware } from 'openapi-fetch'
import type { paths } from '@/types/api'
import { useAuthStore } from '@/stores/auth'
import { ApiError } from '@/lib/errors'

export { ApiError }

function createAuthMiddleware(): Middleware {
  return {
    onRequest({ request }) {
      const auth = useAuthStore()
      if (auth.accessToken) {
        request.headers.set('Authorization', `Bearer ${auth.accessToken}`)
      }
      return request
    },
    onResponse({ response }) {
      if (response.status === 401) {
        const auth = useAuthStore()
        auth.logout()
      }
      return response
    },
  }
}

let _client: ReturnType<typeof createClient<paths>> | null = null

function getClient() {
  if (!_client) {
    _client = createClient<paths>()
    _client.use(createAuthMiddleware())
  }
  return _client
}

export function useApiClient() {
  return getClient()
}

/**
 * Unwrap an openapi-fetch response, throwing ApiError on failure.
 * Use in TanStack Query queryFn/mutationFn where throwing is expected.
 */
export function unwrap<T>(result: { data?: T; error?: unknown; response: Response }): T {
  if (result.error !== undefined || !result.response.ok) {
    const err = result.error as Record<string, unknown> | undefined
    const message = (err?.detail as string) ?? (err?.title as string) ?? result.response.statusText ?? 'Request failed'
    const code = err?.code as string | undefined
    throw new ApiError(result.response.status, message, code)
  }
  return result.data as T
}
