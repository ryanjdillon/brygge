import { useAuthStore } from '@/stores/auth'
import { ApiError } from '@/lib/errors'

export { ApiError }

export function useApi() {
  const auth = useAuthStore()

  async function fetchApi<T = unknown>(
    url: string,
    options: RequestInit = {},
  ): Promise<T> {
    const headers = new Headers(options.headers)

    if (auth.accessToken) {
      headers.set('Authorization', `Bearer ${auth.accessToken}`)
    }

    if (options.body && !headers.has('Content-Type')) {
      headers.set('Content-Type', 'application/json')
    }

    const response = await fetch(url, { ...options, headers })

    if (!response.ok) {
      const body = await response.json().catch(() => null)
      const message = body?.error || response.statusText || 'Request failed'
      const code = body?.code as string | undefined

      if (response.status === 401) {
        auth.logout()
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
