<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { usePaymentsList, useExportCSV, type PaymentsFilters, type Payment } from '@/composables/useFinancials'
import { Download } from 'lucide-vue-next'

const { t } = useI18n()

const typeFilter = ref('')
const statusFilter = ref('')
const yearFilter = ref<number | undefined>(undefined)
const currentPage = ref(1)
const perPage = 50

const selectedPayment = ref<Payment | null>(null)

const filters = computed<PaymentsFilters>(() => ({
  type: typeFilter.value || undefined,
  status: statusFilter.value || undefined,
  year: yearFilter.value,
  page: currentPage.value,
  per_page: perPage,
}))

const { data, isLoading, error } = usePaymentsList(filters)
const { downloadCSV } = useExportCSV()

const currentYear = new Date().getFullYear()
const yearOptions = computed(() => {
  const years: { label: string; value: number | undefined }[] = [
    { label: t('admin.financials.allYears'), value: undefined },
  ]
  for (let y = currentYear; y >= currentYear - 5; y--) {
    years.push({ label: String(y), value: y })
  }
  return years
})

const totalPages = computed(() => {
  if (!data.value) return 1
  return Math.max(1, Math.ceil(data.value.total / perPage))
})

function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString('nb-NO', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  })
}

function formatNOK(amount: number): string {
  return new Intl.NumberFormat('nb-NO', { style: 'currency', currency: 'NOK' }).format(amount)
}

function statusClass(status: string): string {
  switch (status) {
    case 'completed':
      return 'bg-green-100 text-green-800'
    case 'failed':
      return 'bg-red-100 text-red-800'
    case 'refunded':
      return 'bg-gray-100 text-gray-800'
    default:
      return 'bg-yellow-100 text-yellow-800'
  }
}

function typeClass(type: string): string {
  switch (type) {
    case 'dues':
      return 'bg-blue-100 text-blue-800'
    case 'andel':
      return 'bg-purple-100 text-purple-800'
    case 'booking':
      return 'bg-teal-100 text-teal-800'
    case 'slip_fee':
      return 'bg-indigo-100 text-indigo-800'
    default:
      return 'bg-gray-100 text-gray-800'
  }
}

