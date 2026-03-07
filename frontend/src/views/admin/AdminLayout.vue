<script setup lang="ts">
import { ref, computed } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import {
  Users,
  Anchor,
  ListOrdered,
  CalendarDays,
  CalendarCheck,
  FileText,
  DollarSign,
  Megaphone,
  Banknote,
  ShoppingBag,
  FolderKanban,
  Ship,
  HardHat,
  Menu,
  X,
} from 'lucide-vue-next'

const { t } = useI18n()
const auth = useAuthStore()
const route = useRoute()

const sidebarOpen = ref(false)

interface NavItem {
  to: string
  icon: typeof Users
  label: string
  roles?: string[]
}

interface NavGroup {
  title?: string
  items: NavItem[]
}

const navGroups = computed<NavGroup[]>(() => {
  const groups: NavGroup[] = [
    {
      items: [
        { to: '/admin/events', icon: CalendarDays, label: t('admin.sidebar.events') },
        { to: '/admin/users', icon: Users, label: t('admin.sidebar.users') },
        { to: '/admin/waiting-list', icon: ListOrdered, label: t('admin.sidebar.waitingList'), roles: ['styre', 'admin'] },
      ],
    },
    {
      title: 'Havn',
      items: [
        { to: '/admin/slips', icon: Anchor, label: t('admin.sidebar.slips'), roles: ['styre', 'harbour_master', 'admin'] },
        { to: '/admin/boats', icon: Ship, label: t('admin.sidebar.boats'), roles: ['styre', 'harbour_master', 'admin'] },
        { to: '/admin/bookings', icon: CalendarCheck, label: t('admin.sidebar.bookings'), roles: ['styre', 'harbour_master', 'admin'] },
        { to: '/admin/projects', icon: FolderKanban, label: t('admin.sidebar.projects') },
        { to: '/admin/dugnad', icon: HardHat, label: t('dugnad.title') },
      ],
    },
    {
      title: 'Økonomi',
      items: [
        { to: '/admin/pricing', icon: DollarSign, label: t('admin.sidebar.pricing'), roles: ['admin', 'treasurer'] },
        { to: '/admin/products', icon: ShoppingBag, label: t('admin.sidebar.products'), roles: ['styre', 'admin'] },
        { to: '/admin/financials', icon: Banknote, label: t('admin.sidebar.financials'), roles: ['treasurer', 'styre', 'admin'] },
      ],
    },
    {
      title: 'Arkiv og info',
      items: [
        { to: '/admin/documents', icon: FileText, label: t('admin.sidebar.documents') },
        { to: '/admin/communication', icon: Megaphone, label: t('admin.sidebar.communication') },
      ],
    },
  ]

  return groups
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => {
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
        'fixed inset-y-0 left-0 z-40 w-64 transform border-r border-gray-200 bg-white pt-16 transition-transform lg:static lg:z-auto lg:translate-x-0 lg:pt-0',
        sidebarOpen ? 'translate-x-0' : '-translate-x-full',
      ]"
    >
      <div class="flex items-center justify-between border-b border-gray-200 px-4 py-4 lg:hidden">
        <span class="text-lg font-semibold text-gray-900">{{ t('admin.title') }}</span>
        <button class="text-gray-500 hover:text-gray-700" @click="closeSidebar">
          <X class="h-5 w-5" />
        </button>
      </div>

      <nav class="px-3 py-4">
        <div v-for="(group, gi) in navGroups" :key="gi" :class="gi > 0 ? 'mt-4' : ''">
          <div
            v-if="group.title"
            class="mb-1 px-3 text-xs font-semibold uppercase tracking-wider text-gray-400"
          >
            {{ group.title }}
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
        <button class="text-gray-500 hover:text-gray-700" @click="sidebarOpen = true">
          <Menu class="h-5 w-5" />
        </button>
        <span class="ml-3 text-lg font-semibold text-gray-900">{{ t('admin.title') }}</span>
      </div>

      <main class="p-6 lg:p-8">
        <RouterView />
      </main>
    </div>
  </div>
</template>
