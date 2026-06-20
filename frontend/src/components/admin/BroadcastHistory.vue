<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { AlertTriangle, RefreshCw, Megaphone } from 'lucide-vue-next'
import { useBroadcasts, useBroadcastDetail, useRetryBroadcast } from '@/composables/useBroadcasts'
import { formatDateTime } from '@/lib/format'

const { t } = useI18n()

const { data: broadcasts, isLoading, isError } = useBroadcasts()
const selectedId = ref<string | null>(null)
const { data: detail, isLoading: detailLoading } = useBroadcastDetail(selectedId)
const retry = useRetryBroadcast()

function select(id: string) {
  selectedId.value = id
}

// One badge palette for both broadcast-level and delivery-level statuses.
function statusClass(status: string): string {
  switch (status) {
    case 'sent':
    case 'complete':
      return 'bg-green-100 text-green-700'
    case 'failed':
      return 'bg-red-100 text-red-700'
    case 'pending':
    case 'sending':
      return 'bg-yellow-100 text-yellow-700'
    default:
      return 'bg-gray-100 text-gray-600'
  }
}

const failedCount = computed(() => detail.value?.failed ?? 0)

function onRetry() {
  if (selectedId.value) retry.mutate(selectedId.value)
}
</script>

<template>
  <div class="flex min-h-0 flex-1">
    <!-- List -->
    <section class="w-[28rem] shrink-0 overflow-y-auto border-r border-gray-200 bg-white">
      <div v-if="isLoading" class="p-4 text-sm text-gray-500">{{ t('common.loading') }}</div>
      <div v-else-if="isError" class="m-4 flex items-center gap-2 rounded border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
        <AlertTriangle class="h-4 w-4" />
        {{ t('admin.inbox.broadcasts.error') }}
      </div>
      <div v-else-if="!broadcasts || !broadcasts.length" class="p-8 text-center text-sm text-gray-500">
        {{ t('admin.inbox.broadcasts.empty') }}
      </div>
      <ul v-else class="divide-y divide-gray-100">
        <li v-for="b in broadcasts" :key="b.id">
          <button
            type="button"
            class="flex w-full flex-col gap-1 px-4 py-3 text-left hover:bg-gray-50"
            :class="{ 'bg-blue-50': b.id === selectedId }"
            @click="select(b.id)"
          >
            <div class="flex items-center justify-between gap-2">
              <span class="truncate font-medium text-gray-900">{{ b.subject || t('admin.inbox.noSubject') }}</span>
              <span class="shrink-0 rounded-full px-2 py-0.5 text-xs" :class="statusClass(b.status)">
                {{ t('admin.inbox.broadcasts.status_' + b.status, b.status) }}
              </span>
            </div>
            <div class="text-xs text-gray-500">{{ b.recipients }} · {{ formatDateTime(b.created_at) }}</div>
            <div class="flex gap-3 text-xs">
              <span class="text-green-700">{{ t('admin.inbox.broadcasts.sentN', { n: b.sent }) }}</span>
              <span v-if="b.failed" class="text-red-700">{{ t('admin.inbox.broadcasts.failedN', { n: b.failed }) }}</span>
              <span v-if="b.pending" class="text-yellow-700">{{ t('admin.inbox.broadcasts.pendingN', { n: b.pending }) }}</span>
              <span class="text-gray-400">/ {{ b.total }}</span>
            </div>
          </button>
        </li>
      </ul>
    </section>

    <!-- Detail -->
    <main class="flex-1 overflow-y-auto bg-white">
      <div v-if="!selectedId" class="flex h-full items-center justify-center text-sm text-gray-400">
        <div class="flex flex-col items-center gap-2">
          <Megaphone class="h-8 w-8 text-gray-300" />
          {{ t('admin.inbox.broadcasts.selectPrompt') }}
        </div>
      </div>
      <div v-else-if="detailLoading" class="p-6 text-sm text-gray-500">{{ t('common.loading') }}</div>
      <div v-else-if="detail" class="p-6">
        <div class="mb-4 flex items-start justify-between gap-4">
          <div>
            <h2 class="text-lg font-semibold text-gray-900">{{ detail.subject || t('admin.inbox.noSubject') }}</h2>
            <p class="mt-1 text-sm text-gray-500">
              {{ detail.recipients }} · {{ formatDateTime(detail.created_at) }}
            </p>
          </div>
          <button
            v-if="failedCount > 0"
            type="button"
            class="inline-flex items-center gap-1.5 rounded-md border border-gray-300 px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
            :disabled="retry.isPending.value"
            @click="onRetry"
          >
            <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': retry.isPending.value }" />
            {{ t('admin.inbox.broadcasts.retryFailed', { n: failedCount }) }}
          </button>
        </div>

        <table class="w-full text-left text-sm">
          <thead class="border-b border-gray-200 text-xs uppercase text-gray-500">
            <tr>
              <th class="py-2 pr-4 font-medium">{{ t('admin.inbox.broadcasts.recipient') }}</th>
              <th class="py-2 pr-4 font-medium">{{ t('admin.inbox.broadcasts.statusCol') }}</th>
              <th class="py-2 pr-4 font-medium">{{ t('admin.inbox.broadcasts.attempts') }}</th>
              <th class="py-2 font-medium">{{ t('admin.inbox.broadcasts.detail') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100">
            <tr v-for="d in detail.deliveries" :key="d.id">
              <td class="py-2 pr-4 text-gray-900">{{ d.email }}</td>
              <td class="py-2 pr-4">
                <span class="rounded-full px-2 py-0.5 text-xs" :class="statusClass(d.status)">
                  {{ t('admin.inbox.broadcasts.status_' + d.status, d.status) }}
                </span>
              </td>
              <td class="py-2 pr-4 text-gray-500">{{ d.attempts }}</td>
              <td class="py-2 text-gray-500">
                <span v-if="d.sent_at">{{ formatDateTime(d.sent_at) }}</span>
                <span v-else-if="d.error" class="text-red-600">{{ d.error }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </main>
  </div>
</template>
