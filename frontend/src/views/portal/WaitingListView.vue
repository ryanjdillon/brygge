<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import { ref } from 'vue'
import { CheckCircle, XCircle, LogOut } from 'lucide-vue-next'

const { t } = useI18n()
const { fetchApi } = useApi()
const queryClient = useQueryClient()

interface WaitingListEntry {
  id: string
  position: number
  is_local: boolean
  status: string
  offer_deadline: string | null
}

const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

function showToast(type: 'success' | 'error', message: string) {
  toast.value = { type, message }
  setTimeout(() => (toast.value = null), 3000)
}

const { data: entry, isLoading, isError } = useQuery({
  queryKey: ['portal', 'waiting-list'],
  queryFn: () => fetchApi<WaitingListEntry>('/api/v1/waiting-list/me'),
})

const { mutate: acceptOffer, isPending: isAccepting } = useMutation({
  mutationFn: () =>
    fetchApi(`/api/v1/waiting-list/${entry.value!.id}/accept`, { method: 'POST' }),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['portal', 'waiting-list'] })
    showToast('success', t('portal.waitingList.acceptSuccess'))
  },
})

const { mutate: declineOffer, isPending: isDeclining } = useMutation({
  mutationFn: () =>
    fetchApi(`/api/v1/waiting-list/${entry.value!.id}/decline`, { method: 'POST' }),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['portal', 'waiting-list'] })
    showToast('success', t('portal.waitingList.declineSuccess'))
  },
})

const { mutate: withdraw, isPending: isWithdrawing } = useMutation({
  mutationFn: () =>
    fetchApi('/api/v1/waiting-list/withdraw', { method: 'POST' }),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['portal', 'waiting-list'] })
    showToast('success', t('portal.waitingList.withdrawSuccess'))
  },
})

function confirmWithdraw() {
  if (confirm(t('portal.waitingList.withdrawConfirm'))) {
    withdraw()
  }
}
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('portal.waitingList.title') }}</h1>

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
      {{ t('portal.waitingList.loadError') }}
    </div>

    <template v-else-if="entry">
      <div class="mt-6 grid gap-4 sm:grid-cols-3">
        <div class="rounded-lg border border-gray-200 bg-white p-5">
          <p class="text-sm font-medium text-gray-500">{{ t('portal.waitingList.position') }}</p>
          <p class="mt-1 text-2xl font-bold text-gray-900">#{{ entry.position }}</p>
        </div>

        <div class="rounded-lg border border-gray-200 bg-white p-5">
          <p class="text-sm font-medium text-gray-500">{{ t('portal.waitingList.localStatus') }}</p>
          <span
            :class="[
              'mt-1 inline-block rounded-full px-3 py-1 text-sm font-medium',
              entry.is_local ? 'bg-green-100 text-green-800' : 'bg-yellow-100 text-yellow-800',
            ]"
          >
            {{ entry.is_local ? t('portal.waitingList.local') : t('portal.waitingList.nonLocal') }}
          </span>
        </div>

        <div class="rounded-lg border border-gray-200 bg-white p-5">
          <p class="text-sm font-medium text-gray-500">{{ t('portal.waitingList.status') }}</p>
          <span class="mt-1 inline-block rounded-full bg-blue-100 px-3 py-1 text-sm font-medium text-blue-800">
            {{ entry.status }}
          </span>
        </div>
      </div>

      <div
        v-if="entry.status === 'offered' && entry.offer_deadline"
        class="mt-6 rounded-lg border-2 border-blue-300 bg-blue-50 p-6"
      >
        <h2 class="text-lg font-bold text-blue-900">{{ t('portal.waitingList.slipOffer') }}</h2>
        <p class="mt-2 text-sm text-blue-800">
          <strong>Deadline:</strong> {{ new Date(entry.offer_deadline).toLocaleDateString() }}
        </p>

        <div class="mt-5 flex gap-3">
          <button
            :disabled="isAccepting"
            class="flex items-center gap-1.5 rounded-md bg-green-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-green-700 disabled:opacity-50"
            @click="acceptOffer()"
          >
            <CheckCircle class="h-4 w-4" />
            {{ t('portal.waitingList.accept') }}
          </button>
          <button
            :disabled="isDeclining"
            class="flex items-center gap-1.5 rounded-md bg-red-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-red-700 disabled:opacity-50"
            @click="declineOffer()"
          >
            <XCircle class="h-4 w-4" />
            {{ t('portal.waitingList.decline') }}
          </button>
        </div>
      </div>

      <div class="mt-8">
        <button
          :disabled="isWithdrawing"
          class="flex items-center gap-1.5 rounded-md border border-red-300 bg-white px-4 py-2 text-sm font-semibold text-red-700 hover:bg-red-50 disabled:opacity-50"
          @click="confirmWithdraw"
        >
          <LogOut class="h-4 w-4" />
          {{ t('portal.waitingList.withdraw') }}
        </button>
      </div>
    </template>
  </div>
</template>
