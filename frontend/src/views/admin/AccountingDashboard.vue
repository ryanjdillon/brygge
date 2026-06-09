<script setup lang="ts">
import { ref, computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useFinancialSummary, usePriceItemSummary, type PriceItemSummaryRow } from '@/composables/useFinancials'
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
  Receipt,
  Settings,
  Upload,
} from 'lucide-vue-next'
import {
  useAccountsList,
  useJournalEntries,
} from '@/composables/useAccounting'
import { usePricing } from '@/composables/usePricing'
import Select from '@/components/ui/form/Select.vue'

const { t } = useI18n()
const { categoryLabel, unitLabel } = usePricing()

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
const { data: summary, isLoading: summaryLoading } = useFinancialSummary(selectedYear)
const { data: priceItemSummary, isLoading: priceItemLoading } = usePriceItemSummary(selectedYear)

type CategoryGroup = { category: string; items: PriceItemSummaryRow[]; subtotals: { billed: number; received: number; overdue: number; outstanding: number } }
const priceItemGroups = computed<CategoryGroup[]>(() => {
  const items = priceItemSummary.value?.items ?? []
  const byCat = new Map<string, CategoryGroup>()
  for (const it of items) {
    let g = byCat.get(it.category)
    if (!g) {
      g = { category: it.category, items: [], subtotals: { billed: 0, received: 0, overdue: 0, outstanding: 0 } }
      byCat.set(it.category, g)
    }
    g.items.push(it)
    g.subtotals.billed += it.billed
    g.subtotals.received += it.received
    g.subtotals.overdue += it.overdue
    g.subtotals.outstanding += it.outstanding
  }
  return [...byCat.values()]
})

function formatNOK(amount: number): string {
  return new Intl.NumberFormat('nb-NO', { style: 'currency', currency: 'NOK' }).format(amount)
}

