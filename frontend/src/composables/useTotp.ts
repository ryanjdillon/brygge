import { useAuthStore } from '@/stores/auth'

interface SetupResponse {
  secret: string
  qr_url: string
}

interface ConfirmResponse {
  message: string
  recovery_codes: string[]
}

interface VerifyResponse {
  message: string
}

interface RecoverResponse {
  message: string
  codes_remaining: number
}

interface RegenerateResponse {
  message: string
  recovery_codes: string[]
}

export class TotpError extends Error {
  constructor(public status: number, message: string) {
    super(message)
    this.name = 'TotpError'
  }
}

async function postJSON<T>(url: string, body?: unknown): Promise<T> {
  const res = await fetch(url, {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
  })
  if (!res.ok) {
    const data = await res.json().catch(() => null)
    throw new TotpError(res.status, data?.error ?? `${url} failed (${res.status})`)
  }
  return res.json() as Promise<T>
}

export function useTotp() {
  const auth = useAuthStore()

  async function setup(): Promise<SetupResponse> {
    return postJSON<SetupResponse>('/api/v1/admin/totp/setup')
  }

  // confirm enrolls TOTP and returns recovery codes ONCE.
  async function confirm(secret: string, code: string): Promise<ConfirmResponse> {
    const result = await postJSON<ConfirmResponse>('/api/v1/admin/totp/confirm', { secret, code })
    // Refresh /me so totpEnabled flips immediately in the store —
    // canAccessAdmin needs it before the user navigates.
    await auth.checkSession()
    return result
  }

  // verify stamps the session as TOTP-verified within the 12h window.
  async function verify(code: string): Promise<VerifyResponse> {
    const result = await postJSON<VerifyResponse>('/api/v1/admin/totp/verify', { code })
    await auth.checkSession()
    return result
  }

  // recover redeems a single-use recovery code (same effect as verify
  // for unlocking the step-up window, but burns the code).
  async function recover(code: string): Promise<RecoverResponse> {
    const result = await postJSON<RecoverResponse>('/api/v1/admin/totp/recover', { code })
    await auth.checkSession()
    return result
  }

  // regenerateCodes wipes existing recovery codes and returns a fresh
  // batch. Backend gates this on RequireFreshTOTP(5m); the modal
  // provided by the parent view should obtain that freshness first.
  async function regenerateCodes(): Promise<RegenerateResponse> {
    return postJSON<RegenerateResponse>('/api/v1/admin/totp/regenerate-codes')
  }

  return { setup, confirm, verify, recover, regenerateCodes }
}
