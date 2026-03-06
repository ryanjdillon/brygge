<script setup lang="ts">
import { ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useRouter } from 'vue-router'
import { Menu, X, LogIn, LogOut, User, Shield } from 'lucide-vue-next'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()
const mobileOpen = ref(false)

async function handleLogout() {
  await auth.logout()
  router.push('/')
}

const navLinks = [
  { to: '/', label: 'nav.home' },
  { to: '/calendar', label: 'nav.calendar' },
  { to: '/weather', label: 'nav.weather' },
  { to: '/directions', label: 'nav.directions' },
  { to: '/contact', label: 'nav.contact' },
  { to: '/pricing', label: 'nav.pricing' },
  { to: '/merchandise', label: 'nav.merchandise' },
  { to: '/join', label: 'nav.join' },
]
</script>

<template>
  <nav class="sticky top-0 z-50 bg-white shadow-sm">
    <div class="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
      <div class="flex h-16 items-center justify-between">
        <RouterLink to="/" class="flex items-center gap-2 text-xl font-bold text-blue-900">
          Brygge
        </RouterLink>

        <div class="hidden items-center gap-1 md:flex">
          <RouterLink
            v-for="link in navLinks"
            :key="link.to"
            :to="link.to"
            class="rounded-md px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-100 hover:text-blue-900"
          >
            {{ t(link.label) }}
          </RouterLink>
        </div>

        <div class="hidden items-center gap-2 md:flex">
          <template v-if="auth.isAuthenticated">
            <RouterLink
              v-if="auth.hasRole('admin') || auth.hasRole('styre')"
              to="/admin"
              class="rounded-md px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-100"
            >
              <span class="flex items-center gap-1">
                <Shield class="h-4 w-4" />
                {{ t('nav.admin') }}
              </span>
            </RouterLink>
            <RouterLink
              to="/portal"
              class="rounded-md px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-100"
            >
              <span class="flex items-center gap-1">
                <User class="h-4 w-4" />
                {{ t('nav.portal') }}
              </span>
            </RouterLink>
            <button
              class="inline-flex items-center gap-1 rounded-md px-3 py-2 text-sm font-medium text-gray-500 hover:bg-gray-100 hover:text-gray-700"
              @click="handleLogout"
            >
              <LogOut class="h-4 w-4" />
            </button>
          </template>
          <template v-else>
            <RouterLink
              to="/login"
              class="inline-flex items-center gap-1 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
            >
              <LogIn class="h-4 w-4" />
              {{ t('nav.login') }}
            </RouterLink>
          </template>
        </div>

        <button
          class="inline-flex items-center justify-center rounded-md p-2 text-gray-700 hover:bg-gray-100 md:hidden"
          @click="mobileOpen = !mobileOpen"
        >
          <X v-if="mobileOpen" class="h-6 w-6" />
          <Menu v-else class="h-6 w-6" />
        </button>
      </div>
    </div>

    <div v-if="mobileOpen" class="border-t border-gray-200 md:hidden">
      <div class="space-y-1 px-4 pb-3 pt-2">
        <RouterLink
          v-for="link in navLinks"
          :key="link.to"
          :to="link.to"
          class="block rounded-md px-3 py-2 text-base font-medium text-gray-700 hover:bg-gray-100"
          @click="mobileOpen = false"
        >
          {{ t(link.label) }}
        </RouterLink>
        <div class="border-t border-gray-200 pt-2">
          <template v-if="auth.isAuthenticated">
            <RouterLink
              v-if="auth.hasRole('admin') || auth.hasRole('styre')"
              to="/admin"
              class="block rounded-md px-3 py-2 text-base font-medium text-gray-700 hover:bg-gray-100"
              @click="mobileOpen = false"
            >
              {{ t('nav.admin') }}
            </RouterLink>
            <RouterLink
              to="/portal"
              class="block rounded-md px-3 py-2 text-base font-medium text-gray-700 hover:bg-gray-100"
              @click="mobileOpen = false"
            >
              {{ t('nav.portal') }}
            </RouterLink>
            <button
              class="block w-full rounded-md px-3 py-2 text-left text-base font-medium text-gray-500 hover:bg-gray-100"
              @click="handleLogout(); mobileOpen = false"
            >
              {{ t('nav.login') === 'Logg inn' ? 'Logg ut' : 'Log out' }}
            </button>
          </template>
          <template v-else>
            <RouterLink
              to="/login"
              class="block rounded-md px-3 py-2 text-base font-medium text-blue-600 hover:bg-gray-100"
              @click="mobileOpen = false"
            >
              {{ t('nav.login') }}
            </RouterLink>
          </template>
        </div>
      </div>
    </div>
  </nav>
</template>
