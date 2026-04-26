import createClient from 'openapi-fetch'
import type { paths } from '@/types/api'
import { ApiError } from '@/lib/errors'
import { useTotpGateStore } from '@/stores/totpGate'

export { ApiError }

let _client: ReturnType<typeof createClient<paths>> | null = null

// Track which requests have already been retried after a step-up
// modal so we don't loop if the backend keeps answering 403.
const retried = new WeakSet<Request>()

function getClient() {
  if (_client) return _client
  _client = createClient<paths>({ credentials: 'include' })

  // openapi-fetch middleware: handle the two TOTP step-up signals so
  // every consumer (useApiClient, useQuery, etc.) gets the same UX
  // that useApi.ts provides for raw fetch callers.
  _client.use({
    async onResponse({ request, response }) {
      if (response.status !== 403) return

      // Peek the body without consuming it for the caller.
      const cloned = response.clone()
      let body: { error?: string; window_seconds?: number } | null = null
      try {
        body = await cloned.json()
      } catch {
        return
      }

      if (body?.error === 'totp_required') {
        if (typeof window !== 'undefined') {
          const next = window.location.pathname + window.location.search
          window.location.href = '/admin/verify-totp?next=' + encodeURIComponent(next)
        }
        return
      }

      if (body?.error === 'totp_fresh_required' && !retried.has(request)) {
        const totpGate = useTotpGateStore()
        const verified = await totpGate.open()
        if (!verified) return
        retried.add(request)
        return fetch(request)
      }
    },
  })

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
