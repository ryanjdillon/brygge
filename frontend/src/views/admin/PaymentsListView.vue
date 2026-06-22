<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { usePaymentsList, useExportCSV, type PaymentsFilters, type Payment } from '@/composables/useFinancials'
import { Download } from 'lucide-vue-next'
import Select from '@/components/ui/form/Select.vue'
import SortableTh from '@/components/admin/SortableTh.vue'
import { formatNOK, formatDateMedium as formatDate } from '@/lib/format'

const { t } = useI18n()

const typeFilter = ref('')
const statusFilter = ref('')
const yearFilter = ref<number | undefined>(undefined)
const currentPage = ref(1)
const perPage = 50

const selectedPayment = ref<Payment | null>(null)

type SortField = 'date' | 'member' | 'amount' | 'status'
const sortField = ref<SortField>('date')
const sortDir = ref<'asc' | 'desc'>('desc')

function setSort(field: SortField) {
  if (sortField.value === field) {
    sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc'
  } else {
    sortField.value = field
    sortDir.value = field === 'amount' ? 'desc' : 'asc'
  }
}

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
const ALL_YEARS = 0
const yearFilterValue = computed<number>({
  get: () => yearFilter.value ?? ALL_YEARS,
  set: (v) => {
    yearFilter.value = v === ALL_YEARS ? undefined : v
  },
})
const yearOptions = computed(() => {
  const years: { label: string; value: number }[] = [
    { label: t('admin.financials.allYears'), value: ALL_YEARS },
  ]
  for (let y = currentYear; y >= currentYear - 5; y--) {
    years.push({ label: String(y), value: y })
  }
  return years
})

const typeFilterOptions = computed(() => [
  { value: '', label: t('admin.financials.allTypes') },
  { value: 'dues', label: t('admin.financials.typeDues') },
  { value: 'harbor_membership', label: t('admin.financials.typeHarborMembership') },
  { value: 'slip_fee', label: t('admin.financials.typeSlipFee') },
  { value: 'booking', label: t('admin.financials.typeBooking') },
  { value: 'merchandise', label: t('admin.financials.typeMerchandise') },
])

const statusFilterOptions = computed(() => [
  { value: '', label: t('admin.financials.allStatuses') },
  { value: 'pending', label: t('admin.financials.statusPending') },
  { value: 'completed', label: t('admin.financials.statusCompleted') },
  { value: 'failed', label: t('admin.financials.statusFailed') },
  { value: 'refunded', label: t('admin.financials.statusRefunded') },
])

const totalPages = computed(() => {
  if (!data.value) return 1
  return Math.max(1, Math.ceil(data.value.total / perPage))
})

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

const typeKeys: Record<string, string> = {
  dues: 'admin.financials.typeDues',
  harbor_membership: 'admin.financials.typeHarborMembership',
  slip_fee: 'admin.financials.typeSlipFee',
  booking: 'admin.financials.typeBooking',
  merchandise: 'admin.financials.typeMerchandise',
}

function translateType(type: string): string {
  return typeKeys[type] ? t(typeKeys[type]) : type
}

function translateStatus(status: string): string {
  const key = `admin.financials.status${status.charAt(0).toUpperCase() + status.slice(1)}`
  return t(key)
}

function typeClass(type: string): string {
  switch (type) {
    case 'dues':
      return 'bg-brand-100 text-brand-800'
    case 'harbor_membership':
      return 'bg-purple-100 text-purple-800'
    case 'booking':
      return 'bg-teal-100 text-teal-800'
    case 'slip_fee':
      return 'bg-indigo-100 text-indigo-800'
    default:
      return 'bg-gray-100 text-gray-800'
  }
}

const sortedPayments = computed(() => {
  const list = [...(data.value?.payments ?? [])]
  list.sort((a, b) => {
    let cmp = 0
    if (sortField.value === 'date') cmp = (a.created_at ?? '') < (b.created_at ?? '') ? -1 : 1
    else if (sortField.value === 'member') cmp = (a.user_name ?? '').localeCompare(b.user_name ?? '')
    else if (sortField.value === 'amount') cmp = (a.amount ?? 0) - (b.amount ?? 0)
    else if (sortField.value === 'status') cmp = (a.status ?? '').localeCompare(b.status ?? '')
    return sortDir.value === 'asc' ? cmp : -cmp
  })
  return list
})

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
        <div class="mt-1">
          <Select v-model="typeFilter" :options="typeFilterOptions" />
        </div>
      </div>
      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('common.status') }}</label>
        <div class="mt-1">
          <Select v-model="statusFilter" :options="statusFilterOptions" />
        </div>
      </div>
      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.financials.year') }}</label>
        <div class="mt-1">
          <Select v-model="yearFilterValue" :options="yearOptions" />
        </div>
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
            <th scope="col" class="w-10 px-3 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-400">#</th>
            <SortableTh :active="sortField === 'date'" :dir="sortDir" @click="setSort('date')">
              {{ t('admin.financials.date') }}
            </SortableTh>
            <SortableTh :active="sortField === 'member'" :dir="sortDir" @click="setSort('member')">
              {{ t('admin.financials.member') }}
            </SortableTh>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.financials.paymentType') }}
            </th>
            <SortableTh :active="sortField === 'amount'" :dir="sortDir" class="text-right" @click="setSort('amount')">
              {{ t('admin.financials.amount') }}
            </SortableTh>
            <SortableTh :active="sortField === 'status'" :dir="sortDir" @click="setSort('status')">
              {{ t('common.status') }}
            </SortableTh>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.financials.reference') }}
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-if="!sortedPayments.length">
            <td colspan="7" class="px-4 py-8 text-center text-gray-500">
              {{ t('common.noResults') }}
            </td>
          </tr>
          <tr
            v-for="(payment, index) in sortedPayments"
            :key="payment.id"
            class="cursor-pointer hover:bg-gray-50"
            @click="selectedPayment = payment"
          >
            <td class="whitespace-nowrap px-3 py-3 text-right text-xs text-gray-400 tabular-nums">{{ (currentPage - 1) * perPage + index + 1 }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-600">
              {{ formatDate(payment.created_at) }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">
              {{ payment.user_name }}
            </td>
            <td class="px-4 py-3">
              <span :class="['inline-flex rounded-full px-2 py-0.5 text-xs font-medium', typeClass(payment.type)]">
                {{ translateType(payment.type) }}
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
      v-backdrop-close="() => (selectedPayment = null)"
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
            <dd class="text-sm text-gray-700">{{ translateType(selectedPayment.type) }}</dd>
          </div>
          <div class="flex justify-between">
            <dt class="text-sm text-gray-500">{{ t('admin.financials.amount') }}</dt>
            <dd class="text-sm font-medium text-gray-900">{{ formatNOK(selectedPayment.amount) }}</dd>
          </div>
          <div class="flex justify-between">
            <dt class="text-sm text-gray-500">{{ t('common.status') }}</dt>
            <dd>
              <span :class="['inline-flex rounded-full px-2 py-0.5 text-xs font-medium', statusClass(selectedPayment.status)]">
                {{ translateStatus(selectedPayment.status) }}
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
