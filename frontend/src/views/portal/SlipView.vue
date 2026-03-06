<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import { AlertTriangle } from 'lucide-vue-next'

const { t } = useI18n()
const { fetchApi } = useApi()

interface SlipData {
  slip_id: string
  number: string
  section: string
  length_m: number | null
  width_m: number | null
  depth_m: number | null
  status: string
  assigned_at: string
}

const { data: slip, isLoading, isError } = useQuery({
  queryKey: ['portal', 'slip'],
  queryFn: () => fetchApi<SlipData>('/api/v1/members/me/slip'),
})

const showIssueForm = ref(false)
const issueForm = ref({ title: '', description: '', priority: 'medium' })
const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

function showToast(type: 'success' | 'error', message: string) {
  toast.value = { type, message }
  setTimeout(() => (toast.value = null), 3000)
}

const { mutate: reportIssue, isPending: isReporting } = useMutation({
  mutationFn: () =>
    fetchApi('/api/v1/members/me/slip/issues', {
      method: 'POST',
      body: JSON.stringify(issueForm.value),
    }),
  onSuccess: () => {
    showToast('success', t('portal.slip.issueSuccess'))
    showIssueForm.value = false
    issueForm.value = { title: '', description: '', priority: 'medium' }
  },
  onError: () => {
    showToast('error', t('portal.slip.issueError'))
  },
})
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('portal.slip.title') }}</h1>

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
      {{ t('portal.slip.loadError') }}
    </div>

    <template v-else-if="slip">
      <div class="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div class="rounded-lg border border-gray-200 bg-white p-5">
          <p class="text-sm font-medium text-gray-500">{{ t('portal.slip.slipNumber') }}</p>
          <p class="mt-1 text-lg font-semibold text-gray-900">#{{ slip.number }}</p>
        </div>
        <div class="rounded-lg border border-gray-200 bg-white p-5">
          <p class="text-sm font-medium text-gray-500">{{ t('portal.slip.section') }}</p>
          <p class="mt-1 text-lg font-semibold text-gray-900">{{ slip.section }}</p>
        </div>
        <div class="rounded-lg border border-gray-200 bg-white p-5">
          <p class="text-sm font-medium text-gray-500">{{ t('portal.slip.size') }}</p>
          <p class="mt-1 text-lg font-semibold text-gray-900">
            {{ slip.length_m ?? '—' }} × {{ slip.width_m ?? '—' }} m
          </p>
        </div>
        <div class="rounded-lg border border-gray-200 bg-white p-5">
          <p class="text-sm font-medium text-gray-500">{{ t('portal.slip.status') }}</p>
          <span
            :class="[
              'mt-1 inline-block rounded-full px-3 py-1 text-sm font-medium',
              slip.status === 'available' ? 'bg-green-100 text-green-800' : 'bg-blue-100 text-blue-800',
            ]"
          >
            {{ slip.status }}
          </span>
        </div>
      </div>

      <div class="mt-8 rounded-lg border border-gray-200 bg-white p-5">
        <p class="text-sm text-gray-500">
          {{ t('portal.slip.assignedAt') }}: {{ new Date(slip.assigned_at).toLocaleDateString() }}
        </p>
      </div>

      <div class="mt-8">
        <button
          v-if="!showIssueForm"
          class="flex items-center gap-1.5 rounded-md bg-yellow-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-yellow-700"
          @click="showIssueForm = true"
        >
          <AlertTriangle class="h-4 w-4" />
          {{ t('portal.slip.reportIssue') }}
        </button>

        <form
          v-else
          class="max-w-lg space-y-4 rounded-lg border border-gray-200 bg-white p-5"
          @submit.prevent="reportIssue()"
        >
          <h3 class="text-lg font-semibold text-gray-900">{{ t('portal.slip.reportIssue') }}</h3>

          <div>
            <label for="issue-title" class="block text-sm font-medium text-gray-700">{{ t('portal.slip.issueTitle') }}</label>
            <input
              id="issue-title"
              v-model="issueForm.title"
              type="text"
              required
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label for="issue-desc" class="block text-sm font-medium text-gray-700">{{ t('portal.slip.issueDescription') }}</label>
            <textarea
              id="issue-desc"
              v-model="issueForm.description"
              rows="3"
              required
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label for="issue-priority" class="block text-sm font-medium text-gray-700">{{ t('portal.slip.issuePriority') }}</label>
            <select
              id="issue-priority"
              v-model="issueForm.priority"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="low">{{ t('portal.slip.priorityLow') }}</option>
              <option value="medium">{{ t('portal.slip.priorityMedium') }}</option>
              <option value="high">{{ t('portal.slip.priorityHigh') }}</option>
            </select>
          </div>

          <div class="flex gap-3">
            <button
              type="submit"
              :disabled="isReporting"
              class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 disabled:opacity-50"
            >
              {{ t('common.submit') }}
            </button>
            <button
              type="button"
              class="rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-semibold text-gray-700 shadow-sm hover:bg-gray-50"
              @click="showIssueForm = false"
            >
              {{ t('common.cancel') }}
            </button>
          </div>
        </form>
      </div>
    </template>

    <div v-else class="mt-6 text-gray-500">{{ t('portal.slip.noSlip') }}</div>
  </div>
</template>
