<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useQuery } from '@tanstack/vue-query'
import { useAuthStore } from '@/stores/auth'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { useFeatures } from '@/composables/useFeatures'
import { useMyInvoices, isOverdue, type MemberInvoice } from '@/composables/useMyInvoices'
import { formatSlip } from '@/lib/slipSort'
import { formatNOK, formatDate } from '@/lib/format'
import BoatCard from '@/components/boats/BoatCard.vue'

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
const recentPaid = computed(() => paid.value.slice(0, 3))

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

function chipBorderClass(inv: MemberInvoice): string {
  if (inv.paid) return 'border-l-green-400'
  if (isOverdue(inv)) return 'border-l-red-400'
  return 'border-l-amber-400'
}

function chipBadgeClass(inv: MemberInvoice): string {
  if (inv.paid) return 'bg-green-100 text-green-800'
  if (isOverdue(inv)) return 'bg-red-100 text-red-800'
  return 'bg-amber-100 text-amber-800'
}

function chipBadgeLabel(inv: MemberInvoice): string {
  if (inv.paid) return t('portal.invoices.status.paid')
  if (isOverdue(inv)) return t('portal.invoices.status.overdue')
  return t('portal.invoices.status.unpaid')
}
</script>

<template>
  <div class="max-w-3xl space-y-5">
    <h1 class="text-2xl font-bold text-gray-900">
      {{ t('portal.dashboard.welcome', { name: userName }) }}
    </h1>

    <div v-if="isLoading" class="text-sm text-gray-500">{{ t('common.loading') }}...</div>

    <template v-else>
      <!-- Membership overview -->
      <section class="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
        <h2 class="text-xs font-semibold uppercase tracking-wide text-gray-400">
          {{ t('portal.dashboard.membershipStatus') }}
        </h2>
        <div class="mt-4 grid grid-cols-2 gap-y-4 sm:grid-cols-3">
          <div>
            <p class="text-xs text-gray-500">Status</p>
            <p class="mt-0.5 font-semibold text-gray-900">{{ displayRole }}</p>
          </div>
          <div>
            <p class="text-xs text-gray-500">{{ t('portal.dashboard.slipInfo') }}</p>
            <p class="mt-0.5 font-semibold text-gray-900">{{ slipLabel }}</p>
          </div>
          <RouterLink
            v-if="dashboard?.queuePosition"
            to="/portal/waiting-list"
            class="group"
          >
            <p class="text-xs text-gray-500">{{ t('portal.dashboard.queuePosition') }}</p>
            <p class="mt-0.5 font-semibold text-blue-600 group-hover:underline">
              {{ t('portal.waitingList.positionOf', { position: dashboard.queuePosition, total: dashboard.queueTotal }) }}
            </p>
          </RouterLink>
        </div>
      </section>

      <!-- Upcoming bookings -->
      <section class="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
        <div class="flex items-center justify-between">
          <h2 class="text-sm font-semibold text-gray-900">{{ t('portal.dashboard.upcomingBookings') }}</h2>
          <RouterLink to="/portal/bookings" class="text-sm font-medium text-blue-600 hover:text-blue-700">
            {{ t('portal.dashboard.viewAll') }}
          </RouterLink>
        </div>
        <p class="mt-2 text-sm text-gray-500">
          {{ dashboard?.upcomingBookingsCount
            ? t('portal.dashboard.bookingsCount', { count: dashboard.upcomingBookingsCount }, dashboard.upcomingBookingsCount)
            : t('portal.dashboard.noBookings') }}
        </p>
      </section>

      <!-- Fakturaar -->
      <section v-if="showInvoices" class="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
        <div class="flex items-center justify-between">
          <h2 class="text-sm font-semibold text-gray-900">{{ t('portal.invoices.title') }}</h2>
          <RouterLink to="/portal/invoices" class="text-sm font-medium text-blue-600 hover:text-blue-700">
            {{ t('portal.dashboard.viewAll') }}
          </RouterLink>
        </div>

        <div v-if="invoicesLoading" class="mt-3 text-sm text-gray-500">{{ t('common.loading') }}...</div>
        <template v-else>
          <div v-if="unpaid.length" class="mt-3 space-y-2">
            <p class="text-xs font-medium uppercase tracking-wide text-gray-400">
              {{ t('portal.invoices.unpaidHeading') }}
            </p>
            <RouterLink
              v-for="inv in unpaid"
              :key="inv.id"
              to="/portal/invoices"
              :class="[
                'flex items-center gap-3 rounded-lg border border-l-4 border-gray-100 bg-gray-50 px-3 py-2.5 transition hover:bg-white hover:shadow-sm',
                chipBorderClass(inv),
              ]"
            >
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm font-medium text-gray-900">
                  #{{ inv.invoice_number }}
                  <span v-if="inv.price_item_name || inv.description" class="font-normal text-gray-500">
                    — {{ inv.price_item_name || inv.description }}
                  </span>
                </p>
                <p class="text-xs text-gray-500">{{ t('portal.invoices.due') }}: {{ formatDate(inv.due_date) }}</p>
              </div>
              <div class="flex shrink-0 items-center gap-2">
                <span class="text-sm font-semibold tabular-nums text-gray-900">
                  {{ formatNOK(inv.total_amount) }}
                </span>
                <span :class="['rounded-full px-2 py-0.5 text-xs font-medium', chipBadgeClass(inv)]">
                  {{ chipBadgeLabel(inv) }}
                </span>
              </div>
            </RouterLink>
          </div>

          <div v-if="recentPaid.length" :class="['space-y-2', unpaid.length ? 'mt-4' : 'mt-3']">
            <p class="text-xs font-medium uppercase tracking-wide text-gray-400">
              {{ t('portal.invoices.recentPaidHeading') }}
            </p>
            <RouterLink
              v-for="inv in recentPaid"
              :key="inv.id"
              to="/portal/invoices"
              :class="[
                'flex items-center gap-3 rounded-lg border border-l-4 border-gray-100 bg-gray-50 px-3 py-2.5 transition hover:bg-white hover:shadow-sm',
                chipBorderClass(inv),
              ]"
            >
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm text-gray-500">
                  #{{ inv.invoice_number }}
                  <span v-if="inv.price_item_name || inv.description">
                    — {{ inv.price_item_name || inv.description }}
                  </span>
                </p>
                <p class="text-xs text-gray-400">{{ t('portal.invoices.due') }}: {{ formatDate(inv.due_date) }}</p>
              </div>
              <div class="flex shrink-0 items-center gap-2">
                <span class="text-sm tabular-nums text-gray-500">{{ formatNOK(inv.total_amount) }}</span>
                <span :class="['rounded-full px-2 py-0.5 text-xs font-medium', chipBadgeClass(inv)]">
                  {{ chipBadgeLabel(inv) }}
                </span>
              </div>
            </RouterLink>
          </div>

          <p v-if="!unpaid.length && !paid.length" class="mt-3 text-sm text-gray-500">
            {{ t('portal.invoices.none') }}
          </p>
        </template>
      </section>

      <!-- Båtane mine -->
      <section class="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
        <div class="flex items-center justify-between">
          <h2 class="text-sm font-semibold text-gray-900">{{ t('portal.dashboard.myBoats') }}</h2>
          <RouterLink to="/portal/boats" class="text-sm font-medium text-blue-600 hover:text-blue-700">
            {{ t('portal.dashboard.manageBoats') }}
          </RouterLink>
        </div>
        <div v-if="boatsLoading" class="mt-3 text-sm text-gray-500">{{ t('common.loading') }}...</div>
        <div v-else-if="boats && boats.length" class="mt-3 space-y-3">
          <BoatCard v-for="boat in boats" :key="boat.id" :boat="boat" />
        </div>
        <p v-else class="mt-3 text-sm text-gray-500">
          {{ t('portal.dashboard.noBoats') }}
          <RouterLink to="/portal/boats" class="font-medium text-blue-600 hover:text-blue-700">
            {{ t('portal.dashboard.addBoat') }}
          </RouterLink>
        </p>
      </section>
    </template>
  </div>
</template>
