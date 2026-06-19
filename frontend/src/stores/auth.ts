import { ref, computed, watch } from 'vue'
import { defineStore } from 'pinia'
import { useTotpGateStore } from '@/stores/totpGate'
import { setLocale, hasExplicitLocale } from '@/i18n'

interface User {
  id: string
  firstName: string
  lastName: string
  name: string // computed convenience: `${firstName} ${lastName}` (kept while DIL-230 is pending)
  email: string
  clubId: string
  roles: string[]
  totpEnabled: boolean
  totpVerifiedAt: Date | null
  preferredLanguage: string | null
  clubDefaultLanguage: string
}

interface MeResponse {
  user_id: string
  club_id: string
  roles: string[]
  first_name: string
  last_name: string
  full_name: string
  email: string
  totp_enabled: boolean
  totp_verified_at?: string | null
  preferred_language?: string | null
  club_default_language?: string
  fresh_totp_window_ms?: number
}

// Mirrors the 12-hour step-up window enforced by the backend's
// RequireAdminTOTP middleware. Used purely for soft UX (hiding admin
// nav links when the gate would 403 anyway).
const TOTP_FRESH_MS = 12 * 60 * 60 * 1000

// Per-action freshness window enforced by RequireFreshTOTP on mutating
// admin endpoints. The default tracks the backend's compiled default
// but is overridden at session-load time by the server-configured
// value surfaced on /session/me — that's the source of truth.
export const totpActionFreshMs = ref(10 * 60 * 1000)

// Lead time for the "still working?" warning. Capped at 3 minutes so
// users have time to react before the window lapses.
export const totpActionWarnMs = computed(() =>
  Math.min(3 * 60 * 1000, Math.floor(totpActionFreshMs.value / 10)),
)

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

  const hasFreshTotp = computed(() => {
    if (!user.value?.totpEnabled || !user.value.totpVerifiedAt) return false
    return Date.now() - user.value.totpVerifiedAt.getTime() < totpActionFreshMs.value
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
      if (typeof data.fresh_totp_window_ms === 'number' && data.fresh_totp_window_ms > 0) {
        totpActionFreshMs.value = data.fresh_totp_window_ms
      }
      const first = data.first_name ?? ''
      const last = data.last_name ?? ''
      const clubDefaultLanguage = data.club_default_language || 'nb'
      const preferredLanguage = data.preferred_language ?? null
      user.value = {
        id: data.user_id,
        firstName: first,
        lastName: last,
        name: data.full_name || `${first} ${last}`.trim(),
        email: data.email,
        clubId: data.club_id,
        roles: data.roles,
        totpEnabled: !!data.totp_enabled,
        totpVerifiedAt: data.totp_verified_at ? new Date(data.totp_verified_at) : null,
        preferredLanguage,
        clubDefaultLanguage,
      }
      // Locale precedence: explicit member preference wins (persisted so
      // it survives logout); otherwise, if the user has made no explicit
      // in-app choice, follow the club default without persisting so
      // they keep tracking it if it later changes.
      if (preferredLanguage) {
        setLocale(preferredLanguage, { persist: true })
      } else if (!hasExplicitLocale()) {
        setLocale(clubDefaultLanguage)
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

  // Schedule a "still working?" warning ~1 minute before the fresh-TOTP
  // window lapses. Re-arms whenever totpVerifiedAt advances (i.e. on a
  // successful step-up).
  let warnTimer: ReturnType<typeof setTimeout> | null = null
  watch(
    () => user.value?.totpVerifiedAt,
    (verifiedAt) => {
      if (warnTimer) {
        clearTimeout(warnTimer)
        warnTimer = null
      }
      if (!verifiedAt || !user.value?.totpEnabled) return
      const warnAt = verifiedAt.getTime() + totpActionFreshMs.value - totpActionWarnMs.value
      const delay = warnAt - Date.now()
      if (delay <= 0) return
      warnTimer = setTimeout(() => {
        useTotpGateStore().showExpiringWarning()
      }, delay)
    },
    { immediate: true },
  )

  const ready = checkSession()

  return {
    user,
    loginError,
    isAuthenticated,
    hasAdminRole,
    canAccessAdmin,
    hasFreshTotp,
    ready,
    logout,
    hasRole,
    checkSession,
    requestMagicLink,
  }
})
