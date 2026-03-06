import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'

vi.unmock('vue-i18n')
vi.unmock('@tanstack/vue-query')

describe('auth store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
  })

  it('initial state is unauthenticated', () => {
    const auth = useAuthStore()
    expect(auth.isAuthenticated).toBe(false)
    expect(auth.user).toBeNull()
    expect(auth.accessToken).toBeNull()
  })

  it('login sets user and isAuthenticated', () => {
    const auth = useAuthStore()
    auth.user = { id: '1', name: 'Test User', email: 'test@example.com', roles: ['member'] }
    auth.accessToken = 'test-token'

    expect(auth.isAuthenticated).toBe(true)
    expect(auth.user?.name).toBe('Test User')
  })

  it('logout clears state', async () => {
    const auth = useAuthStore()
    auth.user = { id: '1', name: 'Test', email: 'test@example.com', roles: ['member'] }
    auth.accessToken = 'test-token'

    await auth.logout()

    expect(auth.user).toBeNull()
    expect(auth.accessToken).toBeNull()
    expect(auth.isAuthenticated).toBe(false)
  })

  it('hasRole returns true for matching role', () => {
    const auth = useAuthStore()
    auth.user = { id: '1', name: 'Test', email: 'test@example.com', roles: ['member', 'admin'] }

    expect(auth.hasRole('admin')).toBe(true)
    expect(auth.hasRole('member')).toBe(true)
  })

  it('hasRole returns false for non-matching role', () => {
    const auth = useAuthStore()
    auth.user = { id: '1', name: 'Test', email: 'test@example.com', roles: ['member'] }

    expect(auth.hasRole('admin')).toBe(false)
    expect(auth.hasRole('styre')).toBe(false)
  })

  it('hasRole returns false when no user', () => {
    const auth = useAuthStore()
    expect(auth.hasRole('member')).toBe(false)
  })

  it('accessToken is persisted via localStorage', () => {
    localStorage.setItem('access_token', 'persisted-token')
    const auth = useAuthStore()
    expect(auth.accessToken).toBe('persisted-token')
  })

  it('logout removes token from localStorage', async () => {
    localStorage.setItem('access_token', 'token-to-remove')
    const auth = useAuthStore()
    await auth.logout()
    expect(localStorage.getItem('access_token')).toBeNull()
  })
})
