import { ref, computed } from 'vue'
import { defineStore } from 'pinia'

interface User {
  id: string
  name: string
  email: string
  clubId: string
  roles: string[]
}

interface MeResponse {
  user_id: string
  club_id: string
  roles: string[]
  full_name: string
  email: string
}

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const loginError = ref<string | null>(null)

  const isAuthenticated = computed(() => user.value !== null)

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

  return { user, loginError, isAuthenticated, ready, logout, hasRole, checkSession, requestMagicLink }
})
