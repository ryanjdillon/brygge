<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useFinancialSummary } from '@/composables/useFinancials'
import {
  BookOpen,
  CalendarDays,
  FileText,
  RefreshCw,
  Plus,
  Banknote,
  Clock,
  AlertTriangle,
  TrendingUp,
  CreditCard,
  FileDown,
  FilePlus,
} from 'lucide-vue-next'
import {
  useAccountsList,
  useFiscalPeriods,
  useJournalEntries,
  useSeedAccounts,
  useCreatePeriod,
  useSyncPayments,
  useSyncInvoices,
} from '@/composables/useAccounting'

const { t } = useI18n()

// Financial summary
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
const { data: summary, isLoading: summaryLoading } = useFinancialSummary(selectedYear)

function formatNOK(amount: number): string {
  return new Intl.NumberFormat('nb-NO', { style: 'currency', currency: 'NOK' }).format(amount)
}

const financeCards = computed(() => {
  if (!summary.value) return []
  return [
    { label: t('admin.financials.duesReceived'), value: formatNOK(summary.value.total_dues_received), icon: Banknote, color: 'text-green-600', bg: 'bg-green-50' },
    { label: t('admin.financials.outstanding'), value: formatNOK(summary.value.total_outstanding), icon: Clock, color: 'text-yellow-600', bg: 'bg-yellow-50' },
    { label: t('admin.financials.overdue'), value: formatNOK(summary.value.total_overdue), icon: AlertTriangle, color: 'text-red-600', bg: 'bg-red-50' },
    { label: t('admin.financials.harborMembershipCollected'), value: formatNOK(summary.value.total_harbor_membership_collected), icon: TrendingUp, color: 'text-blue-600', bg: 'bg-blue-50' },
    { label: t('admin.financials.bookingRevenue'), value: formatNOK(summary.value.total_booking_revenue), icon: CreditCard, color: 'text-purple-600', bg: 'bg-purple-50' },
  ]
})

// Accounting
const { data: accounts } = useAccountsList()
const { data: periods, isLoading: periodsLoading } = useFiscalPeriods()
const seedMutation = useSeedAccounts()
const createPeriodMutation = useCreatePeriod()
const syncPaymentsMutation = useSyncPayments()
const syncInvoicesMutation = useSyncInvoices()

const selectedPeriodId = ref('')
const syncMessage = ref('')

watch(periods, (val) => {
  if (val && val.length > 0 && !selectedPeriodId.value) {
    const open = val.find(p => p.status === 'open')
    selectedPeriodId.value = open?.id ?? val[0].id
  }
}, { immediate: true })

const { data: entries } = useJournalEntries(selectedPeriodId)

const totalAccounts = computed(() => accounts.value?.length ?? 0)
const totalEntries = computed(() => entries.value?.length ?? 0)
const postedCount = computed(() => entries.value?.filter(e => e.status === 'posted').length ?? 0)
const hasAccounts = computed(() => totalAccounts.value > 0)
const hasPeriods = computed(() => (periods.value?.length ?? 0) > 0)
const newYear = ref(currentYear)

function handleSeedAccounts() {
  seedMutation.mutate(undefined)
}

function handleCreatePeriod() {
  createPeriodMutation.mutate({ year: newYear.value })
}

async function handleSyncPayments() {
  syncMessage.value = ''
  syncPaymentsMutation.mutate({ period_id: selectedPeriodId.value }, {
    onSuccess: (data) => {
      syncMessage.value = `${t('admin.accounting.dashboard.syncPayments')} ${t('admin.accounting.dashboard.synced')}: ${data.synced}`
    },
    onError: (err) => {
      syncMessage.value = `${t('common.error')}: ${(err as Error).message}`
    },
  })
}

