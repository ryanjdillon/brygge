<script setup lang="ts">
import { ref, computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useCashFlow, useFinancialSummary, usePriceItemSummary, useReservationsByMonth, type PriceItemSummaryRow } from '@/composables/useFinancials'
import DonutChart from '@/components/charts/DonutChart.vue'
import BarChart from '@/components/charts/BarChart.vue'
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
// Reservations chart always wants a concrete year; "all years" still
// renders the current calendar year — historic stacking can be a
// follow-up if anyone asks.
const reservationsYear = computed(() => selectedYear.value ?? currentYear)
const reservationsYearRef = computed({ get: () => reservationsYear.value, set: () => {} })
const { data: reservations } = useReservationsByMonth(reservationsYearRef)
const { data: cashFlow } = useCashFlow(reservationsYearRef)

const MONTH_LABELS = ['Jan', 'Feb', 'Mar', 'Apr', 'Mai', 'Jun', 'Jul', 'Aug', 'Sep', 'Okt', 'Nov', 'Des']

const reservationBuckets = computed(() =>
  (reservations.value?.buckets ?? []).map((b) => ({
    label: MONTH_LABELS[b.month - 1] ?? String(b.month),
    values: { guest_slip: b.guest_slip, motorhome: b.motorhome },
  })),
)
const reservationTotals = computed(() => {
  const acc = { guest_slip: 0, motorhome: 0 }
  for (const b of reservations.value?.buckets ?? []) {
    acc.guest_slip += b.guest_slip
    acc.motorhome += b.motorhome
  }
  return acc
})

const cashFlowBuckets = computed(() =>
  (cashFlow.value?.buckets ?? []).map((b) => ({
    label: MONTH_LABELS[b.month - 1] ?? String(b.month),
    values: { income: b.income, expense: b.expense },
  })),
)
const cashFlowTotals = computed(() => {
  const acc = { income: 0, expense: 0 }
  for (const b of cashFlow.value?.buckets ?? []) {
    acc.income += b.income
    acc.expense += b.expense
  }
  return acc
})
const cashFlowNet = computed(() => cashFlowTotals.value.income - cashFlowTotals.value.expense)

const fakturaDonutSlices = computed(() => {
  const totals = priceItemSummary.value?.totals
  if (!totals) return []
  const waiting = Math.max(0, totals.outstanding - totals.overdue)
  return [
    { label: t('admin.financials.statusPaid'), value: totals.received, color: '#16a34a' },
    { label: t('admin.financials.statusWaiting'), value: waiting, color: '#eab308' },
    { label: t('admin.financials.statusOverdue'), value: totals.overdue, color: '#dc2626' },
  ]
})

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

// Primary row label: prefer the concise per-tier name (e.g. "Plassleie
// 2.0–2.5m bredde") over the long generic description ("Årlig
// plassleie basert på båtbredde") that's identical across rows in the
// same category. Falls back to the description on legacy rows that
// never set a distinguishing name.
function rowLabel(row: PriceItemSummaryRow): string {
  return row.name?.trim() || row.description
}

