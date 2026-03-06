import { ref, computed } from 'vue'
import { defineStore } from 'pinia'

interface User {
  id: string
  name: string
  email: string
  clubId: string
  roles: string[]
}

interface LoginResponse {
  access_token: string
  refresh_token: string
  token_type: string
}

interface MeResponse {
  user_id: string
  club_id: string
  roles: string[]
}

export const useAuthStore = defineStore('auth', () => {
  const accessToken = ref<string | null>(localStorage.getItem('access_token'))
  const refreshToken = ref<string | null>(localStorage.getItem('refresh_token'))
  const user = ref<User | null>(null)
  const loginError = ref<string | null>(null)

  const isAuthenticated = computed(() => user.value !== null)

  async function fetchMe() {
    if (!accessToken.value) return
    try {
      const res = await fetch('/api/v1/auth/me', {
        headers: { Authorization: `Bearer ${accessToken.value}` },
      })
      if (!res.ok) {
        if (res.status === 401) {
          clearTokens()
        }
        return
      }
      const data: MeResponse = await res.json()
      user.value = {
        id: data.user_id,
        name: '',
        email: '',
        clubId: data.club_id,
        roles: data.roles,
      }
    } catch {
      clearTokens()
    }
  }

  async function login(email: string, password: string, clubSlug = 'default') {
    loginError.value = null
    try {
      const res = await fetch('/api/v1/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email, password, club_slug: clubSlug }),
      })
      if (!res.ok) {
        const text = await res.text().catch(() => '')
        if (res.status === 401) {
          loginError.value = 'Feil e-post eller passord'
        } else {
          loginError.value = text || 'Innlogging feilet'
        }
        return false
      }
      const data: LoginResponse = await res.json()
      accessToken.value = data.access_token
      refreshToken.value = data.refresh_token
      localStorage.setItem('access_token', data.access_token)
      localStorage.setItem('refresh_token', data.refresh_token)
      await fetchMe()
      return true
    } catch {
      loginError.value = 'Kunne ikke koble til serveren'
      return false
    }
  }

  async function logout() {
    if (accessToken.value && refreshToken.value) {
      try {
        await fetch('/api/v1/auth/logout', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            Authorization: `Bearer ${accessToken.value}`,
          },
          body: JSON.stringify({ refresh_token: refreshToken.value }),
        })
      } catch {
        // ignore logout errors
      }
    }
    clearTokens()
  }

  function clearTokens() {
    user.value = null
    accessToken.value = null
    refreshToken.value = null
    localStorage.removeItem('access_token')
    localStorage.removeItem('refresh_token')
  }

  function hasRole(role: string): boolean {
    return user.value?.roles.includes(role) ?? false
  }

  // Restore session on store init
  if (accessToken.value) {
    fetchMe()
  }

  return { user, accessToken, refreshToken, loginError, isAuthenticated, login, logout, fetchMe, hasRole }
})
