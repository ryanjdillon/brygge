<script setup lang="ts">
import { ref, computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useFinancialSummary, importUni24CSV, type Uni24ImportResult } from '@/composables/useFinancials'
import { useFreshTotp } from '@/composables/useFreshTotp'
import {
  Banknote,
  Clock,
  AlertTriangle,
  TrendingUp,
  CreditCard,
  FileDown,
  FilePlus,
  FileUp,
  X,
} from 'lucide-vue-next'
import Select from '@/components/ui/form/Select.vue'

const { t } = useI18n()
const freshTotp = useFreshTotp()

const currentYear = new Date().getFullYear()
const selectedYear = ref<number | undefined>(undefined)

const ALL_YEARS = 0
const selectedYearValue = computed<number>({
  get: () => selectedYear.value ?? ALL_YEARS,
  set: (v) => {
    selectedYear.value = v === ALL_YEARS ? undefined : v
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
      label: t('admin.financials.harborMembershipCollected'),
      value: formatNOK(summary.value.total_harbor_membership_collected),
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

// Uni24 import modal
const showImportModal = ref(false)
const importFile = ref<File | null>(null)
const importDateFrom = ref(`${currentYear - 1}-01-01`)
const importDateTo = ref(`${currentYear}-12-31`)
const importLoading = ref(false)
const importError = ref<string | null>(null)
const importResult = ref<Uni24ImportResult | null>(null)

async function openImportModal() {
  const ok = await freshTotp.ensureFreshTotp()
  if (!ok) return
  importFile.value = null
  importError.value = null
  importResult.value = null
  showImportModal.value = true
}

function onFileChange(e: Event) {
  const input = e.target as HTMLInputElement
  importFile.value = input.files?.[0] ?? null
}

async function runImport() {
  if (!importFile.value) return
  importLoading.value = true
  importError.value = null
  importResult.value = null
  try {
    importResult.value = await importUni24CSV(importFile.value, importDateFrom.value, importDateTo.value)
  } catch (e) {
    importError.value = e instanceof Error ? e.message : String(e)
  } finally {
    importLoading.value = false
  }
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.financials.title') }}</h1>
      <Select v-model="selectedYearValue" :options="yearOptions" width="content" />
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

        <button
          class="group flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-4 transition hover:border-indigo-300 hover:shadow-sm"
          @click="openImportModal"
        >
          <FileUp class="h-5 w-5 text-indigo-600 group-hover:text-indigo-700" />
          <span class="text-sm font-medium text-gray-700 group-hover:text-gray-900">
            {{ t('admin.financials.importUni24') }}
          </span>
        </button>
      </div>
    </template>

    <!-- Uni24 import modal -->
    <div
      v-if="showImportModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      role="dialog"
      aria-modal="true"
      @keydown.esc="showImportModal = false"
    >
      <div class="w-full max-w-lg rounded-lg bg-white p-6 shadow-xl">
        <div class="flex items-center justify-between border-b border-gray-100 pb-4">
          <h2 class="text-base font-semibold text-gray-900">
            {{ t('admin.financials.importUni24Title') }}
          </h2>
          <button class="text-gray-400 hover:text-gray-600" @click="showImportModal = false">
            <X class="h-5 w-5" />
          </button>
        </div>

        <div v-if="!importResult" class="mt-4 space-y-4">
          <p class="text-sm text-gray-600">{{ t('admin.financials.importUni24Desc') }}</p>

          <div>
            <label class="mb-1 block text-sm font-medium text-gray-700">
              {{ t('admin.financials.importFile') }}
            </label>
            <input
              type="file"
              accept=".csv"
              class="block w-full text-sm text-gray-600 file:mr-3 file:rounded file:border file:border-gray-300 file:bg-white file:px-3 file:py-1 file:text-sm file:font-medium hover:file:bg-gray-50"
              @change="onFileChange"
            />
          </div>

          <div class="grid grid-cols-2 gap-3">
            <div>
              <label class="mb-1 block text-sm font-medium text-gray-700">
                {{ t('admin.financials.importDateFrom') }}
              </label>
              <input
                v-model="importDateFrom"
                type="date"
                class="w-full rounded-md border border-gray-300 px-3 py-1.5 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div>
              <label class="mb-1 block text-sm font-medium text-gray-700">
                {{ t('admin.financials.importDateTo') }}
              </label>
              <input
                v-model="importDateTo"
                type="date"
                class="w-full rounded-md border border-gray-300 px-3 py-1.5 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          </div>

          <p v-if="importError" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">
            {{ importError }}
          </p>

          <div class="flex justify-end gap-2 pt-2">
            <button
              class="rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
              @click="showImportModal = false"
            >
              {{ t('common.cancel') }}
            </button>
            <button
              class="rounded-md bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50"
              :disabled="!importFile || importLoading"
              @click="runImport"
            >
              {{ importLoading ? t('common.loading') : t('admin.financials.importRun') }}
            </button>
          </div>
        </div>

        <div v-else class="mt-4 space-y-4">
          <div class="rounded-md bg-green-50 px-4 py-3 text-sm text-green-800">
            {{ t('admin.financials.importDone', { imported: importResult.imported, skipped: importResult.skipped }) }}
          </div>

          <div v-if="importResult.rows.length" class="max-h-64 overflow-y-auto rounded border border-gray-200">
            <table class="w-full text-xs">
              <thead class="bg-gray-50 text-gray-500">
                <tr>
                  <th class="px-3 py-2 text-left">{{ t('admin.financials.importColId') }}</th>
                  <th class="px-3 py-2 text-left">{{ t('admin.financials.importColName') }}</th>
                  <th class="px-3 py-2 text-left">{{ t('admin.financials.importColStatus') }}</th>
                  <th class="px-3 py-2 text-left">{{ t('admin.financials.importColError') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100">
                <tr
                  v-for="row in importResult.rows"
                  :key="row.row"
                  :class="row.status === 'error' ? 'bg-red-50' : ''"
                >
                  <td class="px-3 py-1.5 font-mono">{{ row.external_id }}</td>
                  <td class="px-3 py-1.5">{{ row.name }}</td>
                  <td class="px-3 py-1.5">{{ row.status }}</td>
                  <td class="px-3 py-1.5 text-red-600">{{ row.error }}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div class="flex justify-end">
            <button
              class="rounded-md bg-gray-100 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-200"
              @click="showImportModal = false"
            >
              {{ t('common.close') }}
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