// Right-aligned tertiary label showing the unit price — small enough
// to feel like metadata, but useful when the operator wants to check
// a tier rate without leaving the dashboard.
function rowUnitPrice(row: PriceItemSummaryRow): string {
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

          <!-- Visualizations -->
          <div class="mt-4 grid gap-4 lg:grid-cols-2 xl:grid-cols-3">
            <div class="rounded-lg border border-gray-200 bg-white p-5">
              <h3 class="text-sm font-semibold text-gray-700">{{ t('admin.financials.fakturaStatusTitle') }}</h3>
              <p class="mt-0.5 text-xs text-gray-500">{{ t('admin.financials.fakturaStatusHint') }}</p>
              <div class="mt-4">
                <DonutChart
                  :slices="fakturaDonutSlices"
                  :center-value="formatNOK(priceItemSummary.totals.billed)"
                  :center-label="t('admin.financials.totalBilled')"
                />
              </div>
            </div>
            <div class="rounded-lg border border-gray-200 bg-white p-5">
              <div class="flex items-center justify-between">
                <div>
                  <h3 class="text-sm font-semibold text-gray-700">{{ t('admin.financials.reservationsTitle') }}</h3>
                  <p class="mt-0.5 text-xs text-gray-500">{{ t('admin.financials.reservationsHint', { year: reservationsYear }) }}</p>
                </div>
                <p class="text-right text-xs text-gray-500">
                  <span class="block tabular-nums text-gray-900 font-semibold">{{ reservationTotals.guest_slip + reservationTotals.motorhome }}</span>
                  {{ t('admin.financials.reservationsTotal') }}
                </p>
              </div>
              <div class="mt-4">
                <BarChart
                  :buckets="reservationBuckets"
                  :series="[
                    { key: 'guest_slip', label: t('admin.financials.guestSlip'), color: '#2563eb' },
                    { key: 'motorhome', label: t('admin.financials.motorhome'), color: '#a855f7' },
                  ]"
                  :height="180"
                />
              </div>
            </div>
            <div class="rounded-lg border border-gray-200 bg-white p-5">
              <div class="flex items-start justify-between gap-3">
                <div>
                  <h3 class="text-sm font-semibold text-gray-700">{{ t('admin.financials.cashFlowTitle') }}</h3>
                  <p class="mt-0.5 text-xs text-gray-500">{{ t('admin.financials.cashFlowHint', { year: reservationsYear }) }}</p>
                </div>
                <div class="text-right text-xs text-gray-500">
                  <p :class="cashFlowNet >= 0 ? 'text-green-700' : 'text-red-700'" class="block text-sm font-semibold tabular-nums">{{ formatNOK(cashFlowNet) }}</p>
                  {{ t('admin.financials.cashFlowNet') }}
                </div>
              </div>
              <div class="mt-4">
                <BarChart
                  :buckets="cashFlowBuckets"
                  :series="[
                    { key: 'income', label: t('admin.financials.cashFlowIncome'), color: '#16a34a' },
                    { key: 'expense', label: t('admin.financials.cashFlowExpense'), color: '#dc2626' },
                  ]"
                  :height="180"
                  :value-formatter="formatNOK"
                />
              </div>
            </div>
          </div>

          <!-- Per-item breakdown, grouped by category -->
          <div class="mt-4 overflow-x-auto rounded-lg border border-gray-200 bg-white">
            <table class="min-w-full text-sm">
              <thead class="border-b border-gray-200 text-left text-xs font-medium text-gray-600">
                <tr>
                  <th class="px-4 py-3 font-medium">{{ t('admin.financials.priceItem') }}</th>
                  <th class="px-4 py-3 text-right font-medium">{{ t('admin.financials.invoices') }}</th>
                  <th class="px-4 py-3 text-right font-medium">{{ t('admin.financials.billed') }}</th>
                  <th class="px-4 py-3 text-right font-medium">{{ t('admin.financials.received') }}</th>
                  <th class="px-4 py-3 text-right font-medium">{{ t('admin.financials.outstanding') }}</th>
                  <th class="px-4 py-3 text-right font-medium">{{ t('admin.financials.forfall') }}</th>
                </tr>
              </thead>
              <tbody>
                <template v-for="g in priceItemGroups" :key="g.category">
                  <tr class="border-t border-gray-200 bg-blue-50/40">
                    <td class="px-4 py-2 text-sm font-semibold text-gray-900">
                      {{ categoryLabel(g.category) }}
                      <span class="ml-2 text-xs font-normal text-gray-500">{{ g.items.length }}</span>
                    </td>
                    <td colspan="5" />
                  </tr>
                  <tr v-for="row in g.items" :key="row.price_item_id" class="border-t border-gray-100">
                    <td class="px-4 py-2 text-gray-900">
                      <div class="flex items-baseline gap-3">
                        <span>{{ rowLabel(row) }}</span>
                        <span v-if="rowUnitPrice(row)" class="text-xs text-gray-400 tabular-nums">{{ rowUnitPrice(row) }}</span>
                      </div>
                    </td>
                    <td class="px-4 py-2 text-right tabular-nums" :class="row.invoice_count === 0 ? 'text-gray-300' : 'text-gray-600'">{{ row.invoice_count }}</td>
                    <td class="px-4 py-2 text-right tabular-nums" :class="row.billed === 0 ? 'text-gray-300' : 'text-gray-900'">{{ formatNOK(row.billed) }}</td>
                    <td class="px-4 py-2 text-right tabular-nums" :class="row.received === 0 ? 'text-gray-300' : 'text-green-700'">{{ formatNOK(row.received) }}</td>
                    <td class="px-4 py-2 text-right tabular-nums" :class="row.outstanding === 0 ? 'text-gray-300' : 'text-yellow-700'">{{ formatNOK(row.outstanding) }}</td>
                    <td class="px-4 py-2 text-right tabular-nums" :class="row.overdue === 0 ? 'text-gray-300' : 'text-red-700 font-medium'">{{ formatNOK(row.overdue) }}</td>
                  </tr>
                  <tr v-if="priceItemGroups.length > 1" class="border-t border-gray-200 bg-gray-50 text-xs">
                    <td class="px-4 py-2 italic font-medium text-gray-700">{{ t('admin.financials.subtotal') }}</td>
                    <td />
                    <td class="px-4 py-2 text-right font-semibold tabular-nums text-gray-900">{{ formatNOK(g.subtotals.billed) }}</td>
                    <td class="px-4 py-2 text-right font-semibold tabular-nums" :class="g.subtotals.received === 0 ? 'text-gray-400' : 'text-green-700'">{{ formatNOK(g.subtotals.received) }}</td>
                    <td class="px-4 py-2 text-right font-semibold tabular-nums" :class="g.subtotals.outstanding === 0 ? 'text-gray-400' : 'text-yellow-700'">{{ formatNOK(g.subtotals.outstanding) }}</td>
                    <td class="px-4 py-2 text-right font-semibold tabular-nums" :class="g.subtotals.overdue === 0 ? 'text-gray-400' : 'text-red-700'">{{ formatNOK(g.subtotals.overdue) }}</td>
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
