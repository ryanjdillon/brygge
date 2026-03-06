<script setup lang="ts">
import { ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import { Plus, X } from 'lucide-vue-next'

const { t } = useI18n()
const { fetchApi } = useApi()
const queryClient = useQueryClient()

interface Booking {
  id: string
  resource_id: string
  start_date: string
  end_date: string
  status: string
  notes: string
}

const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

function showToast(type: 'success' | 'error', message: string) {
  toast.value = { type, message }
  setTimeout(() => (toast.value = null), 3000)
}

const { data: bookings, isLoading, isError } = useQuery({
  queryKey: ['portal', 'bookings'],
  queryFn: () => fetchApi<Booking[]>('/api/v1/bookings/me'),
})

const { mutate: cancelBooking } = useMutation({
  mutationFn: (id: string) =>
    fetchApi(`/api/v1/bookings/${id}/cancel`, { method: 'POST' }),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['portal', 'bookings'] })
    showToast('success', t('portal.bookings.cancelSuccess'))
  },
  onError: () => {
    showToast('error', t('portal.bookings.cancelError'))
  },
})

function confirmCancel(id: string) {
  if (confirm(t('portal.bookings.cancelConfirm'))) {
    cancelBooking(id)
  }
}

const statusClasses: Record<string, string> = {
  pending: 'bg-yellow-100 text-yellow-800',
  confirmed: 'bg-green-100 text-green-800',
  cancelled: 'bg-gray-100 text-gray-500',
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('portal.bookings.title') }}</h1>
      <RouterLink
        to="/bookings"
        class="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700"
      >
        <Plus class="h-4 w-4" />
        {{ t('portal.bookings.newBooking') }}
      </RouterLink>
    </div>

    <div
      v-if="toast"
      :class="[
        'mt-4 rounded-md p-3 text-sm',
        toast.type === 'success' ? 'bg-green-50 text-green-800' : 'bg-red-50 text-red-800',
      ]"
    >
      {{ toast.message }}
    </div>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>

    <div v-else-if="isError" class="mt-6 rounded-md bg-red-50 p-3 text-sm text-red-800">
      {{ t('portal.bookings.loadError') }}
    </div>

    <div v-else-if="!bookings?.length" class="mt-6 text-gray-500">
      {{ t('portal.bookings.noBookings') }}
    </div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('portal.bookings.resource') }}</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('portal.bookings.startDate') }}</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('portal.bookings.endDate') }}</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('portal.bookings.status') }}</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-for="booking in bookings" :key="booking.id">
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">{{ booking.resource_id }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ new Date(booking.start_date).toLocaleDateString() }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ new Date(booking.end_date).toLocaleDateString() }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm">
              <span :class="['rounded-full px-2.5 py-0.5 text-xs font-medium', statusClasses[booking.status]]">
                {{ t(`portal.bookings.${booking.status}`) }}
              </span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm">
              <button
                v-if="booking.status !== 'cancelled'"
                class="flex items-center gap-1 text-red-600 hover:text-red-800"
                @click="confirmCancel(booking.id)"
              >
                <X class="h-4 w-4" />
                {{ t('portal.bookings.cancelBooking') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
