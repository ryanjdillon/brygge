<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useQuery } from '@tanstack/vue-query'
import { useAuthStore } from '@/stores/auth'
import { useApi } from '@/composables/useApi'
import {
  User,
  Ship,
  FileText,
  Users,
  ListOrdered,
  Anchor,
  CalendarDays,
} from 'lucide-vue-next'

const { t } = useI18n()
const auth = useAuthStore()
const { fetchApi } = useApi()

import type { components } from '@/types/api'

type DashboardData = components['schemas']['DashboardResponse']

const { data: dashboard, isLoading } = useQuery({
  queryKey: ['portal', 'dashboard'],
  queryFn: () => fetchApi<DashboardData>('/api/v1/members/me/dashboard'),
})

const userName = computed(() => auth.user?.name ?? '')

const displayRole = computed(() => {
  const roles = auth.user?.roles ?? []
  if (roles.includes('admin')) return t('portal.dashboard.role.admin')
  if (roles.includes('styre')) return t('portal.dashboard.role.styre')
  if (roles.includes('slip_owner')) return t('portal.dashboard.role.slip_owner')
  if (roles.includes('member')) return t('portal.dashboard.role.member')
  if (roles.includes('applicant')) return t('portal.dashboard.role.applicant')
  return ''
})

const quickLinks = computed(() => {
  const links = [
    { to: '/portal/profile', icon: User, label: t('portal.sidebar.profile') },
    { to: '/portal/boats', icon: Ship, label: t('portal.sidebar.boats') },
    { to: '/portal/documents', icon: FileText, label: t('portal.sidebar.documents') },
    { to: '/portal/directory', icon: Users, label: t('portal.sidebar.directory') },
  ]
  if (auth.hasRole('applicant') && !auth.hasRole('member')) {
    links.push({ to: '/portal/waiting-list', icon: ListOrdered, label: t('portal.sidebar.waitingList') })
  }
  if (auth.hasRole('slip_owner')) {
    links.push({ to: '/portal/slip', icon: Anchor, label: t('portal.sidebar.slip') })
  }
  links.push({ to: '/portal/bookings', icon: CalendarDays, label: t('portal.sidebar.bookings') })
  return links
})
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">
      {{ t('portal.dashboard.welcome', { name: userName }) }}
    </h1>

    <div v-if="isLoading" class="mt-6 text-gray-500">
      {{ t('common.loading') }}...
    </div>

    <div v-else class="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
      <div class="rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
        <p class="text-sm font-medium text-gray-500">{{ t('portal.dashboard.membershipStatus') }}</p>
        <p class="mt-1 text-lg font-semibold text-gray-900">{{ displayRole }}</p>
      </div>

      <div
        v-if="dashboard?.queuePosition"
        class="rounded-lg border border-gray-200 bg-white p-5 shadow-sm"
      >
        <p class="text-sm font-medium text-gray-500">{{ t('portal.dashboard.queuePosition') }}</p>
        <p class="mt-1 text-lg font-semibold text-gray-900">
          {{ t('portal.waitingList.positionOf', { position: dashboard.queuePosition, total: dashboard.queueTotal }) }}
        </p>
      </div>

      <div class="rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
        <p class="text-sm font-medium text-gray-500">{{ t('portal.dashboard.slipInfo') }}</p>
        <p class="mt-1 text-lg font-semibold text-gray-900">
          {{ dashboard?.slip ? `#${dashboard.slip.number} — ${dashboard.slip.location}` : t('portal.dashboard.noSlip') }}
        </p>
      </div>

      <div class="rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
        <p class="text-sm font-medium text-gray-500">{{ t('portal.dashboard.upcomingBookings') }}</p>
        <p class="mt-1 text-lg font-semibold text-gray-900">
          {{ dashboard?.upcomingBookingsCount
            ? t('portal.dashboard.bookingsCount', { count: dashboard.upcomingBookingsCount }, dashboard.upcomingBookingsCount)
            : t('portal.dashboard.noBookings') }}
        </p>
      </div>
    </div>

    <div class="mt-10">
      <h2 class="text-lg font-semibold text-gray-900">{{ t('portal.dashboard.quickLinks') }}</h2>
      <div class="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <RouterLink
          v-for="link in quickLinks"
          :key="link.to"
          :to="link.to"
          class="group flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-4 transition hover:border-blue-300 hover:shadow-sm"
        >
          <component :is="link.icon" class="h-5 w-5 text-blue-600 group-hover:text-blue-700" />
          <span class="text-sm font-medium text-gray-700 group-hover:text-gray-900">{{ link.label }}</span>
        </RouterLink>
      </div>
    </div>
  </div>
</template>
