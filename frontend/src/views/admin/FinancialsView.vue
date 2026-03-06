<script setup lang="ts">
import { ref, computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useFinancialSummary } from '@/composables/useFinancials'
import {
  Banknote,
  Clock,
  AlertTriangle,
  TrendingUp,
  CreditCard,
  FileDown,
  FilePlus,
} from 'lucide-vue-next'

const { t } = useI18n()

const currentYear = new Date().getFullYear()
const selectedYear = ref<number | undefined>(undefined)

const yearOptions = computed(() => {
  const years: { label: string; value: number | undefined }[] = [
    { label: t('admin.financials.allYears'), value: undefined },
  ]
  for (let y = currentYear; y >= currentYear - 5; y--) {
    years.push({ label: String(y), value: y })
  }
  return years
})

const { data: summary, isLoading, error } = useFinancialSummary(selectedYear)

function formatNOK(amount: number): string {
  return new Intl.NumberFormat('nb-NO', { style: 'currency', currency: 'NOK' }).format(amount)
}

const cards = computed(() => {
  if (!summary.value) return []
  return [
    {
      label: t('admin.financials.duesReceived'),
      value: formatNOK(summary.value.total_dues_received),
      icon: Banknote,
      color: 'text-green-600',
      bg: 'bg-green-50',
    },
    {
      label: t('admin.financials.outstanding'),
      value: formatNOK(summary.value.total_outstanding),
      icon: Clock,
      color: 'text-yellow-600',
      bg: 'bg-yellow-50',
    },
    {
      label: t('admin.financials.overdue'),
      value: formatNOK(summary.value.total_overdue),
      icon: AlertTriangle,
      color: 'text-red-600',
      bg: 'bg-red-50',
    },
    {
      label: t('admin.financials.andelCollected'),
      value: formatNOK(summary.value.total_andel_collected),
      icon: TrendingUp,
      color: 'text-blue-600',
      bg: 'bg-blue-50',
    },
    {
      label: t('admin.financials.bookingRevenue'),
      value: formatNOK(summary.value.total_booking_revenue),
      icon: CreditCard,
      color: 'text-purple-600',
      bg: 'bg-purple-50',
    },
  ]
})
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.financials.title') }}</h1>
      <select
        v-model="selectedYear"
        class="rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
      >
        <option v-for="opt in yearOptions" :key="String(opt.value)" :value="opt.value">
          {{ opt.label }}
        </option>
      </select>
    </div>

    <div v-if="isLoading" class="mt-8 text-center text-gray-500">
      {{ t('common.loading') }}...
    </div>

    <div v-else-if="error" class="mt-8 text-center text-red-600">
      {{ t('admin.financials.loadError') }}
    </div>

    <template v-else>
      <div class="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5">
        <div
          v-for="card in cards"
          :key="card.label"
          class="rounded-lg border border-gray-200 bg-white p-5 shadow-sm"
        >
          <div class="flex items-center gap-3">
            <div :class="['rounded-md p-2', card.bg]">
              <component :is="card.icon" :class="['h-5 w-5', card.color]" />
            </div>
            <p class="text-sm font-medium text-gray-500">{{ card.label }}</p>
          </div>
          <p class="mt-3 text-xl font-semibold text-gray-900">{{ card.value }}</p>
        </div>
      </div>

      <div class="mt-10 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <RouterLink
          to="/admin/financials/payments"
          class="group flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-4 transition hover:border-blue-300 hover:shadow-sm"
        >
          <Banknote class="h-5 w-5 text-blue-600 group-hover:text-blue-700" />
          <span class="text-sm font-medium text-gray-700 group-hover:text-gray-900">
            {{ t('admin.financials.viewPayments') }}
          </span>
        </RouterLink>

        <RouterLink
          to="/admin/financials/overdue"
          class="group flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-4 transition hover:border-red-300 hover:shadow-sm"
        >
          <AlertTriangle class="h-5 w-5 text-red-600 group-hover:text-red-700" />
          <span class="text-sm font-medium text-gray-700 group-hover:text-gray-900">
            {{ t('admin.financials.viewOverdue') }}
          </span>
        </RouterLink>

        <RouterLink
          to="/admin/financials/invoices/new"
          class="group flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-4 transition hover:border-green-300 hover:shadow-sm"
        >
          <FilePlus class="h-5 w-5 text-green-600 group-hover:text-green-700" />
          <span class="text-sm font-medium text-gray-700 group-hover:text-gray-900">
            {{ t('admin.financials.createInvoice') }}
          </span>
        </RouterLink>

        <RouterLink
          to="/admin/financials/payments?export=1"
          class="group flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-4 transition hover:border-gray-400 hover:shadow-sm"
        >
          <FileDown class="h-5 w-5 text-gray-600 group-hover:text-gray-700" />
          <span class="text-sm font-medium text-gray-700 group-hover:text-gray-900">
            {{ t('admin.financials.exportCSV') }}
          </span>
        </RouterLink>
      </div>
    </template>
  </div>
</template>
