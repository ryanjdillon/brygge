import { useAuthStore } from '@/stores/auth'

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

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

    if (response.status === 401) {
      auth.logout()
      throw new ApiError(401, 'Unauthorized')
    }

    if (!response.ok) {
      const text = await response.text().catch(() => 'Request failed')
      throw new ApiError(response.status, text)
    }

    if (response.status === 204) {
      return undefined as T
    }

    return response.json()
  }

  return { fetchApi }
}