async function handleSyncInvoices() {
  syncMessage.value = ''
  syncInvoicesMutation.mutate({ period_id: selectedPeriodId.value }, {
    onSuccess: (data) => {
      syncMessage.value = `${t('admin.accounting.dashboard.syncInvoices')} ${t('admin.accounting.dashboard.synced')}: ${data.synced}`
    },
    onError: (err) => {
      syncMessage.value = `${t('common.error')}: ${(err as Error).message}`
    },
  })
}
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.accounting.dashboard.title') }}</h1>

    <!-- Financial Summary -->
    <div class="mt-6">
      <div class="flex items-center justify-between">
        <h2 class="text-lg font-semibold text-gray-700">{{ t('admin.financials.title') }}</h2>
        <select
          v-model="selectedYear"
          class="rounded-md border border-gray-300 px-3 py-1.5 text-sm"
        >
          <option v-for="opt in yearOptions" :key="String(opt.value)" :value="opt.value">{{ opt.label }}</option>
        </select>
      </div>

      <div v-if="summaryLoading" class="mt-4 text-sm text-gray-500">{{ t('common.loading') }}...</div>
      <div v-else-if="financeCards.length" class="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5">
        <div
          v-for="card in financeCards"
          :key="card.label"
          class="rounded-lg border border-gray-200 bg-white p-4"
        >
          <div class="flex items-center gap-2">
            <div :class="['rounded-md p-1.5', card.bg]">
              <component :is="card.icon" :class="['h-4 w-4', card.color]" />
            </div>
            <p class="text-xs font-medium text-gray-500">{{ card.label }}</p>
          </div>
          <p class="mt-2 text-lg font-semibold text-gray-900">{{ card.value }}</p>
        </div>
      </div>

      <div class="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <RouterLink
          to="/admin/financials/payments"
          class="group flex items-center gap-2 rounded-lg border border-gray-200 bg-white p-3 text-sm transition hover:border-blue-300"
        >
          <Banknote class="h-4 w-4 text-blue-600" />
          <span class="font-medium text-gray-700">{{ t('admin.financials.viewPayments') }}</span>
        </RouterLink>
        <RouterLink
          to="/admin/financials/overdue"
          class="group flex items-center gap-2 rounded-lg border border-gray-200 bg-white p-3 text-sm transition hover:border-red-300"
        >
          <AlertTriangle class="h-4 w-4 text-red-600" />
          <span class="font-medium text-gray-700">{{ t('admin.financials.viewOverdue') }}</span>
        </RouterLink>
        <RouterLink
          to="/admin/financials/invoices/new"
          class="group flex items-center gap-2 rounded-lg border border-gray-200 bg-white p-3 text-sm transition hover:border-green-300"
        >
          <FilePlus class="h-4 w-4 text-green-600" />
          <span class="font-medium text-gray-700">{{ t('admin.financials.createInvoice') }}</span>
        </RouterLink>
        <RouterLink
          to="/admin/financials/payments?export=1"
          class="group flex items-center gap-2 rounded-lg border border-gray-200 bg-white p-3 text-sm transition hover:border-gray-400"
        >
          <FileDown class="h-4 w-4 text-gray-600" />
          <span class="font-medium text-gray-700">{{ t('admin.financials.exportCSV') }}</span>
        </RouterLink>
      </div>
    </div>

    <!-- Divider -->
    <hr class="my-8 border-gray-200" />

    <!-- Accounting Section -->
    <div v-if="periodsLoading" class="text-sm text-gray-500">{{ t('common.loading') }}...</div>

    <template v-else>
      <div v-if="!hasPeriods" class="rounded-lg border border-dashed border-gray-300 p-8 text-center">
        <CalendarDays class="mx-auto h-12 w-12 text-gray-400" />
        <h3 class="mt-2 text-sm font-semibold text-gray-900">{{ t('admin.accounting.dashboard.noPeriods') }}</h3>
        <p class="mt-1 text-sm text-gray-500">{{ t('admin.accounting.dashboard.noPeriodsDesc') }}</p>
        <div class="mt-4 flex items-center justify-center gap-2">
          <input v-model.number="newYear" type="number" min="2000" max="2100" class="w-24 rounded-md border border-gray-300 px-3 py-2 text-sm" />
          <button
            class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
            :disabled="createPeriodMutation.isPending.value"
            @click="handleCreatePeriod"
          >
            <Plus class="h-4 w-4" />
            {{ t('admin.accounting.dashboard.createYear') }}
          </button>
        </div>
      </div>

      <div v-if="!hasAccounts && hasPeriods" class="mt-4 rounded-lg border border-dashed border-gray-300 p-6 text-center">
        <BookOpen class="mx-auto h-10 w-10 text-gray-400" />
        <p class="mt-2 text-sm text-gray-500">{{ t('admin.accounting.accounts.seedButton') }}</p>
        <button
          class="mt-3 inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
          :disabled="seedMutation.isPending.value"
          @click="handleSeedAccounts"
        >
          {{ t('admin.accounting.accounts.seedButton') }}
        </button>
      </div>

      <template v-if="hasPeriods">
        <div class="flex items-center justify-between">
          <h2 class="text-lg font-semibold text-gray-700">{{ t('admin.accounting.title') }}</h2>
          <div class="flex items-center gap-3">
            <label class="text-sm text-gray-500">{{ t('admin.accounting.journal.period') }}:</label>
            <select v-model="selectedPeriodId" class="rounded-md border border-gray-300 px-3 py-1.5 text-sm">
              <option v-for="p in periods" :key="p.id" :value="p.id">
                {{ p.year }} ({{ p.status === 'open' ? t('admin.accounting.periods.open') : t('admin.accounting.periods.closed') }})
              </option>
            </select>
          </div>
        </div>

        <div class="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div class="rounded-lg border border-gray-200 bg-white p-5">
            <p class="text-sm font-medium text-gray-500">{{ t('admin.accounting.dashboard.accounts') }}</p>
            <p class="mt-1 text-2xl font-semibold text-gray-900">{{ totalAccounts }}</p>
          </div>
          <div class="rounded-lg border border-gray-200 bg-white p-5">
            <p class="text-sm font-medium text-gray-500">{{ t('admin.accounting.dashboard.totalEntries') }}</p>
            <p class="mt-1 text-2xl font-semibold text-gray-900">{{ totalEntries }}</p>
          </div>
          <div class="rounded-lg border border-gray-200 bg-white p-5">
            <p class="text-sm font-medium text-gray-500">{{ t('admin.accounting.dashboard.posted') }}</p>
            <p class="mt-1 text-2xl font-semibold text-gray-900">{{ postedCount }}</p>
          </div>
        </div>

        <div class="mt-4 flex flex-wrap gap-3">
          <button
            class="inline-flex items-center gap-1.5 rounded-md bg-green-600 px-4 py-2 text-sm font-medium text-white hover:bg-green-700 disabled:opacity-50"
            :disabled="syncPaymentsMutation.isPending.value || !selectedPeriodId"
            @click="handleSyncPayments"
          >
            <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': syncPaymentsMutation.isPending.value }" />
            {{ t('admin.accounting.dashboard.syncPayments') }}
          </button>
          <button
            class="inline-flex items-center gap-1.5 rounded-md bg-green-600 px-4 py-2 text-sm font-medium text-white hover:bg-green-700 disabled:opacity-50"
            :disabled="syncInvoicesMutation.isPending.value || !selectedPeriodId"
            @click="handleSyncInvoices"
          >
            <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': syncInvoicesMutation.isPending.value }" />
            {{ t('admin.accounting.dashboard.syncInvoices') }}
          </button>
        </div>

        <p v-if="syncMessage" class="mt-2 text-sm" :class="syncMessage.startsWith(t('common.error')) ? 'text-red-600' : 'text-green-600'">
          {{ syncMessage }}
        </p>

        <div class="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-3">
          <RouterLink to="/admin/accounting/accounts" class="flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-5 transition hover:border-blue-300 hover:shadow-sm">
            <BookOpen class="h-8 w-8 text-blue-600" />
            <div>
              <p class="font-medium text-gray-900">{{ t('admin.accounting.dashboard.navAccounts') }}</p>
              <p class="text-sm text-gray-500">{{ t('admin.accounting.dashboard.navAccountsDesc') }}</p>
            </div>
          </RouterLink>
          <RouterLink to="/admin/accounting/journal" class="flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-5 transition hover:border-blue-300 hover:shadow-sm">
            <FileText class="h-8 w-8 text-blue-600" />
            <div>
              <p class="font-medium text-gray-900">{{ t('admin.accounting.dashboard.navJournal') }}</p>
              <p class="text-sm text-gray-500">{{ t('admin.accounting.dashboard.navJournalDesc') }}</p>
            </div>
          </RouterLink>
          <RouterLink to="/admin/accounting/periods" class="flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-5 transition hover:border-blue-300 hover:shadow-sm">
            <CalendarDays class="h-8 w-8 text-blue-600" />
            <div>
              <p class="font-medium text-gray-900">{{ t('admin.accounting.dashboard.navPeriods') }}</p>
              <p class="text-sm text-gray-500">{{ t('admin.accounting.dashboard.navPeriodsDesc') }}</p>
            </div>
          </RouterLink>
        </div>
      </template>
    </template>
  </div>
</template>
