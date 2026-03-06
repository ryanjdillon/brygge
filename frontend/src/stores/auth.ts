import { ref, computed } from 'vue'
import { defineStore } from 'pinia'

interface User {
  id: string
  name: string
  email: string
  roles: string[]
}

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)

  const isAuthenticated = computed(() => user.value !== null)

  async function login(_email: string, _password: string) {
    // TODO: implement login API call
  }

  async function logout() {
    // TODO: implement logout API call
    user.value = null
  }

  function hasRole(role: string): boolean {
    return user.value?.roles.includes(role) ?? false
  }

  return { user, isAuthenticated, login, logout, hasRole }
})
