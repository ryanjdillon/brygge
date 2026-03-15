<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useOverduePayments, useExportCSV } from '@/composables/useFinancials'
import { AlertTriangle, Download, Mail } from 'lucide-vue-next'

const { t } = useI18n()

const { data: overdue, isLoading, error } = useOverduePayments()
const { downloadCSV } = useExportCSV()

function formatNOK(amount: number): string {
  return new Intl.NumberFormat('nb-NO', { style: 'currency', currency: 'NOK' }).format(amount)
}



function handleExportOverdue() {
  downloadCSV({ status: 'pending' })
}

function handleSendReminder(_paymentId: string) {
  // Stub for reminder functionality
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <div class="flex items-center gap-3">
        <AlertTriangle class="h-6 w-6 text-red-600" />
        <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.financials.overduePayments') }}</h1>
      </div>
      <button
        class="inline-flex items-center gap-2 rounded-md bg-gray-100 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-200"
        @click="handleExportOverdue"
      >
        <Download class="h-4 w-4" />
        {{ t('admin.financials.exportCSV') }}
      </button>
    </div>

    <div v-if="isLoading" class="mt-8 text-center text-gray-500">
      {{ t('common.loading') }}...
    </div>

    <div v-else-if="error" class="mt-8 text-center text-red-600">
      {{ t('admin.financials.loadError') }}
    </div>

    <div v-else-if="!overdue?.length" class="mt-8 text-center text-gray-500">
      {{ t('admin.financials.noOverdue') }}
    </div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.financials.member') }}
            </th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('contact.email') }}
            </th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('contact.phone') }}
            </th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.financials.paymentType') }}
            </th>
            <th scope="col" class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.financials.amount') }}
            </th>
            <th scope="col" class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.financials.daysOverdue') }}
            </th>
            <th scope="col" class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('common.actions') }}
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-for="item in overdue" :key="item.id">
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">
              {{ item.user_name }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-600">
              {{ item.user_email }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-600">
              {{ item.user_phone || '-' }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-600">
              {{ item.type }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-right text-sm font-medium text-gray-900">
              {{ formatNOK(item.amount) }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-right">
              <span class="inline-flex rounded-full bg-red-100 px-2 py-0.5 text-xs font-medium text-red-800">
                {{ t('admin.financials.daysOverdueCount', { count: item.days_overdue }) }}
              </span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-right">
              <button
                class="inline-flex items-center gap-1 text-sm text-blue-600 hover:text-blue-800"
                :title="t('admin.financials.sendReminder')"
                @click="handleSendReminder(item.id)"
              >
                <Mail class="h-4 w-4" />
                {{ t('admin.financials.sendReminder') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
