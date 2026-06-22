<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery } from '@tanstack/vue-query'
import { useFiscalPeriods } from '@/composables/useAccounting'

const { t } = useI18n()

const BASE = '/api/v1/admin/accounting'

async function apiFetch<T>(path: string, opts?: RequestInit): Promise<T> {
  const res = await fetch(path, { credentials: 'include', ...opts })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

// ── State ─────────────────────────────────────────────────────────────────────

type Tab = 'income' | 'balance' | 'trial' | 'momskomp'
const activeTab = ref<Tab>('income')
const viewMode = ref<'accrual' | 'cash'>('accrual')

const { data: periods } = useFiscalPeriods()
const selectedPeriodId = ref('')

watch(periods, (val) => {
  if (val && val.length > 0 && !selectedPeriodId.value) {
    const open = val.find(p => p.status === 'open')
    selectedPeriodId.value = open?.id ?? val[0].id
  }
}, { immediate: true })


// ── Report types ──────────────────────────────────────────────────────────────

interface ReportLine {
  account_code: string
  account_name: string
  amount: number
}

interface IncomeStatement {
  year: number
  revenue: ReportLine[]
  expenses: ReportLine[]
  total_revenue: number
  total_expenses: number
  result: number
}

interface BalanceSheet {
  year: number
  assets: ReportLine[]
  liabilities: ReportLine[]
  total_assets: number
  total_liabilities: number
  is_balanced: boolean
}

interface TrialBalanceLine {
  account_code: string
  account_name: string
  debit: number
  credit: number
}

interface TrialBalance {
  year: number
  lines: TrialBalanceLine[]
  total_debit: number
  total_credit: number
  is_balanced: boolean
}

interface AccountBreakdown {
  account_code: string
  account_name: string
  total_amount: number
  mva_amount: number
  eligible_part: number
  eligibility: string
}

interface MomskompReport {
  year: number
  model: string
  total_operating_costs: number
  eligible_costs: number
  compensation_amount: number
  breakdown_by_account: AccountBreakdown[]
}

// ── Queries ───────────────────────────────────────────────────────────────────

const viewParam = computed(() => viewMode.value === 'cash' ? '&view=cash' : '')

const incomeQuery = useQuery({
  queryKey: computed(() => ['reports', 'income', selectedPeriodId.value, viewMode.value]),
  queryFn: () => apiFetch<IncomeStatement>(
    `${BASE}/reports/income-statement?period_id=${selectedPeriodId.value}${viewParam.value}`,
  ),
  enabled: computed(() => activeTab.value === 'income' && !!selectedPeriodId.value),
})

const balanceQuery = useQuery({
  queryKey: computed(() => ['reports', 'balance', selectedPeriodId.value, viewMode.value]),
  queryFn: () => apiFetch<BalanceSheet>(
    `${BASE}/reports/balance-sheet?period_id=${selectedPeriodId.value}${viewParam.value}`,
  ),
  enabled: computed(() => activeTab.value === 'balance' && !!selectedPeriodId.value),
})

const trialQuery = useQuery({
  queryKey: computed(() => ['reports', 'trial', selectedPeriodId.value, viewMode.value]),
  queryFn: () => apiFetch<TrialBalance>(
    `${BASE}/reports/trial-balance?period_id=${selectedPeriodId.value}${viewParam.value}`,
  ),
  enabled: computed(() => activeTab.value === 'trial' && !!selectedPeriodId.value),
})

const momskompQuery = useQuery({
  queryKey: computed(() => ['reports', 'momskomp', selectedPeriodId.value]),
  queryFn: () => apiFetch<MomskompReport>(
    `${BASE}/reports/momskomp?period_id=${selectedPeriodId.value}&model=simplified`,
  ),
  enabled: computed(() => activeTab.value === 'momskomp' && !!selectedPeriodId.value),
})

// ── Helpers ───────────────────────────────────────────────────────────────────

function fmt(n: number): string {
  return n.toLocaleString('nb-NO', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

function pdfUrl(report: 'income-statement' | 'balance-sheet'): string {
  return `${BASE}/reports/${report}/pdf?period_id=${selectedPeriodId.value}${viewParam.value}`
}

const tabs: { key: Tab; label: string }[] = [
  { key: 'income', label: 'admin.accounting.reports.income' },
  { key: 'balance', label: 'admin.accounting.reports.balance' },
  { key: 'trial', label: 'admin.accounting.reports.trial' },
  { key: 'momskomp', label: 'admin.accounting.reports.momskomp' },
]
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div>
        <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.accounting.reports.title') }}</h1>
        <p class="mt-1 text-sm text-gray-500">{{ t('admin.accounting.reports.subtitle') }}</p>
      </div>

      <!-- Period selector -->
      <div class="flex items-center gap-3">
        <label for="rpt-period" class="text-sm font-medium text-gray-700">
          {{ t('admin.accounting.journal.period') }}
        </label>
        <select
          id="rpt-period"
          v-model="selectedPeriodId"
          class="rounded-md border border-gray-300 px-3 py-1.5 text-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
        >
          <option v-for="p in periods" :key="p.id" :value="p.id">{{ p.year }}</option>
        </select>
      </div>
    </div>

    <!-- Cash / Accrual toggle -->
    <div class="flex items-center gap-2">
      <span class="text-sm font-medium text-gray-700">{{ t('admin.accounting.reports.viewMode') }}:</span>
      <div class="flex rounded-md border border-gray-300 overflow-hidden text-sm">
        <button
          :class="['px-3 py-1.5 transition', viewMode === 'accrual' ? 'bg-brand-600 text-white' : 'bg-white text-gray-700 hover:bg-gray-50']"
          @click="viewMode = 'accrual'"
        >{{ t('admin.accounting.reports.accrual') }}</button>
        <button
          :class="['px-3 py-1.5 border-l border-gray-300 transition', viewMode === 'cash' ? 'bg-brand-600 text-white' : 'bg-white text-gray-700 hover:bg-gray-50']"
          @click="viewMode = 'cash'"
        >{{ t('admin.accounting.reports.cash') }}</button>
      </div>
      <span v-if="viewMode === 'cash'" class="text-xs text-amber-700 bg-amber-50 px-2 py-0.5 rounded">
        {{ t('admin.accounting.reports.cashNote') }}
      </span>
    </div>

    <!-- Tab navigation -->
    <div class="border-b border-gray-200">
      <nav class="-mb-px flex gap-6">
        <button
          v-for="tab in tabs"
          :key="tab.key"
          :class="[
            'pb-3 text-sm font-medium transition border-b-2',
            activeTab === tab.key
              ? 'border-brand-600 text-brand-600'
              : 'border-transparent text-gray-500 hover:text-gray-700',
          ]"
          @click="activeTab = tab.key"
        >
          {{ t(tab.label) }}
        </button>
      </nav>
    </div>

    <!-- ── Income Statement ─────────────────────────────────────────────── -->
    <template v-if="activeTab === 'income'">
      <div v-if="incomeQuery.isLoading.value" class="animate-pulse space-y-2">
        <div v-for="i in 8" :key="i" class="h-8 rounded bg-gray-100" />
      </div>
      <div v-else-if="!incomeQuery.data.value" class="text-sm text-gray-500">
        {{ t('admin.accounting.reports.selectPeriod') }}
      </div>
      <template v-else>
        <div class="flex items-center justify-between">
          <h2 class="text-lg font-semibold text-gray-900">
            {{ t('admin.accounting.reports.income') }} {{ incomeQuery.data.value.year }}
          </h2>
          <a
            :href="pdfUrl('income-statement')"
            target="_blank"
            class="rounded-md border border-gray-300 px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-50"
          >
            {{ t('admin.accounting.reports.downloadPdf') }}
          </a>
        </div>
        <div class="rounded-lg border border-gray-200 overflow-hidden">
          <table class="w-full text-sm">
            <thead class="bg-gray-50 text-xs uppercase tracking-wide text-gray-500">
              <tr>
                <th class="px-4 py-2 text-left">{{ t('admin.accounting.reports.account') }}</th>
                <th class="px-4 py-2 text-right">{{ t('admin.accounting.reports.amount') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr class="bg-gray-50">
                <td colspan="2" class="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-gray-500">
                  {{ t('admin.accounting.reports.revenue') }}
                </td>
              </tr>
              <tr v-for="line in incomeQuery.data.value.revenue" :key="line.account_code" class="border-t border-gray-100">
                <td class="px-4 py-2 text-gray-700">
                  <span class="font-mono text-xs text-gray-400 mr-2">{{ line.account_code }}</span>{{ line.account_name }}
                </td>
                <td class="px-4 py-2 text-right font-mono">{{ fmt(line.amount) }}</td>
              </tr>
              <tr class="border-t border-gray-300 bg-gray-50 font-semibold">
                <td class="px-4 py-2">{{ t('admin.accounting.reports.totalRevenue') }}</td>
                <td class="px-4 py-2 text-right font-mono">{{ fmt(incomeQuery.data.value.total_revenue) }}</td>
              </tr>
              <tr class="bg-gray-50">
                <td colspan="2" class="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-gray-500">
                  {{ t('admin.accounting.reports.expenses') }}
                </td>
              </tr>
              <tr v-for="line in incomeQuery.data.value.expenses" :key="line.account_code" class="border-t border-gray-100">
                <td class="px-4 py-2 text-gray-700">
                  <span class="font-mono text-xs text-gray-400 mr-2">{{ line.account_code }}</span>{{ line.account_name }}
                </td>
                <td class="px-4 py-2 text-right font-mono">{{ fmt(line.amount) }}</td>
              </tr>
              <tr class="border-t border-gray-300 bg-gray-50 font-semibold">
                <td class="px-4 py-2">{{ t('admin.accounting.reports.totalExpenses') }}</td>
                <td class="px-4 py-2 text-right font-mono">{{ fmt(incomeQuery.data.value.total_expenses) }}</td>
              </tr>
              <tr class="border-t-2 border-gray-400 font-bold text-base">
                <td class="px-4 py-3">{{ t('admin.accounting.reports.result') }}</td>
                <td
                  :class="['px-4 py-3 text-right font-mono', incomeQuery.data.value.result >= 0 ? 'text-green-700' : 'text-red-700']"
                >
                  {{ fmt(incomeQuery.data.value.result) }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </template>

    <!-- ── Balance Sheet ────────────────────────────────────────────────── -->
    <template v-else-if="activeTab === 'balance'">
      <div v-if="balanceQuery.isLoading.value" class="animate-pulse space-y-2">
        <div v-for="i in 8" :key="i" class="h-8 rounded bg-gray-100" />
      </div>
      <div v-else-if="!balanceQuery.data.value" class="text-sm text-gray-500">
        {{ t('admin.accounting.reports.selectPeriod') }}
      </div>
      <template v-else>
        <div class="flex items-center justify-between">
          <h2 class="text-lg font-semibold text-gray-900">
            {{ t('admin.accounting.reports.balance') }} {{ balanceQuery.data.value.year }}
          </h2>
          <div class="flex items-center gap-3">
            <span v-if="!balanceQuery.data.value.is_balanced" class="text-xs text-red-600 font-medium">
              {{ t('admin.accounting.reports.unbalanced') }}
            </span>
            <a
              :href="pdfUrl('balance-sheet')"
              target="_blank"
              class="rounded-md border border-gray-300 px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-50"
            >
              {{ t('admin.accounting.reports.downloadPdf') }}
            </a>
          </div>
        </div>
        <div class="grid gap-6 sm:grid-cols-2">
          <!-- Assets -->
          <div class="rounded-lg border border-gray-200 overflow-hidden">
            <table class="w-full text-sm">
              <thead class="bg-gray-50">
                <tr>
                  <th colspan="2" class="px-4 py-2 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">
                    {{ t('admin.accounting.reports.assets') }}
                  </th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="line in balanceQuery.data.value.assets" :key="line.account_code" class="border-t border-gray-100">
                  <td class="px-4 py-2 text-gray-700">
                    <span class="font-mono text-xs text-gray-400 mr-2">{{ line.account_code }}</span>{{ line.account_name }}
                  </td>
                  <td class="px-4 py-2 text-right font-mono">{{ fmt(line.amount) }}</td>
                </tr>
                <tr class="border-t border-gray-300 bg-gray-50 font-semibold">
                  <td class="px-4 py-2">{{ t('admin.accounting.reports.totalAssets') }}</td>
                  <td class="px-4 py-2 text-right font-mono">{{ fmt(balanceQuery.data.value.total_assets) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <!-- Liabilities -->
          <div class="rounded-lg border border-gray-200 overflow-hidden">
            <table class="w-full text-sm">
              <thead class="bg-gray-50">
                <tr>
                  <th colspan="2" class="px-4 py-2 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">
                    {{ t('admin.accounting.reports.liabilities') }}
                  </th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="line in balanceQuery.data.value.liabilities" :key="line.account_code" class="border-t border-gray-100">
                  <td class="px-4 py-2 text-gray-700">
                    <span class="font-mono text-xs text-gray-400 mr-2">{{ line.account_code }}</span>{{ line.account_name }}
                  </td>
                  <td class="px-4 py-2 text-right font-mono">{{ fmt(line.amount) }}</td>
                </tr>
                <tr class="border-t border-gray-300 bg-gray-50 font-semibold">
                  <td class="px-4 py-2">{{ t('admin.accounting.reports.totalLiabilities') }}</td>
                  <td class="px-4 py-2 text-right font-mono">{{ fmt(balanceQuery.data.value.total_liabilities) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </template>
    </template>

    <!-- ── Trial Balance ────────────────────────────────────────────────── -->
    <template v-else-if="activeTab === 'trial'">
      <div v-if="trialQuery.isLoading.value" class="animate-pulse space-y-2">
        <div v-for="i in 10" :key="i" class="h-7 rounded bg-gray-100" />
      </div>
      <div v-else-if="!trialQuery.data.value" class="text-sm text-gray-500">
        {{ t('admin.accounting.reports.selectPeriod') }}
      </div>
      <template v-else>
        <div class="flex items-center justify-between">
          <h2 class="text-lg font-semibold text-gray-900">
            {{ t('admin.accounting.reports.trial') }} {{ trialQuery.data.value.year }}
          </h2>
          <span v-if="!trialQuery.data.value.is_balanced" class="text-xs text-red-600 font-medium">
            {{ t('admin.accounting.reports.unbalanced') }}
          </span>
        </div>
        <div class="rounded-lg border border-gray-200 overflow-hidden">
          <table class="w-full text-sm">
            <thead class="bg-gray-50 text-xs uppercase tracking-wide text-gray-500">
              <tr>
                <th class="px-4 py-2 text-left">{{ t('admin.accounting.reports.account') }}</th>
                <th class="px-4 py-2 text-right">{{ t('admin.accounting.reports.debit') }}</th>
                <th class="px-4 py-2 text-right">{{ t('admin.accounting.reports.credit') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="line in trialQuery.data.value.lines" :key="line.account_code" class="border-t border-gray-100">
                <td class="px-4 py-2 text-gray-700">
                  <span class="font-mono text-xs text-gray-400 mr-2">{{ line.account_code }}</span>{{ line.account_name }}
                </td>
                <td class="px-4 py-2 text-right font-mono">{{ line.debit ? fmt(line.debit) : '—' }}</td>
                <td class="px-4 py-2 text-right font-mono">{{ line.credit ? fmt(line.credit) : '—' }}</td>
              </tr>
              <tr class="border-t-2 border-gray-400 font-semibold bg-gray-50">
                <td class="px-4 py-2">{{ t('admin.accounting.reports.totals') }}</td>
                <td class="px-4 py-2 text-right font-mono">{{ fmt(trialQuery.data.value.total_debit) }}</td>
                <td class="px-4 py-2 text-right font-mono">{{ fmt(trialQuery.data.value.total_credit) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </template>

    <!-- ── Momskompensasjon ──────────────────────────────────────────────── -->
    <template v-else-if="activeTab === 'momskomp'">
      <div v-if="momskompQuery.isLoading.value" class="animate-pulse space-y-2">
        <div v-for="i in 6" :key="i" class="h-8 rounded bg-gray-100" />
      </div>
      <div v-else-if="!momskompQuery.data.value" class="text-sm text-gray-500">
        {{ t('admin.accounting.reports.selectPeriod') }}
      </div>
      <template v-else>
        <h2 class="text-lg font-semibold text-gray-900">
          {{ t('admin.accounting.reports.momskomp') }} {{ momskompQuery.data.value.year }}
        </h2>
        <div class="grid gap-4 sm:grid-cols-3">
          <div class="rounded-lg border border-gray-200 bg-white p-4">
            <p class="text-xs font-medium uppercase tracking-wide text-gray-500">{{ t('admin.accounting.reports.totalCosts') }}</p>
            <p class="mt-1 text-2xl font-bold text-gray-900">{{ fmt(momskompQuery.data.value.total_operating_costs) }}</p>
          </div>
          <div class="rounded-lg border border-gray-200 bg-white p-4">
            <p class="text-xs font-medium uppercase tracking-wide text-gray-500">{{ t('admin.accounting.reports.eligibleCosts') }}</p>
            <p class="mt-1 text-2xl font-bold text-gray-900">{{ fmt(momskompQuery.data.value.eligible_costs) }}</p>
          </div>
          <div class="rounded-lg border border-brand-200 bg-brand-50 p-4">
            <p class="text-xs font-medium uppercase tracking-wide text-brand-600">{{ t('admin.accounting.reports.compensation') }}</p>
            <p class="mt-1 text-2xl font-bold text-brand-700">{{ fmt(momskompQuery.data.value.compensation_amount) }}</p>
          </div>
        </div>
        <div class="rounded-lg border border-gray-200 overflow-hidden">
          <table class="w-full text-sm">
            <thead class="bg-gray-50 text-xs uppercase tracking-wide text-gray-500">
              <tr>
                <th class="px-4 py-2 text-left">{{ t('admin.accounting.reports.account') }}</th>
                <th class="px-4 py-2 text-right">{{ t('admin.accounting.reports.totalAmount') }}</th>
                <th class="px-4 py-2 text-right">MVA</th>
                <th class="px-4 py-2 text-right">{{ t('admin.accounting.reports.eligible') }}</th>
                <th class="px-4 py-2 text-center">{{ t('admin.accounting.reports.eligibility') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="line in momskompQuery.data.value.breakdown_by_account" :key="line.account_code" class="border-t border-gray-100">
                <td class="px-4 py-2 text-gray-700">
                  <span class="font-mono text-xs text-gray-400 mr-2">{{ line.account_code }}</span>{{ line.account_name }}
                </td>
                <td class="px-4 py-2 text-right font-mono">{{ fmt(line.total_amount) }}</td>
                <td class="px-4 py-2 text-right font-mono">{{ fmt(line.mva_amount) }}</td>
                <td class="px-4 py-2 text-right font-mono">{{ fmt(line.eligible_part) }}</td>
                <td class="px-4 py-2 text-center">
                  <span :class="[
                    'inline-flex rounded-full px-2 py-0.5 text-xs font-medium',
                    line.eligibility === 'full' ? 'bg-green-100 text-green-700'
                    : line.eligibility === 'partial' ? 'bg-yellow-100 text-yellow-700'
                    : 'bg-gray-100 text-gray-500',
                  ]">{{ line.eligibility }}</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </template>
  </div>
</template>
