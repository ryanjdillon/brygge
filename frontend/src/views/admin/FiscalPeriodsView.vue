<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Plus, Lock, Unlock } from 'lucide-vue-next'
import {
  useFiscalPeriods,
  useCreatePeriod,
  useClosePeriod,
  useReopenPeriod,
} from '@/composables/useAccounting'

const { t } = useI18n()

const { data: periods, isLoading, isError } = useFiscalPeriods()
const createMutation = useCreatePeriod()
const closeMutation = useClosePeriod()
const reopenMutation = useReopenPeriod()

const newYear = ref(new Date().getFullYear())
const showCreateForm = ref(false)

function handleCreate() {
  createMutation.mutate({ year: newYear.value }, {
    onSuccess: () => {
      showCreateForm.value = false
    },
  })
}

function handleClose(periodId: string) {
  if (confirm(t('admin.accounting.periods.confirmClose'))) {
    closeMutation.mutate(periodId)
  }
}

function handleReopen(periodId: string) {
  reopenMutation.mutate(periodId)
}

function formatDate(d: string | null): string {
  if (!d) return '-'
  return new Date(d).toLocaleDateString('nb-NO')
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.accounting.periods.title') }}</h1>
      <button
        class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
        @click="showCreateForm = !showCreateForm"
      >
        <Plus class="h-4 w-4" />
        {{ t('admin.accounting.periods.createYear') }}
      </button>
    </div>

    <div v-if="showCreateForm" class="mt-4 flex items-end gap-3 rounded-lg border border-gray-200 bg-gray-50 p-4">
      <div>
        <label class="mb-1 block text-sm font-medium text-gray-700">{{ t('admin.accounting.periods.year') }}</label>
        <input
          v-model.number="newYear"
          type="number"
          min="2000"
          max="2100"
          class="w-28 rounded-md border border-gray-300 px-3 py-2 text-sm"
        />
      </div>
      <button
        class="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
        :disabled="createMutation.isPending.value"
        @click="handleCreate"
      >
        {{ t('admin.accounting.periods.createYear') }}
      </button>
      <button
        class="rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-700 hover:bg-gray-100"
        @click="showCreateForm = false"
      >
        {{ t('admin.accounting.accounts.cancel') }}
      </button>
      <p v-if="createMutation.isError.value" class="text-sm text-red-600">
        {{ (createMutation.error.value as Error)?.message }}
      </p>
    </div>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>
    <div v-else-if="isError" class="mt-6 rounded-md bg-red-50 p-3 text-sm text-red-800">{{ t('common.error') }}</div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.accounting.periods.year') }}</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.accounting.periods.period') }}</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.accounting.periods.status') }}</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.accounting.periods.closedDate') }}</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.accounting.periods.actions') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr
            v-for="period in periods"
            :key="period.id"
            :class="{ 'bg-blue-50': period.status === 'open' }"
          >
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">{{ period.year }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ period.start_date }} – {{ period.end_date }}</td>
            <td class="whitespace-nowrap px-4 py-3">
              <span
                :class="[
                  'inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium',
                  period.status === 'open'
                    ? 'bg-green-100 text-green-800'
                    : 'bg-gray-100 text-gray-800',
                ]"
              >
                {{ period.status === 'open' ? t('admin.accounting.periods.open') : t('admin.accounting.periods.closed') }}
              </span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ formatDate(period.closed_at) }}</td>
            <td class="whitespace-nowrap px-4 py-3">
              <button
                v-if="period.status === 'open'"
                class="inline-flex items-center gap-1 rounded-md border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50"
                :disabled="closeMutation.isPending.value"
                @click="handleClose(period.id)"
              >
                <Lock class="h-3.5 w-3.5" />
                {{ t('admin.accounting.periods.close') }}
              </button>
              <button
                v-else
                class="inline-flex items-center gap-1 rounded-md border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50"
                :disabled="reopenMutation.isPending.value"
                @click="handleReopen(period.id)"
              >
                <Unlock class="h-3.5 w-3.5" />
                {{ t('admin.accounting.periods.reopen') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
      <p v-if="!periods?.length" class="mt-4 text-center text-sm text-gray-500">
        {{ t('admin.accounting.periods.noPeriods') }}
      </p>
    </div>
  </div>
</template>
