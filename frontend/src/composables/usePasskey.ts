import {
  startRegistration,
  startAuthentication,
  browserSupportsWebAuthn,
} from '@simplewebauthn/browser'
import { useAuthStore } from '@/stores/auth'

export interface PasskeyInfo {
  id: string
  name: string
  transports: string[]
  backup_eligible: boolean
  backup_state: boolean
  created_at: string
  last_used_at?: string | null
}

export class PasskeyError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message)
    this.name = 'PasskeyError'
  }
}

// passkeysSupported reports whether this browser can do WebAuthn at all, so the
// UI can hide passkey affordances on unsupported clients.
export function passkeysSupported(): boolean {
  return browserSupportsWebAuthn()
}

async function req<T>(url: string, method = 'GET', body?: unknown): Promise<T> {
  const res = await fetch(url, {
    method,
    credentials: 'include',
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  if (!res.ok) {
    const data = await res.json().catch(() => null)
    throw new PasskeyError(res.status, data?.error ?? `${url} failed (${res.status})`)
  }
  // 200s from ceremony/mutation endpoints always carry a JSON body.
  return res.json() as Promise<T>
}

export function usePasskey() {
  const auth = useAuthStore()

  async function list(): Promise<PasskeyInfo[]> {
    const data = await req<{ passkeys: PasskeyInfo[] }>('/api/v1/auth/passkeys')
    return data.passkeys ?? []
  }

  // register runs the full enrollment ceremony. The backend gates begin/finish
  // on fresh TOTP, so a PasskeyError with message 'totp_fresh_required' means
  // the caller must step up first.
  async function register(): Promise<void> {
    const optionsJSON = await req<Record<string, unknown>>(
      '/api/v1/auth/passkeys/register/begin',
      'POST',
    )
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const attResp = await startRegistration({ optionsJSON: optionsJSON as any })
    await req('/api/v1/auth/passkeys/register/finish', 'POST', attResp)
    // hasPasskey may have flipped; refresh the store.
    await auth.checkSession()
  }

  // stepUp satisfies the admin step-up with a passkey assertion instead of a
  // TOTP code. On success the session is stamped and /session/me reflects it.
  async function stepUp(): Promise<void> {
    const optionsJSON = await req<Record<string, unknown>>(
      '/api/v1/auth/passkeys/step-up/begin',
      'POST',
    )
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const assResp = await startAuthentication({ optionsJSON: optionsJSON as any })
    await req('/api/v1/auth/passkeys/step-up/finish', 'POST', assResp)
    await auth.checkSession()
  }

  async function rename(id: string, name: string): Promise<void> {
    await req(`/api/v1/auth/passkeys/${id}`, 'PATCH', { name })
  }

  async function remove(id: string): Promise<void> {
    await req(`/api/v1/auth/passkeys/${id}`, 'DELETE')
    await auth.checkSession()
  }

  return { list, register, stepUp, rename, remove }
}
