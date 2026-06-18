<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useQuery } from '@tanstack/vue-query'
import { useAuthStore } from '@/stores/auth'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { useFeatures } from '@/composables/useFeatures'
import { useMyInvoices } from '@/composables/useMyInvoices'
import { formatSlip } from '@/lib/slipSort'
import BoatCard from '@/components/boats/BoatCard.vue'
import InvoiceList from '@/components/portal/InvoiceList.vue'

const { t } = useI18n()
const auth = useAuthStore()
const client = useApiClient()
const { isEnabled } = useFeatures()

const { data: dashboard, isLoading } = useQuery({
  queryKey: ['portal', 'dashboard'],
  queryFn: async () => unwrap(await client.GET('/api/v1/members/me/dashboard')),
})

const { data: boats, isLoading: boatsLoading } = useQuery({
  queryKey: ['portal', 'boats'],
  queryFn: async () => unwrap(await client.GET('/api/v1/members/me/boats')) ?? [],
})

const showInvoices = computed(() => isEnabled('accounting'))
const { unpaid, paid, isLoading: invoicesLoading } = useMyInvoices()
const recentPaid = computed(() => paid.value.slice(0, 4))

const userName = computed(() => auth.user?.name ?? '')

const slipLabel = computed(() => {
  const slip = dashboard.value?.slip
  if (!slip) return t('portal.dashboard.noSlip')
  return formatSlip({ section: slip.location, number: slip.number })
})

const displayRole = computed(() => {
  const roles = auth.user?.roles ?? []
  if (roles.includes('admin')) return t('portal.dashboard.role.admin')
  if (roles.includes('board')) return t('portal.dashboard.role.board')
  if (roles.includes('slip_holder')) return t('portal.dashboard.role.slip_holder')
  if (roles.includes('member')) return t('portal.dashboard.role.member')
  if (roles.includes('applicant')) return t('portal.dashboard.role.applicant')
  return ''
})
</script>

<template>
  <div class="max-w-4xl">
    <h1 class="text-2xl font-bold text-gray-900">
      {{ t('portal.dashboard.welcome', { name: userName }) }}
    </h1>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>

    <template v-else>
      <!-- At-a-glance status -->
      <div class="mt-6 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <div class="rounded-lg border border-gray-200 bg-white px-4 py-3.5 shadow-sm">
          <p class="text-xs font-medium uppercase tracking-wide text-gray-400">{{ t('portal.dashboard.membershipStatus') }}</p>
          <p class="mt-1 font-semibold text-gray-900">{{ displayRole }}</p>
        </div>

        <div class="rounded-lg border border-gray-200 bg-white px-4 py-3.5 shadow-sm">
          <p class="text-xs font-medium uppercase tracking-wide text-gray-400">{{ t('portal.dashboard.slipInfo') }}</p>
          <p class="mt-1 font-semibold text-gray-900">{{ slipLabel }}</p>
        </div>

        <RouterLink
          v-if="dashboard?.queuePosition"
          to="/portal/waiting-list"
          class="rounded-lg border border-gray-200 bg-white px-4 py-3.5 shadow-sm transition hover:border-blue-300 hover:shadow"
        >
          <p class="text-xs font-medium uppercase tracking-wide text-gray-400">{{ t('portal.dashboard.queuePosition') }}</p>
          <p class="mt-1 font-semibold text-gray-900">
            {{ t('portal.waitingList.positionOf', { position: dashboard.queuePosition, total: dashboard.queueTotal }) }}
          </p>
        </RouterLink>

        <div class="rounded-lg border border-gray-200 bg-white px-4 py-3.5 shadow-sm">
          <p class="text-xs font-medium uppercase tracking-wide text-gray-400">{{ t('portal.dashboard.upcomingBookings') }}</p>
          <p class="mt-1 font-semibold text-gray-900">
            {{ dashboard?.upcomingBookingsCount
              ? t('portal.dashboard.bookingsCount', { count: dashboard.upcomingBookingsCount }, dashboard.upcomingBookingsCount)
              : t('portal.dashboard.noBookings') }}
          </p>
        </div>
      </div>

      <!-- Fakturas + boats -->
      <div class="mt-6 grid gap-4 lg:grid-cols-3">
        <section
          v-if="showInvoices"
          class="rounded-lg border border-gray-200 bg-white p-5 shadow-sm lg:col-span-2"
        >
          <div class="flex items-center justify-between">
            <h2 class="font-semibold text-gray-900">{{ t('portal.invoices.title') }}</h2>
            <RouterLink to="/portal/invoices" class="text-sm font-medium text-blue-600 hover:text-blue-700">
              {{ t('portal.dashboard.viewAll') }}
            </RouterLink>
          </div>

          <div v-if="invoicesLoading" class="py-6 text-sm text-gray-500">{{ t('common.loading') }}...</div>
          <template v-else>
            <div v-if="unpaid.length" class="mt-2">
              <h3 class="text-xs font-semibold uppercase tracking-wider text-gray-400">
                {{ t('portal.invoices.unpaidHeading') }}
              </h3>
              <InvoiceList :invoices="unpaid" />
            </div>
            <div v-if="recentPaid.length" class="mt-4">
              <h3 class="text-xs font-semibold uppercase tracking-wider text-gray-400">
                {{ t('portal.invoices.recentPaidHeading') }}
              </h3>
              <InvoiceList :invoices="recentPaid" />
            </div>
            <p v-if="!unpaid.length && !paid.length" class="py-6 text-center text-sm text-gray-500">
              {{ t('portal.invoices.none') }}
            </p>
          </template>
        </section>

        <section
          class="rounded-lg border border-gray-200 bg-white p-5 shadow-sm"
          :class="showInvoices ? '' : 'lg:col-span-3'"
        >
          <div class="flex items-center justify-between">
            <h2 class="font-semibold text-gray-900">{{ t('portal.dashboard.myBoats') }}</h2>
            <RouterLink to="/portal/boats" class="text-sm font-medium text-blue-600 hover:text-blue-700">
              {{ t('portal.dashboard.manageBoats') }}
            </RouterLink>
          </div>

          <div v-if="boatsLoading" class="py-6 text-sm text-gray-500">{{ t('common.loading') }}...</div>
          <div v-else-if="boats && boats.length" class="mt-3 space-y-2">
            <BoatCard v-for="boat in boats" :key="boat.id" :boat="boat" compact />
          </div>
          <p v-else class="py-6 text-center text-sm text-gray-500">
            {{ t('portal.dashboard.noBoats') }}
            <RouterLink to="/portal/boats" class="font-medium text-blue-600 hover:text-blue-700">
              {{ t('portal.dashboard.addBoat') }}
            </RouterLink>
          </p>
        </section>
      </div>
    </template>
  </div>
</template>
