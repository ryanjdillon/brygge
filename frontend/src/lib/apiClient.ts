import createClient from 'openapi-fetch'
import type { paths } from '@/types/api'
import { ApiError } from '@/lib/errors'

export { ApiError }

let _client: ReturnType<typeof createClient<paths>> | null = null

function getClient() {
  if (!_client) {
    _client = createClient<paths>({ credentials: 'include' })
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
