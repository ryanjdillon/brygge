<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { Check, XCircle } from 'lucide-vue-next'
const { t } = useI18n()
const client = useApiClient()
const queryClient = useQueryClient()

const statusFilter = ref('')
const resourceTypeFilter = ref('')
const dateFrom = ref('')
const dateTo = ref('')

const queryKey = computed(() => [
  'admin-bookings',
  statusFilter.value,
  resourceTypeFilter.value,
  dateFrom.value,
  dateTo.value,
])

const { data: bookings, isLoading, error } = useQuery({
  queryKey,
  queryFn: async () => {
    const query: Record<string, string> = {}
    if (statusFilter.value) query.status = statusFilter.value
    if (resourceTypeFilter.value) query.resource_type = resourceTypeFilter.value
    if (dateFrom.value) query.start = dateFrom.value
    if (dateTo.value) query.end = dateTo.value
    const res = unwrap(await client.GET('/api/v1/admin/bookings', { params: { query } }))
    return res.items ?? []
  },
  staleTime: 30 * 1000,
})

const confirmMutation = useMutation({
  mutationFn: async (id: string) =>
    unwrap(await client.POST('/api/v1/bookings/{bookingID}/confirm', { params: { path: { bookingID: id } } })),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin-bookings'] })
  },
})

const cancelMutation = useMutation({
  mutationFn: async (id: string) =>
    unwrap(await client.POST('/api/v1/bookings/{bookingID}/cancel', { params: { path: { bookingID: id } } })),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin-bookings'] })
  },
})

function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString('nb-NO', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  })
}

function statusClass(status: string): string {
  switch (status) {
    case 'confirmed':
      return 'bg-green-100 text-green-800'
    case 'cancelled':
      return 'bg-red-100 text-red-800'
    default:
      return 'bg-yellow-100 text-yellow-800'
  }
}
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.bookings.title') }}</h1>

    <div class="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('common.status') }}</label>
        <select
          v-model="statusFilter"
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        >
          <option value="">{{ t('admin.bookings.allStatuses') }}</option>
          <option value="pending">{{ t('portal.bookings.pending') }}</option>
          <option value="confirmed">{{ t('portal.bookings.confirmed') }}</option>
          <option value="cancelled">{{ t('portal.bookings.cancelled') }}</option>
        </select>
      </div>
      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.bookings.resourceType') }}</label>
        <select
          v-model="resourceTypeFilter"
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        >
          <option value="">{{ t('admin.bookings.allTypes') }}</option>
          <option value="guest_slip">{{ t('admin.bookings.guestSlip') }}</option>
          <option value="bobil_spot">{{ t('admin.bookings.motorhomeSpot') }}</option>
          <option value="club_room">{{ t('admin.bookings.clubRoom') }}</option>
          <option value="other">{{ t('admin.events.tagOther') }}</option>
        </select>
      </div>
      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.bookings.dateFrom') }}</label>
        <input
          v-model="dateFrom"
          type="date"
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
      </div>
      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.bookings.dateTo') }}</label>
        <input
          v-model="dateTo"
          type="date"
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
      </div>
    </div>

    <div v-if="isLoading" class="mt-8 text-center text-gray-500">
      {{ t('common.loading') }}...
    </div>

    <div v-else-if="error" class="mt-8 text-center text-red-600">
      {{ t('common.error') }}
    </div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('portal.bookings.resource') }}
            </th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.bookings.bookedBy') }}
            </th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('portal.bookings.date') }}
            </th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('common.status') }}
            </th>
            <th scope="col" class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('common.actions') }}
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-if="!bookings?.length">
            <td colspan="5" class="px-4 py-8 text-center text-gray-500">
              {{ t('common.noResults') }}
            </td>
          </tr>
          <tr v-for="booking in bookings" :key="booking.id">
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">
              {{ booking.resource_name }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-600">
              {{ booking.guest_name || '-' }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-600">
              {{ formatDate(booking.start_date) }} - {{ formatDate(booking.end_date) }}
            </td>
            <td class="px-4 py-3">
              <span
                :class="[
                  'inline-flex rounded-full px-2 py-0.5 text-xs font-medium',
                  statusClass(booking.status),
                ]"
              >
                {{ t(`portal.bookings.${booking.status}`) }}
              </span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-right">
              <button
                v-if="booking.status === 'pending'"
                class="mr-2 text-gray-500 hover:text-green-600"
                :title="t('common.confirm')"
                @click="confirmMutation.mutate(booking.id)"
              >
                <Check class="h-4 w-4" />
              </button>
              <button
                v-if="booking.status !== 'cancelled'"
                class="text-gray-500 hover:text-red-600"
                :title="t('common.cancel')"
                @click="cancelMutation.mutate(booking.id)"
              >
                <XCircle class="h-4 w-4" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
