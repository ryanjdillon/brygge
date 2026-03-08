<script setup lang="ts">
import { ref, computed } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useFeatures } from '@/composables/useFeatures'
import {
  LayoutDashboard,
  User,
  Ship,
  Users,
  FileText,
  ListOrdered,
  Anchor,
  CalendarDays,
  MessageCircle,
  Lightbulb,
  HardHat,
  Bell,
  ShieldCheck,
  Menu,
  X,
} from 'lucide-vue-next'

const { t } = useI18n()
const auth = useAuthStore()
const { isEnabled } = useFeatures()
const route = useRoute()

const sidebarOpen = ref(false)

interface NavItem {
  to: string
  icon: typeof LayoutDashboard
  label: string
  roles?: string[]
  feature?: 'bookings' | 'projects' | 'calendar' | 'commerce' | 'communications'
}

const navItems = computed<NavItem[]>(() => {
  const items: NavItem[] = [
    { to: '/portal', icon: LayoutDashboard, label: t('portal.sidebar.dashboard') },
    { to: '/portal/profile', icon: User, label: t('portal.sidebar.profile') },
    { to: '/portal/boats', icon: Ship, label: t('portal.sidebar.boats') },
    { to: '/portal/directory', icon: Users, label: t('portal.sidebar.directory'), roles: ['member', 'slip_owner', 'styre', 'admin'] },
    { to: '/portal/documents', icon: FileText, label: t('portal.sidebar.documents') },
    { to: '/portal/waiting-list', icon: ListOrdered, label: t('portal.sidebar.waitingList') },
    { to: '/portal/slip', icon: Anchor, label: t('portal.sidebar.slip'), roles: ['slip_owner'] },
    { to: '/portal/bookings', icon: CalendarDays, label: t('portal.sidebar.bookings'), feature: 'bookings' },
    { to: '/portal/dugnad', icon: HardHat, label: t('dugnad.title'), feature: 'projects' },
    { to: '/portal/notifications', icon: Bell, label: t('notifications.title'), feature: 'communications' },
    { to: '/portal/privacy', icon: ShieldCheck, label: t('gdpr.title') },
    { to: '/portal/feature-requests', icon: Lightbulb, label: t('portal.sidebar.featureRequests'), roles: ['member', 'slip_owner', 'styre', 'admin'] },
    { to: '/portal/forum', icon: MessageCircle, label: t('portal.sidebar.forum'), roles: ['member', 'slip_owner', 'styre', 'admin'], feature: 'communications' },
  ]

  return items.filter((item) => {
    if (item.feature && !isEnabled(item.feature)) return false
    if (!item.roles) return true
    return item.roles.some((role) => auth.hasRole(role))
  })
})

function isActive(to: string): boolean {
  if (to === '/portal') return route.path === '/portal' || route.path === '/portal/'
  return route.path.startsWith(to)
}

function closeSidebar() {
  sidebarOpen.value = false
}
</script>

<template>
  <div class="flex min-h-[calc(100vh-4rem)]">
    <div
      v-if="sidebarOpen"
      class="fixed inset-0 z-30 bg-black/50 lg:hidden"
      @click="closeSidebar"
    />

    <aside
      :class="[
        'fixed inset-y-0 left-0 z-40 w-64 transform border-r border-gray-200 bg-white pt-16 transition-transform lg:static lg:z-auto lg:translate-x-0 lg:pt-0',
        sidebarOpen ? 'translate-x-0' : '-translate-x-full',
      ]"
    >
      <div class="flex items-center justify-between border-b border-gray-200 px-4 py-4 lg:hidden">
        <span class="text-lg font-semibold text-gray-900">{{ t('portal.title') }}</span>
        <button class="text-gray-500 hover:text-gray-700" @click="closeSidebar">
          <X class="h-5 w-5" />
        </button>
      </div>

      <nav class="space-y-1 px-3 py-4" :aria-label="t('portal.ariaNav')">
        <RouterLink
          v-for="item in navItems"
          :key="item.to"
          :to="item.to"
          :class="[
            'flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition',
            isActive(item.to)
              ? 'bg-blue-50 text-blue-700'
              : 'text-gray-700 hover:bg-gray-100 hover:text-gray-900',
          ]"
          @click="closeSidebar"
        >
          <component
            :is="item.icon"
            :class="['h-5 w-5', isActive(item.to) ? 'text-blue-600' : 'text-gray-400']"
          />
          {{ item.label }}
        </RouterLink>
      </nav>
    </aside>

    <div class="flex-1">
      <div class="flex items-center border-b border-gray-200 px-4 py-3 lg:hidden">
        <button class="text-gray-500 hover:text-gray-700" :aria-expanded="sidebarOpen" :aria-label="t('nav.ariaMenu')" @click="sidebarOpen = true">
          <Menu class="h-5 w-5" aria-hidden="true" />
        </button>
        <span class="ml-3 text-lg font-semibold text-gray-900">{{ t('portal.title') }}</span>
      </div>

      <main class="p-6 lg:p-8">
        <RouterView />
      </main>
    </div>
  </div>
</template>
