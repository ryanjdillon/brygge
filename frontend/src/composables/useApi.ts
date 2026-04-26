import { useAuthStore } from '@/stores/auth'
import { useTotpGateStore } from '@/stores/totpGate'
import { ApiError } from '@/lib/errors'

export { ApiError }

// Limit replay-after-step-up retries to once per request to avoid
// loops if the user closes the modal repeatedly or the backend keeps
// answering 403 for some other reason.
const MAX_TOTP_RETRIES = 1

export function useApi() {
  const auth = useAuthStore()
  const totpGate = useTotpGateStore()

  async function fetchApi<T = unknown>(
    url: string,
    options: RequestInit = {},
    _retries = 0,
  ): Promise<T> {
    const headers = new Headers(options.headers)

    if (options.body && !headers.has('Content-Type')) {
      headers.set('Content-Type', 'application/json')
    }

    const response = await fetch(url, { ...options, headers, credentials: 'include' })

    if (!response.ok) {
      const body = await response.json().catch(() => null)
      const message = body?.error || response.statusText || 'Request failed'
      const code = body?.code as string | undefined

      if (response.status === 401) {
        auth.logout()
        throw new ApiError(response.status, message, code)
      }

      // Step-up flows: distinguish session-level (12h) from per-action
      // (5min). The backend communicates which via `error` in the body.
      if (response.status === 403 && body?.error === 'totp_required') {
        // Full-page redirect to the verify view, preserving where the
        // user wanted to go. Avoid the redirect for /me-style probes
        // by checking auth.isAuthenticated — if the user isn't logged
        // in, treat it as a normal 403.
        if (auth.isAuthenticated && typeof window !== 'undefined') {
          const next = window.location.pathname + window.location.search
          window.location.href = '/admin/verify-totp?next=' + encodeURIComponent(next)
        }
        throw new ApiError(response.status, message, body?.error)
      }

      if (
        response.status === 403 &&
        body?.error === 'totp_fresh_required' &&
        _retries < MAX_TOTP_RETRIES
      ) {
        // Mount the modal (App.vue watches the totpGate store). When
        // the user verifies, replay the original request once. If they
        // cancel, surface the 403 to the caller so the UI can recover.
        const verified = await totpGate.open()
        if (verified) {
          return fetchApi<T>(url, options, _retries + 1)
        }
        throw new ApiError(response.status, message, body?.error)
      }

      throw new ApiError(response.status, message, code)
    }

    if (response.status === 204) {
      return undefined as T
    }

    return response.json()
  }

  return { fetchApi }
}
