<script setup lang="ts">
import { ref, computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useFinancialSummary } from '@/composables/useFinancials'
import {
  BookOpen,
  FileText,
  Banknote,
  Clock,
  AlertTriangle,
  TrendingUp,
  CreditCard,
  FileDown,
  FilePlus,
  Mail,
} from 'lucide-vue-next'
import {
  useAccountsList,
  useJournalEntries,
} from '@/composables/useAccounting'

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

const { data: accounts } = useAccountsList()
const dummyPeriodId = ref('')
const { data: entries } = useJournalEntries(dummyPeriodId)

const totalAccounts = computed(() => accounts.value?.length ?? 0)
const totalEntries = computed(() => entries.value?.length ?? 0)
const postedCount = computed(() => entries.value?.filter(e => e.status === 'posted').length ?? 0)
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
          class="rounded-lg border border-gray-200 bg-white p-5"
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

    <!-- Accounting quick stats -->
    <div>
      <h2 class="text-lg font-semibold text-gray-700">{{ t('admin.accounting.title') }}</h2>
      <p class="mt-2 text-sm text-gray-500">
        {{ totalAccounts }} {{ t('admin.accounting.dashboard.accounts').toLowerCase() }}
        &bull; {{ totalEntries }} {{ t('admin.accounting.dashboard.totalEntries').toLowerCase() }}
        &bull; {{ postedCount }} {{ t('admin.accounting.dashboard.posted').toLowerCase() }}
      </p>

      <div class="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2">
        <RouterLink
          to="/admin/accounting/invoice-drafts"
          class="flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-5 transition hover:border-blue-300 hover:shadow-sm"
        >
          <Mail class="h-8 w-8 text-blue-600" />
          <div>
            <p class="font-medium text-gray-900">{{ t('admin.invoiceDrafts.title') }}</p>
            <p class="text-sm text-gray-500">{{ t('admin.invoiceDrafts.cardDesc') }}</p>
          </div>
        </RouterLink>
        <RouterLink
          to="/admin/accounting/accounts"
          class="flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-5 transition hover:border-blue-300 hover:shadow-sm"
        >
          <BookOpen class="h-8 w-8 text-blue-600" />
          <div>
            <p class="font-medium text-gray-900">{{ t('admin.accounting.dashboard.navAccounts') }}</p>
            <p class="text-sm text-gray-500">{{ t('admin.accounting.dashboard.navAccountsDesc') }}</p>
          </div>
        </RouterLink>
        <RouterLink
          to="/admin/accounting/journal"
          class="flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-5 transition hover:border-blue-300 hover:shadow-sm"
        >
          <FileText class="h-8 w-8 text-blue-600" />
          <div>
            <p class="font-medium text-gray-900">{{ t('admin.accounting.dashboard.navJournal') }}</p>
            <p class="text-sm text-gray-500">{{ t('admin.accounting.dashboard.navJournalDesc') }}</p>
          </div>
        </RouterLink>
      </div>
    </div>
  </div>
</template>
