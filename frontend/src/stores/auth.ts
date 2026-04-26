import { ref, computed } from 'vue'
import { defineStore } from 'pinia'

interface User {
  id: string
  name: string
  email: string
  clubId: string
  roles: string[]
  totpEnabled: boolean
  totpVerifiedAt: Date | null
}

interface MeResponse {
  user_id: string
  club_id: string
  roles: string[]
  full_name: string
  email: string
  totp_enabled: boolean
  totp_verified_at?: string | null
}

// Mirrors the 12-hour step-up window enforced by the backend's
// RequireAdminTOTP middleware. Used purely for soft UX (hiding admin
// nav links when the gate would 403 anyway).
const TOTP_FRESH_MS = 12 * 60 * 60 * 1000

const ADMIN_ROLES = ['admin', 'board', 'treasurer', 'harbor_master']

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const loginError = ref<string | null>(null)

  const isAuthenticated = computed(() => user.value !== null)

  const hasAdminRole = computed(() => {
    if (!user.value) return false
    return user.value.roles.some((r) => ADMIN_ROLES.includes(r))
  })

  // canAccessAdmin: role-eligible AND TOTP enrolled AND verified
  // within the step-up window. The backend is the hard gate; this
  // just keeps the nav from showing dead-ends.
  const canAccessAdmin = computed(() => {
    if (!hasAdminRole.value || !user.value) return false
    if (!user.value.totpEnabled || !user.value.totpVerifiedAt) return false
    return Date.now() - user.value.totpVerifiedAt.getTime() < TOTP_FRESH_MS
  })

  async function checkSession() {
    try {
      const res = await fetch('/api/v1/auth/session/me', {
        credentials: 'include',
      })
      if (!res.ok) {
        user.value = null
        return
      }
      const data: MeResponse = await res.json()
      user.value = {
        id: data.user_id,
        name: data.full_name,
        email: data.email,
        clubId: data.club_id,
        roles: data.roles,
        totpEnabled: !!data.totp_enabled,
        totpVerifiedAt: data.totp_verified_at ? new Date(data.totp_verified_at) : null,
      }
    } catch {
      user.value = null
    }
  }

  async function requestMagicLink(email: string): Promise<boolean> {
    loginError.value = null
    try {
      const res = await fetch('/api/v1/auth/magic-link', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email }),
      })
      if (!res.ok) {
        const text = await res.text().catch(() => '')
        loginError.value = text || 'Kunne ikke sende innloggingslenke'
        return false
      }
      return true
    } catch {
      loginError.value = 'Kunne ikke koble til serveren'
      return false
    }
  }

  async function logout() {
    try {
      await fetch('/api/v1/auth/session/logout', {
        method: 'POST',
        credentials: 'include',
      })
    } catch {
      // ignore logout errors
    }
    user.value = null
  }

  function hasRole(role: string): boolean {
    return user.value?.roles.includes(role) ?? false
  }

  const ready = checkSession()

  return {
    user,
    loginError,
    isAuthenticated,
    hasAdminRole,
    canAccessAdmin,
    ready,
    logout,
    hasRole,
    checkSession,
    requestMagicLink,
  }
})
