import { useAuthStore } from '@/stores/auth'
import { useTotpGateStore } from '@/stores/totpGate'

// Shared TOTP step-up helpers. Use ensureFreshTotp() at click time
// (before opening a form/confirm); use totpAwareFetch() to wrap any
// admin API call that the backend RequireFreshTOTP middleware guards
// — it transparently re-prompts and retries on 403 totp_fresh_required
// instead of bubbling the raw error up to the user.
export function useFreshTotp() {
  const auth = useAuthStore()
  const gate = useTotpGateStore()

  async function ensureFreshTotp(): Promise<boolean> {
    if (auth.hasFreshTotp) return true
    return gate.open()
  }

  async function totpAwareFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
    const opts: RequestInit = { credentials: 'include', ...init }
    let res = await fetch(input, opts)
    if (res.status !== 403) return res
    const cloned = res.clone()
    let body: { error?: string } = {}
    try {
      body = await cloned.json()
    } catch {
      return res
    }
    if (body.error !== 'totp_fresh_required') return res
    const ok = await gate.open()
    if (!ok) return res
    res = await fetch(input, opts)
    return res
  }

  return { ensureFreshTotp, totpAwareFetch }
}
