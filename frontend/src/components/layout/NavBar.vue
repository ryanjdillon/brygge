<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useClubStore } from '@/stores/club'
import { LogIn, LogOut, User, Shield, ShieldAlert } from 'lucide-vue-next'
import LanguageSwitcher from '@/components/layout/LanguageSwitcher.vue'

const { t } = useI18n()
const router = useRouter()
const route = useRoute()
const auth = useAuthStore()
const club = useClubStore()
club.ensureLoaded()

// On the landing page the hero photo extends up behind the navbar, so
// the bar itself goes transparent and the controls invert to read on
// the dark image. Every other route keeps the white opaque chrome.
const isHero = computed(() => route.path === '/' || route.path === '')

const navClass = computed(() =>
  isHero.value
    ? 'absolute inset-x-0 top-0 z-30 bg-transparent'
    : 'sticky top-0 z-40 border-b border-slate-200 bg-white/95 backdrop-blur',
)

const brandClass = computed(() =>
  isHero.value
    ? 'flex min-w-0 items-center gap-2 text-white drop-shadow'
    : 'flex min-w-0 items-center gap-2 text-slate-800',
)

// Pill controls — present on every route, but the dark variant is used
// on the hero so they read against the photo. The light variant uses
// slate to match the rest of the (non-hero) chrome on the site.
const pillBase = 'inline-flex items-center gap-1 rounded-full px-3 py-1.5 text-sm font-medium transition'

const portalClass = computed(() =>
  isHero.value
    ? `${pillBase} text-white/90 ring-1 ring-white/30 hover:bg-white/10 hover:text-white`
    : `${pillBase} text-slate-600 hover:bg-slate-100 hover:text-slate-900`,
)
const adminClass = computed(() =>
  isHero.value
    ? `${pillBase} hidden text-white/90 ring-1 ring-white/30 hover:bg-white/10 hover:text-white sm:inline-flex`
    : `${pillBase} hidden text-slate-600 hover:bg-slate-100 hover:text-slate-900 sm:inline-flex`,
)
const enable2faClass = computed(() =>
  isHero.value
    ? `${pillBase} hidden text-amber-200 ring-1 ring-amber-300/50 hover:bg-amber-300/10 sm:inline-flex`
    : `${pillBase} hidden text-amber-700 hover:bg-amber-50 sm:inline-flex`,
)
const logoutClass = computed(() =>
  isHero.value
    ? 'inline-flex items-center rounded-full p-2 text-white/80 ring-1 ring-white/30 hover:bg-white/10 hover:text-white'
    : 'inline-flex items-center rounded-full p-2 text-slate-500 hover:bg-slate-100 hover:text-slate-700',
)
const loginClass = computed(() =>
  isHero.value
    ? `${pillBase} bg-white text-blue-900 shadow hover:bg-blue-50`
    : `${pillBase} bg-blue-600 text-white hover:bg-blue-700`,
)

async function handleLogout() {
  await auth.logout()
  router.push('/')
}
</script>

<template>
  <nav :class="navClass" :aria-label="t('nav.ariaMainNav')">
    <div class="w-full px-4 sm:px-6 lg:px-8">
      <!-- On the landing page the brand is presented as a featured logo
           lower on the page, so the top-left slot is left empty for a
           cleaner hero. justify-end keeps the controls flush right. -->
      <div :class="['flex h-14 items-center gap-3', isHero ? 'justify-end' : 'justify-between']">
        <RouterLink v-if="!isHero" to="/" :class="brandClass">
          <span class="truncate text-base font-semibold sm:text-lg">
            {{ club.name || 'Brygge' }}
          </span>
        </RouterLink>

        <div class="flex flex-none items-center gap-1 sm:gap-2">
          <LanguageSwitcher :theme="isHero ? 'dark' : 'light'" />
          <template v-if="auth.isAuthenticated">
            <RouterLink
              v-if="auth.hasAdminRole && auth.user?.totpEnabled"
              to="/admin"
              :class="adminClass"
            >
              <Shield class="h-4 w-4" />
              <span class="hidden md:inline">{{ t('nav.admin') }}</span>
            </RouterLink>
            <RouterLink
              v-else-if="auth.hasAdminRole && auth.user && !auth.user.totpEnabled"
              to="/portal/security"
              :class="enable2faClass"
              :title="t('nav.enable2faTooltip')"
            >
              <ShieldAlert class="h-4 w-4" />
              <span class="hidden md:inline">{{ t('nav.enable2fa') }}</span>
            </RouterLink>
            <RouterLink to="/portal" :class="portalClass">
              <User class="h-4 w-4" />
              <span class="hidden sm:inline">{{ t('nav.portal') }}</span>
            </RouterLink>
            <button
              type="button"
              :class="logoutClass"
              :aria-label="t('nav.ariaLogout')"
              @click="handleLogout"
            >
              <LogOut class="h-4 w-4" aria-hidden="true" />
            </button>
          </template>
          <template v-else>
            <RouterLink to="/login" :class="loginClass">
              <LogIn class="h-4 w-4" />
              <span class="hidden sm:inline">{{ t('nav.login') }}</span>
            </RouterLink>
          </template>
        </div>
      </div>
    </div>
  </nav>
</template>
