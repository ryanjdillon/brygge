<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { Send, Eye, History } from 'lucide-vue-next'

const { t } = useI18n()
const client = useApiClient()
const queryClient = useQueryClient()

import type { components } from '@/types/api'

type Broadcast = components['schemas']['Broadcast']

const subject = ref('')
const body = ref('')
const recipients = ref('all')
const showPreview = ref(false)
const activeTab = ref<'compose' | 'history'>('compose')

const recipientOptions = [
  { value: 'all', labelKey: 'admin.communication.recipientAll' },
  { value: 'members', labelKey: 'admin.communication.recipientMembers' },
  { value: 'styre', labelKey: 'admin.communication.recipientStyre' },
  { value: 'slip_owners', labelKey: 'admin.communication.recipientSlipOwners' },
]

const { data: broadcasts, isLoading: historyLoading } = useQuery({
  queryKey: ['broadcasts'],
  queryFn: async () => unwrap(await client.GET('/api/v1/admin/broadcasts')),
  staleTime: 60 * 1000,
})

const sendMutation = useMutation({
  mutationFn: async () =>
    unwrap(await client.POST('/api/v1/admin/broadcast', {
      body: {
        subject: subject.value,
        body: body.value,
        recipients: recipients.value,
      } as any,
    })),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['broadcasts'] })
    subject.value = ''
    body.value = ''
    recipients.value = 'all'
    showPreview.value = false
    activeTab.value = 'history'
  },
})

const canSend = computed(() => subject.value.trim() && body.value.trim())

function recipientLabel(value: string): string {
  const opt = recipientOptions.find((o) => o.value === value)
  return opt ? t(opt.labelKey) : value
}

function formatDateTime(iso: string): string {
  return new Date(iso).toLocaleString('nb-NO', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.communication.title') }}</h1>

    <div class="mt-6 border-b border-gray-200">
      <nav class="-mb-px flex gap-6">
        <button
          :class="[
            'border-b-2 pb-3 text-sm font-medium transition',
            activeTab === 'compose'
              ? 'border-blue-600 text-blue-600'
              : 'border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-700',
          ]"
          @click="activeTab = 'compose'"
        >
          <Send class="mr-1.5 inline-block h-4 w-4" />
          {{ t('admin.communication.compose') }}
        </button>
        <button
          :class="[
            'border-b-2 pb-3 text-sm font-medium transition',
            activeTab === 'history'
              ? 'border-blue-600 text-blue-600'
              : 'border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-700',
          ]"
          @click="activeTab = 'history'"
        >
          <History class="mr-1.5 inline-block h-4 w-4" />
          {{ t('admin.communication.history') }}
        </button>
      </nav>
    </div>

    <div v-if="activeTab === 'compose'" class="mt-6 max-w-2xl">
      <div v-if="!showPreview" class="space-y-4">
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.communication.recipients') }}</label>
          <select
            v-model="recipients"
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          >
            <option v-for="opt in recipientOptions" :key="opt.value" :value="opt.value">
              {{ t(opt.labelKey) }}
            </option>
          </select>
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.communication.subject') }}</label>
          <input
            v-model="subject"
            type="text"
            required
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.communication.body') }}</label>
          <textarea
            v-model="body"
            rows="8"
            required
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <div class="flex justify-end">
          <button
            :disabled="!canSend"
            class="inline-flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
            @click="showPreview = true"
          >
            <Eye class="h-4 w-4" />
            {{ t('admin.communication.preview') }}
          </button>
        </div>
      </div>

      <div v-else class="space-y-4">
        <div class="rounded-lg border border-gray-200 bg-gray-50 p-6">
          <div class="mb-2 text-sm text-gray-500">
            {{ t('admin.communication.recipients') }}: {{ recipientLabel(recipients) }}
          </div>
          <h3 class="text-lg font-semibold text-gray-900">{{ subject }}</h3>
          <p class="mt-3 whitespace-pre-wrap text-gray-700">{{ body }}</p>
        </div>

        <div class="flex justify-end gap-3">
          <button
            class="rounded-md border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
            @click="showPreview = false"
          >
            {{ t('common.edit') }}
          </button>
          <button
            :disabled="sendMutation.isPending.value"
            class="inline-flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
            @click="sendMutation.mutate()"
          >
            <Send class="h-4 w-4" />
            {{ sendMutation.isPending.value ? t('common.loading') + '...' : t('admin.communication.send') }}
          </button>
        </div>
      </div>
    </div>

    <div v-if="activeTab === 'history'" class="mt-6">
      <div v-if="historyLoading" class="text-center text-gray-500">
        {{ t('common.loading') }}...
      </div>

      <div v-else-if="!broadcasts?.length" class="text-center text-gray-500">
        {{ t('admin.communication.noHistory') }}
      </div>

      <div v-else class="space-y-4">
        <div
          v-for="bc in broadcasts"
          :key="bc.id"
          class="rounded-lg border border-gray-200 bg-white p-4"
        >
          <div class="flex items-start justify-between">
            <div>
              <h3 class="font-medium text-gray-900">{{ bc.subject }}</h3>
              <div class="mt-1 flex gap-3 text-xs text-gray-500">
                <span>{{ recipientLabel(bc.recipients) }}</span>
                <span>{{ formatDateTime(bc.sent_at) }}</span>
              </div>
            </div>
          </div>
          <p class="mt-2 whitespace-pre-wrap text-sm text-gray-600">{{ bc.body }}</p>
        </div>
      </div>
    </div>
  </div>
</template>
