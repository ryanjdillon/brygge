<script setup lang="ts">
import { ref, computed } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useClubStore } from '@/stores/club'
import { useFeatures } from '@/composables/useFeatures'
import ErrorBoundary from '@/components/ui/ErrorBoundary.vue'
import {
  Users,
  Anchor,
  ListOrdered,
  CalendarDays,
  CalendarCheck,
  FileText,
  DollarSign,
  Megaphone,
  ShoppingBag,
  FolderKanban,
  Ship,
  BrushCleaning,
  MapPin,
  Map,
  Bell,
  ShieldCheck,
  Menu,
  X,
  Calculator,
  BookOpen,
  ClipboardList,
  Landmark,
  Receipt,
  Settings,
  Inbox as InboxIcon,
} from 'lucide-vue-next'

const { t } = useI18n()
const auth = useAuthStore()
const club = useClubStore()
club.ensureLoaded()

const { isEnabled } = useFeatures()
const route = useRoute()

const sidebarOpen = ref(false)

interface NavItem {
  to: string
  icon: typeof Users
  label: string
  roles?: string[]
  feature?: 'bookings' | 'projects' | 'calendar' | 'commerce' | 'communications' | 'accounting'
}

interface NavGroup {
  titleKey?: string
  items: NavItem[]
}

const navGroups = computed<NavGroup[]>(() => {
  const groups: NavGroup[] = [
    {
      items: [
        // Shared inbox at the top of the sidebar (DIL-275). Filtered
        // out when the user holds no board-mailbox role; the per-
        // address gate happens server-side on /mailboxes.
        {
          to: '/admin/inbox',
          icon: InboxIcon,
          label: t('admin.sidebar.inbox'),
          roles: ['chair', 'vice_chair', 'treasurer', 'harbor_master', 'secretary', 'board', 'admin'],
        },
        { to: '/admin/events', icon: CalendarDays, label: t('admin.sidebar.events') },
        { to: '/admin/users', icon: Users, label: t('admin.sidebar.users') },
        { to: '/admin/waiting-list', icon: ListOrdered, label: t('admin.sidebar.waitingList'), roles: ['board', 'admin'] },
        { to: '/admin/pricing', icon: DollarSign, label: t('admin.sidebar.pricing'), roles: ['admin', 'treasurer'] },
      ],
    },
    {
      titleKey: 'admin.groupHarbor',
      items: [
        { to: '/admin/slips', icon: Anchor, label: t('admin.sidebar.slips'), roles: ['board', 'harbor_master', 'admin'] },
        { to: '/admin/harbor-map', icon: Map, label: t('admin.sidebar.harborMap'), roles: ['board', 'harbor_master', 'admin'] },
        { to: '/admin/boats', icon: Ship, label: t('admin.sidebar.boats'), roles: ['board', 'harbor_master', 'admin'] },
        { to: '/admin/bookings', icon: CalendarCheck, label: t('admin.sidebar.bookings'), roles: ['board', 'harbor_master', 'admin'], feature: 'bookings' },
        { to: '/admin/projects', icon: FolderKanban, label: t('admin.sidebar.projects'), feature: 'projects' },
        { to: '/admin/volunteer', icon: BrushCleaning, label: t('volunteer.title'), feature: 'projects' },
      ],
    },
    {
      titleKey: 'admin.groupEconomy',
      items: [
        { to: '/admin/accounting', icon: Calculator, label: t('admin.sidebar.accountingOverview'), roles: ['treasurer', 'board', 'admin'], feature: 'accounting' },
        { to: '/admin/accounting/faktura', icon: Receipt, label: t('admin.sidebar.faktura'), roles: ['treasurer', 'admin'], feature: 'accounting' },
        { to: '/admin/accounting/accounts', icon: ClipboardList, label: t('admin.sidebar.accounts'), roles: ['treasurer', 'board', 'admin'], feature: 'accounting' },
        { to: '/admin/accounting/journal', icon: BookOpen, label: t('admin.sidebar.journal'), roles: ['treasurer', 'board', 'admin'], feature: 'accounting' },
        { to: '/admin/accounting/bank-imports', icon: Landmark, label: t('admin.sidebar.bankImports'), roles: ['treasurer', 'admin'], feature: 'accounting' },
      ],
    },
    {
      titleKey: 'admin.groupShop',
      items: [
        { to: '/admin/products', icon: ShoppingBag, label: t('admin.sidebar.products'), roles: ['board', 'admin'], feature: 'commerce' },
      ],
    },
    {
      titleKey: 'admin.groupArchive',
      items: [
        { to: '/admin/documents', icon: FileText, label: t('admin.sidebar.documents') },
        { to: '/admin/communication', icon: Megaphone, label: t('admin.sidebar.communication'), feature: 'communications' },
      ],
    },
    {
      titleKey: 'admin.groupSite',
      items: [
        { to: '/admin/map', icon: MapPin, label: t('admin.sidebar.mapMarkers') },
        { to: '/admin/notifications', icon: Bell, label: t('notifications.admin.title'), feature: 'communications' },
        { to: '/admin/gdpr', icon: ShieldCheck, label: t('gdpr.admin.title') },
        { to: '/admin/accounting/settings', icon: Settings, label: t('admin.sidebar.settings'), roles: ['treasurer', 'admin'], feature: 'accounting' },
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

function isActive(to: string): boolean {
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
        'fixed inset-y-0 left-0 z-40 flex w-64 transform flex-col border-r border-gray-200 bg-white pt-16 transition-transform lg:static lg:z-auto lg:translate-x-0 lg:pt-0',
        sidebarOpen ? 'translate-x-0' : '-translate-x-full',
      ]"
    >
      <div class="flex items-center justify-between border-b border-gray-200 px-4 py-4 lg:hidden">
        <span class="text-lg font-semibold text-gray-900">{{ t('admin.title') }}</span>
        <button class="text-gray-500 hover:text-gray-700" @click="closeSidebar">
          <X class="h-5 w-5" />
        </button>
      </div>

      <nav class="flex-1 overflow-y-auto px-3 py-4" :aria-label="t('admin.ariaNav')">
        <div v-for="(group, gi) in navGroups" :key="gi" :class="gi > 0 ? 'mt-4' : ''">
          <div
            v-if="group.titleKey"
            class="mb-1 px-3 text-xs font-semibold uppercase tracking-wider text-gray-400"
          >
            {{ t(group.titleKey) }}
          </div>
          <div class="space-y-0.5">
            <RouterLink
              v-for="item in group.items"
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
          </div>
        </div>
      </nav>

    </aside>

    <div class="flex-1">
      <div class="flex items-center border-b border-gray-200 px-4 py-3 lg:hidden">
        <button class="text-gray-500 hover:text-gray-700" :aria-expanded="sidebarOpen" :aria-label="t('nav.ariaMenu')" @click="sidebarOpen = true">
          <Menu class="h-5 w-5" aria-hidden="true" />
        </button>
        <span class="ml-3 text-lg font-semibold text-gray-900">{{ t('admin.title') }}</span>
      </div>

      <main class="p-6 lg:p-8">
        <ErrorBoundary>
          <RouterView />
        </ErrorBoundary>
      </main>
    </div>
  </div>
</template>
