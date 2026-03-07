<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { Menu, X, LogIn, LogOut, User, Shield, ChevronDown } from 'lucide-vue-next'
import LanguageSwitcher from '@/components/layout/LanguageSwitcher.vue'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()
const mobileOpen = ref(false)
const clubOpen = ref(false)
const clubDropdownRef = ref<HTMLElement>()

async function handleLogout() {
  await auth.logout()
  router.push('/')
}

const navLinks = [
  { to: '/', label: 'nav.home' },
  { to: '/harbour', label: 'nav.harbour' },
  { to: '/bobil', label: 'nav.bobil' },
  { to: '/weather', label: 'nav.weather' },
  { to: '/merchandise', label: 'nav.merchandise' },
  { to: '/contact', label: 'nav.contact' },
]

const clubLinks = [
  { to: '/calendar', label: 'nav.calendar' },
  { to: '/pricing', label: 'nav.pricing' },
  { to: '/join', label: 'nav.join' },
  { to: '/history', label: 'nav.history' },
]

function handleClickOutside(event: MouseEvent) {
  if (clubDropdownRef.value && !clubDropdownRef.value.contains(event.target as Node)) {
    clubOpen.value = false
  }
}

onMounted(() => document.addEventListener('click', handleClickOutside))
onUnmounted(() => document.removeEventListener('click', handleClickOutside))
</script>

<template>
  <nav class="sticky top-0 z-50 bg-white shadow-sm" aria-label="Hovednavigasjon">
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

          <!-- Club dropdown -->
          <div ref="clubDropdownRef" class="relative">
            <button
              class="flex items-center gap-1 rounded-md px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-100 hover:text-blue-900"
              @click.stop="clubOpen = !clubOpen"
            >
              {{ t('nav.club') }}
              <ChevronDown
                class="h-3 w-3 transition-transform"
                :class="{ 'rotate-180': clubOpen }"
                aria-hidden="true"
              />
            </button>
            <div
              v-if="clubOpen"
              class="absolute left-0 top-full z-50 mt-1 w-48 rounded-md bg-white py-1 shadow-lg ring-1 ring-black/5"
            >
              <RouterLink
                v-for="link in clubLinks"
                :key="link.to"
                :to="link.to"
                class="block px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 hover:text-blue-900"
                @click="clubOpen = false"
              >
                {{ t(link.label) }}
              </RouterLink>
            </div>
          </div>
        </div>

        <div class="hidden items-center gap-2 md:flex">
          <LanguageSwitcher />
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
              aria-label="Logg ut"
              @click="handleLogout"
            >
              <LogOut class="h-4 w-4" aria-hidden="true" />
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
          :aria-expanded="mobileOpen"
          aria-label="Meny"
          @click="mobileOpen = !mobileOpen"
        >
          <X v-if="mobileOpen" class="h-6 w-6" aria-hidden="true" />
          <Menu v-else class="h-6 w-6" aria-hidden="true" />
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

        <div class="border-t border-gray-100 pt-1">
          <span class="block px-3 py-1 text-xs font-semibold uppercase tracking-wider text-gray-400">
            {{ t('nav.club') }}
          </span>
          <RouterLink
            v-for="link in clubLinks"
            :key="link.to"
            :to="link.to"
            class="block rounded-md px-3 py-2 text-base font-medium text-gray-700 hover:bg-gray-100"
            @click="mobileOpen = false"
          >
            {{ t(link.label) }}
          </RouterLink>
        </div>

        <div class="border-t border-gray-100 pt-2 px-3">
          <LanguageSwitcher />
        </div>

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
