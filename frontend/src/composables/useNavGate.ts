import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useTotpGateStore } from '@/stores/totpGate'

// useNavGate centralizes the TOTP gating story for nav clicks. Two
// gates are exposed:
//
//   - gateToFresh(path): used by economy sidebar items where the
//     destination only protects sensitive admin operations (faktura
//     create/send, bank-account edits, etc.). Requires the per-action
//     10-minute fresh-TOTP window. If the user has no TOTP enrollment
//     yet, sends them to /portal/security?next=… so the cookie flow
//     resumes the original click after setup.
//
//   - gateToAdmin(path): used by the navbar "Admin" button. Requires
//     the 12-hour step-up window. Same enrollment redirect.
//
// Both return true when the gate resolved positively and the caller
// should proceed with the navigation; false when the user cancelled
// or was redirected for enrollment.
export function useNavGate() {
  const router = useRouter()
  const auth = useAuthStore()
  const gate = useTotpGateStore()

  function toEnrollment(nextPath: string) {
    router.push({ path: '/portal/security', query: { next: nextPath } })
  }

  async function gateToFresh(path: string): Promise<boolean> {
    if (!auth.user?.totpEnabled) {
      toEnrollment(path)
      return false
    }
    if (auth.hasFreshTotp) return true
    return gate.open()
  }

  async function gateToAdmin(path: string): Promise<boolean> {
    if (!auth.user?.totpEnabled) {
      toEnrollment(path)
      return false
    }
    if (auth.canAccessAdmin) return true
    return gate.open()
  }

  return { gateToFresh, gateToAdmin }
}
