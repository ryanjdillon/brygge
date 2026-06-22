<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { RouterView, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useClubStore } from '@/stores/club'
import { useBreadcrumbStore } from '@/stores/breadcrumb'
import { useFeatures } from '@/composables/useFeatures'
import ErrorBoundary from '@/components/ui/ErrorBoundary.vue'
import FeedbackWidget from '@/components/ui/FeedbackWidget.vue'
import SidebarNav from '@/components/layout/SidebarNav.vue'
import type { NavGroup } from '@/components/layout/navTypes'
import {
  LayoutDashboard,
  User,
  Ship,
  Users,
  Anchor,
  CalendarDays,
  BrushCleaning,
  Bell,
  ShieldCheck,
  Receipt,
  Map,
  Menu,
} from 'lucide-vue-next'

const { t } = useI18n()
const auth = useAuthStore()
const club = useClubStore()
const route = useRoute()
const breadcrumb = useBreadcrumbStore()
const { isEnabled } = useFeatures()
club.ensureLoaded()

const sidebarOpen = ref(false)

// Themed groups mirroring the admin sidebar (Harbour / Economy /
// Community / Account) so the two portals feel consistent.
const navGroups = computed<NavGroup[]>(() => {
  const groups: NavGroup[] = [
    {
      items: [
        { to: '/portal', icon: LayoutDashboard, label: t('portal.sidebar.dashboard') },
      ],
    },
    {
      titleKey: 'portal.groupHarbor',
      items: [
        { to: '/portal/boats', icon: Ship, label: t('portal.sidebar.myBoats') },
        { to: '/portal/harbor-map', icon: Map, label: t('portal.sidebar.harborMap') },
        { to: '/portal/slip', icon: Anchor, label: t('portal.sidebar.slip'), roles: ['slip_holder'] },
        { to: '/portal/bookings', icon: CalendarDays, label: t('portal.sidebar.bookings'), feature: 'bookings' },
        { to: '/portal/volunteer', icon: BrushCleaning, label: t('volunteer.title'), feature: 'projects' },
      ],
    },
    {
      titleKey: 'portal.groupEconomy',
      items: [
        { to: '/portal/invoices', icon: Receipt, label: t('portal.sidebar.invoices'), feature: 'accounting' },
      ],
    },
    {
      titleKey: 'portal.groupCommunity',
      items: [
        { to: '/portal/directory', icon: Users, label: t('portal.sidebar.directory'), roles: ['member', 'slip_holder', 'board', 'admin'] },
      ],
    },
    {
      titleKey: 'portal.groupAccount',
      items: [
        { to: '/portal/profile', icon: User, label: t('portal.sidebar.profile') },
        { to: '/portal/notifications', icon: Bell, label: t('notifications.title') },
        { to: '/portal/security', icon: ShieldCheck, label: t('security.title') },
        { to: '/portal/privacy', icon: ShieldCheck, label: t('gdpr.title') },
      ],
    },
  ]

  return groups
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => {
        if (item.feature && !isEnabled(item.feature)) return false
        if (!item.roles) return true
        return item.roles.some((role) => auth.hasRole(role))
      }),
    }))
    .filter((group) => group.items.length > 0)
})

// Feed the top-bar breadcrumb: the localized label of the active nav item
// (most-specific path match), so NavBar can show "Brukarportal › <page>".
const activePageLabel = computed(() => {
  const path = route.path
  let best: { to: string; label: string } | null = null
  for (const group of navGroups.value) {
    for (const item of group.items) {
      const match =
        item.to === '/portal' ? path === '/portal' || path === '/portal/' : path.startsWith(item.to)
      if (match && (!best || item.to.length > best.to.length)) best = item
    }
  }
  return best?.label ?? ''
})
watch(activePageLabel, (label) => breadcrumb.setPage(label), { immediate: true })

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
        'fixed inset-y-0 left-0 z-40 flex w-64 transform flex-col border-r border-gray-200 bg-white pt-16 transition-transform lg:static lg:z-auto lg:translate-x-0 lg:pt-0',
        sidebarOpen ? 'translate-x-0' : '-translate-x-full',
      ]"
    >
      <SidebarNav
        :title="t('portal.sidebarTitle')"
        :groups="navGroups"
        :ariaLabel="t('portal.ariaNav')"
        @navigate="closeSidebar"
        @close="closeSidebar"
      />
    </aside>

    <div class="flex-1">
      <div class="flex items-center border-b border-gray-200 px-4 py-3 lg:hidden">
        <button class="text-gray-500 hover:text-gray-700" :aria-expanded="sidebarOpen" :aria-label="t('nav.ariaMenu')" @click="sidebarOpen = true">
          <Menu class="h-5 w-5" aria-hidden="true" />
        </button>
        <span class="ml-3 text-lg font-semibold text-gray-900">{{ t('portal.sidebarTitle') }}</span>
      </div>

      <main class="px-6 pb-6 pt-8 lg:px-8 lg:pb-8 lg:pt-10">
        <ErrorBoundary>
          <RouterView />
        </ErrorBoundary>
      </main>
    </div>
  </div>
  <FeedbackWidget />
</template>