// Renders a small secondary label distinguishing rows that share the
// same description in the same category — e.g. five SLIP_FEE rows all
// titled "Årlig plassleie basert på båtbredde". Falls back to the
// row's name when the name differs from the description; otherwise
// shows the unit price so the operator can still tell rows apart.
function rowSubLabel(row: PriceItemSummaryRow): string {
  if (row.name && row.name !== row.description) {
    return row.name
  }
  if (row.amount > 0) {
    return `${formatNOK(row.amount)} ${unitLabel(row.unit)}`
  }
  return ''
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
        <Select v-model="selectedYearValue" :options="yearOptions" width="content" />
      </div>

      <!-- Per-price-item totals (faktura side — works even without Vipps) -->
      <div class="mt-4">
        <p v-if="priceItemLoading" class="text-sm text-gray-500">{{ t('common.loading') }}...</p>
        <template v-else-if="priceItemSummary && priceItemSummary.items.length">
          <!-- Headline totals across all price items -->
          <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <div class="rounded-lg border border-gray-200 bg-white p-5">
              <div class="flex items-center gap-2">
                <div class="rounded-md bg-blue-50 p-1.5"><Receipt class="h-4 w-4 text-blue-600" /></div>
                <p class="text-xs font-medium text-gray-500">{{ t('admin.financials.totalBilled') }}</p>
              </div>
              <p class="mt-2 text-lg font-semibold text-gray-900">{{ formatNOK(priceItemSummary.totals.billed) }}</p>
            </div>
            <div class="rounded-lg border border-gray-200 bg-white p-5">
              <div class="flex items-center gap-2">
                <div class="rounded-md bg-green-50 p-1.5"><Banknote class="h-4 w-4 text-green-600" /></div>
                <p class="text-xs font-medium text-gray-500">{{ t('admin.financials.totalReceived') }}</p>
              </div>
              <p class="mt-2 text-lg font-semibold text-gray-900">{{ formatNOK(priceItemSummary.totals.received) }}</p>
            </div>
            <div class="rounded-lg border border-gray-200 bg-white p-5">
              <div class="flex items-center gap-2">
                <div class="rounded-md bg-yellow-50 p-1.5"><Clock class="h-4 w-4 text-yellow-600" /></div>
                <p class="text-xs font-medium text-gray-500">{{ t('admin.financials.totalOutstanding') }}</p>
              </div>
              <p class="mt-2 text-lg font-semibold text-gray-900">{{ formatNOK(priceItemSummary.totals.outstanding) }}</p>
            </div>
            <div class="rounded-lg border border-gray-200 bg-white p-5">
              <div class="flex items-center gap-2">
                <div class="rounded-md bg-red-50 p-1.5"><AlertTriangle class="h-4 w-4 text-red-600" /></div>
                <p class="text-xs font-medium text-gray-500">{{ t('admin.financials.totalForfall') }}</p>
              </div>
              <p class="mt-2 text-lg font-semibold text-gray-900">{{ formatNOK(priceItemSummary.totals.overdue) }}</p>
            </div>
          </div>

          <!-- Per-item breakdown, grouped by category -->
          <div class="mt-4 overflow-x-auto rounded-lg border border-gray-200 bg-white">
            <table class="min-w-full divide-y divide-gray-200 text-sm">
              <thead class="bg-gray-50 text-left text-xs font-medium uppercase tracking-wide text-gray-500">
                <tr>
                  <th class="px-4 py-2">{{ t('admin.financials.priceItem') }}</th>
                  <th class="px-4 py-2 text-right">{{ t('admin.financials.invoices') }}</th>
                  <th class="px-4 py-2 text-right">{{ t('admin.financials.billed') }}</th>
                  <th class="px-4 py-2 text-right">{{ t('admin.financials.received') }}</th>
                  <th class="px-4 py-2 text-right">{{ t('admin.financials.outstanding') }}</th>
                  <th class="px-4 py-2 text-right">{{ t('admin.financials.forfall') }}</th>
                </tr>
              </thead>
              <tbody>
                <template v-for="g in priceItemGroups" :key="g.category">
                  <tr class="bg-gray-50/50">
                    <td colspan="6" class="px-4 py-1.5 text-xs font-semibold tracking-wide text-gray-700">{{ categoryLabel(g.category) }}</td>
                  </tr>
                  <tr v-for="row in g.items" :key="row.price_item_id" class="border-t border-gray-100">
                    <td class="px-4 py-2 text-gray-900">
                      <div>{{ row.description }}</div>
                      <div v-if="rowSubLabel(row)" class="mt-0.5 text-xs text-gray-500">{{ rowSubLabel(row) }}</div>
                    </td>
                    <td class="px-4 py-2 text-right tabular-nums text-gray-600">{{ row.invoice_count }}</td>
                    <td class="px-4 py-2 text-right tabular-nums">{{ formatNOK(row.billed) }}</td>
                    <td class="px-4 py-2 text-right tabular-nums text-green-700">{{ formatNOK(row.received) }}</td>
                    <td class="px-4 py-2 text-right tabular-nums text-yellow-700">{{ formatNOK(row.outstanding) }}</td>
                    <td class="px-4 py-2 text-right tabular-nums" :class="row.overdue > 0 ? 'text-red-700 font-medium' : 'text-gray-400'">{{ formatNOK(row.overdue) }}</td>
                  </tr>
                  <tr v-if="priceItemGroups.length > 1" class="border-t border-gray-200 bg-gray-50/30 text-xs font-medium text-gray-700">
                    <td class="px-4 py-1.5">{{ t('admin.financials.subtotal') }}</td>
                    <td />
                    <td class="px-4 py-1.5 text-right tabular-nums">{{ formatNOK(g.subtotals.billed) }}</td>
                    <td class="px-4 py-1.5 text-right tabular-nums">{{ formatNOK(g.subtotals.received) }}</td>
                    <td class="px-4 py-1.5 text-right tabular-nums">{{ formatNOK(g.subtotals.outstanding) }}</td>
                    <td class="px-4 py-1.5 text-right tabular-nums">{{ formatNOK(g.subtotals.overdue) }}</td>
                  </tr>
                </template>
              </tbody>
            </table>
          </div>
        </template>
        <p v-else class="rounded-lg border border-dashed border-gray-300 bg-gray-50 px-4 py-6 text-center text-sm text-gray-500">
          {{ t('admin.financials.noPriceItemActivity') }}
        </p>
      </div>

      <!-- Legacy commerce-side cards (Vipps payments table). Kept for clubs
           that use Vipps integration; will read zero otherwise. -->
      <div v-if="summaryLoading" class="mt-4 text-sm text-gray-500">{{ t('common.loading') }}...</div>
      <details v-else-if="financeCards.length" class="mt-6">
        <summary class="cursor-pointer text-xs uppercase tracking-wide text-gray-500">{{ t('admin.financials.vippsBreakdown') }}</summary>
        <div class="mt-3 grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5">
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
      </details>

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
      <h2 class="text-lg font-semibold text-gray-700">{{ t('admin.accounting.dashboard.toolsHeading') }}</h2>
      <p class="mt-2 text-sm text-gray-500">
        {{ totalAccounts }} {{ t('admin.accounting.dashboard.accounts').toLowerCase() }}
        &bull; {{ totalEntries }} {{ t('admin.accounting.dashboard.totalEntries').toLowerCase() }}
        &bull; {{ postedCount }} {{ t('admin.accounting.dashboard.posted').toLowerCase() }}
      </p>

      <div class="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2">
        <RouterLink
          to="/admin/accounting/faktura"
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
        <RouterLink
          to="/admin/economy/settings"
          class="flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-5 transition hover:border-blue-300 hover:shadow-sm"
        >
          <Settings class="h-8 w-8 text-blue-600" />
          <div>
            <p class="font-medium text-gray-900">{{ t('admin.economySettings.title') }}</p>
            <p class="text-sm text-gray-500">{{ t('admin.economySettings.subtitle') }}</p>
          </div>
        </RouterLink>
        <RouterLink
          to="/admin/accounting/bank-imports"
          class="flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-5 transition hover:border-blue-300 hover:shadow-sm"
          data-testid="bank-imports-tile"
        >
          <Upload class="h-8 w-8 text-blue-600" />
          <div>
            <p class="font-medium text-gray-900">{{ t('admin.bankImports.navCardTitle') }}</p>
            <p class="text-sm text-gray-500">{{ t('admin.bankImports.navCardDesc') }}</p>
          </div>
        </RouterLink>
      </div>
    </div>
  </div>
</template>
