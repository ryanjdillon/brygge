<script setup lang="ts">
import { ref, computed } from 'vue'
import { RouterView, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useClubStore } from '@/stores/club'
import { useFeatures } from '@/composables/useFeatures'
import { useNavGate } from '@/composables/useNavGate'
import { useBankUnmatchedCount, usePendingRefundCount } from '@/composables/useBankReconcile'
import ErrorBoundary from '@/components/ui/ErrorBoundary.vue'
import FeedbackWidget from '@/components/ui/FeedbackWidget.vue'
import SidebarNav from '@/components/layout/SidebarNav.vue'
import type { NavGroup } from '@/components/layout/navTypes'
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
  Calculator,
  BookOpen,
  ClipboardList,
  Landmark,
  Receipt,
  Settings,
  Inbox as InboxIcon,
  TerminalSquare,
} from 'lucide-vue-next'

const { t } = useI18n()
const auth = useAuthStore()
const club = useClubStore()
club.ensureLoaded()

const { isEnabled } = useFeatures()
const currentYear = ref(new Date().getFullYear())
const { data: bankUnmatchedCount } = useBankUnmatchedCount(currentYear)
const { data: pendingRefundCount } = usePendingRefundCount()

const sidebarOpen = ref(false)

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
        { to: '/admin/harbor/settings', icon: Settings, label: t('admin.sidebar.harborSettings'), roles: ['board', 'harbor_master', 'admin'] },
        { to: '/admin/motorhome/settings', icon: Settings, label: t('admin.sidebar.motorhomeSettings'), roles: ['board', 'harbor_master', 'admin'] },
      ],
    },
    {
      titleKey: 'admin.groupEconomy',
      items: [
        { to: '/admin/accounting', icon: Calculator, label: t('admin.sidebar.accountingOverview'), roles: ['treasurer', 'board', 'admin'], feature: 'accounting' },
        { to: '/admin/accounting/faktura', icon: Receipt, label: t('admin.sidebar.faktura'), roles: ['treasurer', 'admin'], feature: 'accounting' },
        { to: '/admin/accounting/accounts', icon: ClipboardList, label: t('admin.sidebar.accounts'), roles: ['treasurer', 'board', 'admin'], feature: 'accounting' },
        { to: '/admin/accounting/journal', icon: BookOpen, label: t('admin.sidebar.journal'), roles: ['treasurer', 'board', 'admin'], feature: 'accounting' },
        { to: '/admin/accounting/bank-imports', icon: Landmark, label: t('admin.sidebar.bankImports'), roles: ['treasurer', 'admin'], feature: 'accounting', badge: (bankUnmatchedCount.value ?? 0) + (pendingRefundCount.value ?? 0) },
        { to: '/admin/economy/settings', icon: Settings, label: t('admin.sidebar.economySettings'), roles: ['treasurer', 'admin'], feature: 'accounting' },
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
        { to: '/admin/settings/general', icon: Settings, label: t('admin.sidebar.generalSettings'), roles: ['board', 'admin'] },
        { to: '/admin/settings/security', icon: ShieldCheck, label: t('admin.sidebar.securitySettings'), roles: ['board', 'admin'] },
        { to: '/admin/map', icon: MapPin, label: t('admin.sidebar.mapMarkers') },
        { to: '/admin/notifications', icon: Bell, label: t('notifications.admin.title'), feature: 'communications' },
        { to: '/admin/gdpr', icon: ShieldCheck, label: t('gdpr.admin.title') },
        { to: '/admin/settings/site', icon: Settings, label: t('admin.sidebar.siteSettings'), roles: ['board', 'admin'] },
        { to: '/admin/dev/query', icon: TerminalSquare, label: t('admin.sidebar.devQuery'), roles: ['admin'] },
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

function closeSidebar() {
  sidebarOpen.value = false
}

// Economy sidebar items gate on fresh-TOTP (10-min window). Anything
// under /admin/accounting or /admin/economy hits this. See DIL-369.
const router = useRouter()
const { gateToFresh } = useNavGate()

function requiresFreshTotp(path: string): boolean {
  return path.startsWith('/admin/accounting')
    || path.startsWith('/admin/economy')
    || path.startsWith('/admin/dev')
}

async function handleNavClick(e: MouseEvent, to: string) {
  if (!requiresFreshTotp(to)) {
    closeSidebar()
    return
  }
  e.preventDefault()
  const ok = await gateToFresh(to)
  closeSidebar()
  if (ok) router.push(to)
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
        :title="t('admin.sidebarTitle')"
        :groups="navGroups"
        :ariaLabel="t('admin.ariaNav')"
        @navigate="handleNavClick"
        @close="closeSidebar"
      />
    </aside>

    <div class="flex-1">
      <div class="flex items-center border-b border-gray-200 px-4 py-3 lg:hidden">
        <button class="text-gray-500 hover:text-gray-700" :aria-expanded="sidebarOpen" :aria-label="t('nav.ariaMenu')" @click="sidebarOpen = true">
          <Menu class="h-5 w-5" aria-hidden="true" />
        </button>
        <span class="ml-3 text-lg font-semibold text-gray-900">{{ t('admin.sidebarTitle') }}</span>
      </div>

      <main class="p-6 lg:p-8">
        <ErrorBoundary>
          <RouterView />
        </ErrorBoundary>
      </main>
    </div>
  </div>
  <FeedbackWidget />
</template>