function handleExport() {
  downloadCSV({
    type: typeFilter.value || undefined,
    status: statusFilter.value || undefined,
    year: yearFilter.value,
  })
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.financials.payments') }}</h1>
      <button
        class="inline-flex items-center gap-2 rounded-md bg-gray-100 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-200"
        @click="handleExport"
      >
        <Download class="h-4 w-4" />
        {{ t('admin.financials.exportCSV') }}
      </button>
    </div>

    <div class="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-3">
      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.financials.paymentType') }}</label>
        <select
          v-model="typeFilter"
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        >
          <option value="">{{ t('admin.financials.allTypes') }}</option>
          <option value="dues">{{ t('admin.financials.typeDues') }}</option>
          <option value="andel">{{ t('admin.financials.typeAndel') }}</option>
          <option value="slip_fee">{{ t('admin.financials.typeSlipFee') }}</option>
          <option value="booking">{{ t('admin.financials.typeBooking') }}</option>
          <option value="merchandise">{{ t('admin.financials.typeMerchandise') }}</option>
        </select>
      </div>
      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('common.status') }}</label>
        <select
          v-model="statusFilter"
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        >
          <option value="">{{ t('admin.financials.allStatuses') }}</option>
          <option value="pending">{{ t('admin.financials.statusPending') }}</option>
          <option value="completed">{{ t('admin.financials.statusCompleted') }}</option>
          <option value="failed">{{ t('admin.financials.statusFailed') }}</option>
          <option value="refunded">{{ t('admin.financials.statusRefunded') }}</option>
        </select>
      </div>
      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.financials.year') }}</label>
        <select
          v-model="yearFilter"
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        >
          <option v-for="opt in yearOptions" :key="String(opt.value)" :value="opt.value">
            {{ opt.label }}
          </option>
        </select>
      </div>
    </div>

    <div v-if="isLoading" class="mt-8 text-center text-gray-500">
      {{ t('common.loading') }}...
    </div>

    <div v-else-if="error" class="mt-8 text-center text-red-600">
      {{ t('admin.financials.loadError') }}
    </div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.financials.date') }}
            </th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.financials.member') }}
            </th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.financials.paymentType') }}
            </th>
            <th scope="col" class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.financials.amount') }}
            </th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('common.status') }}
            </th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.financials.reference') }}
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-if="!data?.payments.length">
            <td colspan="6" class="px-4 py-8 text-center text-gray-500">
              {{ t('common.noResults') }}
            </td>
          </tr>
          <tr
            v-for="payment in data?.payments"
            :key="payment.id"
            class="cursor-pointer hover:bg-gray-50"
            @click="selectedPayment = payment"
          >
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-600">
              {{ formatDate(payment.created_at) }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">
              {{ payment.user_name }}
            </td>
            <td class="px-4 py-3">
              <span :class="['inline-flex rounded-full px-2 py-0.5 text-xs font-medium', typeClass(payment.type)]">
                {{ t(`admin.financials.type${payment.type.charAt(0).toUpperCase() + payment.type.slice(1).replace('_', '')}`) }}
              </span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-right text-sm font-medium text-gray-900">
              {{ formatNOK(payment.amount) }}
            </td>
            <td class="px-4 py-3">
              <span :class="['inline-flex rounded-full px-2 py-0.5 text-xs font-medium', statusClass(payment.status)]">
                {{ t(`admin.financials.status${payment.status.charAt(0).toUpperCase() + payment.status.slice(1)}`) }}
              </span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">
              {{ payment.vipps_reference || '-' }}
            </td>
          </tr>
        </tbody>
      </table>

      <div v-if="totalPages > 1" class="mt-4 flex items-center justify-between border-t border-gray-200 pt-4">
        <p class="text-sm text-gray-700">
          {{ t('admin.financials.totalPayments', { count: data?.total ?? 0 }) }}
        </p>
        <div class="flex gap-2">
          <button
            :disabled="currentPage <= 1"
            class="rounded-md border border-gray-300 px-3 py-1 text-sm disabled:opacity-50"
            @click="currentPage--"
          >
            {{ t('common.back') }}
          </button>
          <span class="px-3 py-1 text-sm text-gray-700">
            {{ currentPage }} / {{ totalPages }}
          </span>
          <button
            :disabled="currentPage >= totalPages"
            class="rounded-md border border-gray-300 px-3 py-1 text-sm disabled:opacity-50"
            @click="currentPage++"
          >
            {{ t('admin.financials.next') }}
          </button>
        </div>
      </div>
    </div>

    <div
      v-if="selectedPayment"
      role="dialog" aria-modal="true" class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      @click.self="selectedPayment = null"
    >
      <div class="w-full max-w-md rounded-lg bg-white p-6 shadow-xl">
        <h2 class="text-lg font-semibold text-gray-900">{{ t('admin.financials.paymentDetails') }}</h2>
        <dl class="mt-4 space-y-3">
          <div class="flex justify-between">
            <dt class="text-sm text-gray-500">{{ t('admin.financials.member') }}</dt>
            <dd class="text-sm font-medium text-gray-900">{{ selectedPayment.user_name }}</dd>
          </div>
          <div class="flex justify-between">
            <dt class="text-sm text-gray-500">{{ t('contact.email') }}</dt>
            <dd class="text-sm text-gray-700">{{ selectedPayment.user_email }}</dd>
          </div>
          <div class="flex justify-between">
            <dt class="text-sm text-gray-500">{{ t('admin.financials.paymentType') }}</dt>
            <dd class="text-sm text-gray-700">{{ selectedPayment.type }}</dd>
          </div>
          <div class="flex justify-between">
            <dt class="text-sm text-gray-500">{{ t('admin.financials.amount') }}</dt>
            <dd class="text-sm font-medium text-gray-900">{{ formatNOK(selectedPayment.amount) }}</dd>
          </div>
          <div class="flex justify-between">
            <dt class="text-sm text-gray-500">{{ t('common.status') }}</dt>
            <dd>
              <span :class="['inline-flex rounded-full px-2 py-0.5 text-xs font-medium', statusClass(selectedPayment.status)]">
                {{ selectedPayment.status }}
              </span>
            </dd>
          </div>
          <div v-if="selectedPayment.description" class="flex justify-between">
            <dt class="text-sm text-gray-500">{{ t('admin.financials.description') }}</dt>
            <dd class="text-sm text-gray-700">{{ selectedPayment.description }}</dd>
          </div>
          <div v-if="selectedPayment.due_date" class="flex justify-between">
            <dt class="text-sm text-gray-500">{{ t('admin.financials.dueDate') }}</dt>
            <dd class="text-sm text-gray-700">{{ formatDate(selectedPayment.due_date) }}</dd>
          </div>
          <div v-if="selectedPayment.paid_at" class="flex justify-between">
            <dt class="text-sm text-gray-500">{{ t('admin.financials.paidAt') }}</dt>
            <dd class="text-sm text-gray-700">{{ formatDate(selectedPayment.paid_at) }}</dd>
          </div>
          <div v-if="selectedPayment.vipps_reference" class="flex justify-between">
            <dt class="text-sm text-gray-500">{{ t('admin.financials.reference') }}</dt>
            <dd class="text-sm text-gray-700">{{ selectedPayment.vipps_reference }}</dd>
          </div>
        </dl>
        <button
          class="mt-6 w-full rounded-md bg-gray-100 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-200"
          @click="selectedPayment = null"
        >
          {{ t('common.close') }}
        </button>
      </div>
    </div>
  </div>
</template>
