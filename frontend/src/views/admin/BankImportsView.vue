<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQueryClient } from '@tanstack/vue-query'
import {
  useBankFormats,
  useBankImport,
  useBankImportsList,
  useBankRowsByAccount,
  useVippsImports,
  useVippsImport,
  useVippsRowsByMSN,
  uploadBankCSV,
  uploadVippsCSV,
  reassignBankImport,
  previewVippsReconcile,
  confirmVippsReconcile,
  type BankImportResult,
  type VippsImportResult,
  type VippsReconcilePreview,
} from '@/composables/useBankImports'
import { useAccountsList } from '@/composables/useAccounting'
import AccountSelect from '@/components/ui/AccountSelect.vue'
import Tabs from '@/components/ui/Tabs.vue'
import BankReconcileTab from '@/components/admin/BankReconcileTab.vue'
import { useBankUnmatchedCount } from '@/composables/useBankReconcile'
import BankRowsTable from '@/components/admin/BankRowsTable.vue'
import VippsRowsTable from '@/components/admin/VippsRowsTable.vue'
import Select from '@/components/ui/form/Select.vue'
import FileInput from '@/components/ui/form/FileInput.vue'
import { monthOptions } from '@/utils/month'
import type { BankImportRow } from '@/composables/useBankImports'
import { runBankSync, runVippsResync, type BankSyncResult, type VippsResyncResult } from '@/composables/useBankImports'
import { importUni24CSV, type Uni24ImportResult } from '@/composables/useFinancials'
import { formatNOK as nok, formatDate } from '@/lib/format'
import { useFreshTotp } from '@/composables/useFreshTotp'

const { t, locale } = useI18n()
const { ensureFreshTotp } = useFreshTotp()
const queryClient = useQueryClient()

const { data: formats } = useBankFormats()
const { data: vippsImports } = useVippsImports()
const { data: accounts } = useAccountsList()

// NS 4102 reserves codes 1920–1949 for bank deposits. 1900 is physical
// cash (kasse), not a bank account, so it must not appear here.
const bankAccounts = computed(() =>
  (accounts.value ?? []).filter((a) => {
    if (!a.is_active) return false
    const n = Number(a.code)
    return n >= 1920 && n <= 1949
  }),
)

// ── Bank upload ──────────────────────────────────────────────
const bankFile = ref<File | null>(null)
const bankFormat = ref<string>('sparebank-norge-v1')
const bankAccountCode = ref<string>('1920')
const bankError = ref<string | null>(null)
const bankBusy = ref(false)
const bankResult = ref<BankImportResult | null>(null)
const currentBankImportId = ref<string | undefined>(undefined)
const { data: bankRows } = useBankImport(currentBankImportId)

function onBankFile(files: FileList | null) {
  bankFile.value = files?.[0] ?? null
}

async function submitBank() {
  if (!bankFile.value || !bankAccountCode.value) return
  bankError.value = null
  bankBusy.value = true
  try {
    const res = await uploadBankCSV(bankFile.value, bankFormat.value, bankAccountCode.value)
    bankResult.value = res
    currentBankImportId.value = res.id
    queryClient.invalidateQueries({ queryKey: ['accounting', 'bank-imports'] })
  } catch (e: any) {
    bankError.value = e?.message ?? 'Upload failed'
  } finally {
    bankBusy.value = false
  }
}

// ── Vipps upload ─────────────────────────────────────────────
const vippsFile = ref<File | null>(null)
const vippsError = ref<string | null>(null)
const vippsBusy = ref(false)
const vippsResult = ref<VippsImportResult | null>(null)
const currentVippsImportId = ref<string | undefined>(undefined)
const { data: vippsRows } = useVippsImport(currentVippsImportId)

function onVippsFile(files: FileList | null) {
  vippsFile.value = files?.[0] ?? null
}

async function submitVipps() {
  if (!vippsFile.value) return
  vippsError.value = null
  vippsBusy.value = true
  try {
    const res = await uploadVippsCSV(vippsFile.value)
    vippsResult.value = res
    currentVippsImportId.value = res.id
    queryClient.invalidateQueries({ queryKey: ['accounting', 'vipps-imports'] })
  } catch (e: any) {
    vippsError.value = e?.message ?? 'Upload failed'
  } finally {
    vippsBusy.value = false
  }
}

// ── Uni24 import ────────────────────────────────────────────
const thisYear = new Date().getFullYear()
const uni24File = ref<File | null>(null)
const uni24DateFrom = ref(`${thisYear - 1}-01-01`)
const uni24DateTo = ref(`${thisYear}-12-31`)
const uni24Error = ref<string | null>(null)
const uni24Busy = ref(false)
const uni24Result = ref<Uni24ImportResult | null>(null)

function onUni24File(files: FileList | null) {
  uni24File.value = files?.[0] ?? null
  uni24Result.value = null
}

async function submitUni24() {
  if (!uni24File.value) return
  const ok = await ensureFreshTotp()
  if (!ok) return
  uni24Error.value = null
  uni24Busy.value = true
  try {
    uni24Result.value = await importUni24CSV(uni24File.value, uni24DateFrom.value, uni24DateTo.value)
  } catch (e: any) {
    uni24Error.value = e?.message ?? 'Import failed'
  } finally {
    uni24Busy.value = false
  }
}

// ── Reconciliation ──────────────────────────────────────────
const reconcilePreview = ref<VippsReconcilePreview | null>(null)
const reconcileError = ref<string | null>(null)
const reconcileBusy = ref(false)
const reconcileTarget = ref<{ id: string; description: string } | null>(null)

async function openReconcile(row: { id: string; description: string } | BankImportRow) {
  reconcileError.value = null
  reconcilePreview.value = null
  reconcileTarget.value = row
  reconcileBusy.value = true
  try {
    reconcilePreview.value = await previewVippsReconcile(row.id)
  } catch (e: any) {
    reconcileError.value = e?.message ?? 'Preview failed'
  } finally {
    reconcileBusy.value = false
  }
}

async function confirmReconcile() {
  if (!reconcilePreview.value || !reconcileTarget.value) return
  reconcileBusy.value = true
  try {
    await confirmVippsReconcile(reconcileTarget.value.id, reconcilePreview.value.lines)
    reconcileTarget.value = null
    reconcilePreview.value = null
    queryClient.invalidateQueries({ queryKey: ['accounting', 'bank-import', currentBankImportId.value] })
  } catch (e: any) {
    reconcileError.value = e?.message ?? 'Confirm failed'
  } finally {
    reconcileBusy.value = false
  }
}

// ── Tabs ────────────────────────────────────────────────────
const activeTab = ref<'imports' | 'accounts' | 'reconcile'>('imports')
const currentYear = ref(new Date().getFullYear())
const { data: unmatchedCount } = useBankUnmatchedCount(currentYear)
const tabs = computed(() => [
  { value: 'imports', label: t('admin.bankImports.tabImports') },
  { value: 'accounts', label: t('admin.bankImports.tabAccounts') },
  { value: 'reconcile', label: t('admin.bankImports.tabReconcile'), badge: unmatchedCount.value ?? 0 },
])

// ── Sync action ─────────────────────────────────────────────
const syncBusy = ref(false)
const vippsResyncBusy = ref(false)
const vippsResyncResult = ref<VippsResyncResult | null>(null)
const vippsResyncError = ref<string | null>(null)

async function runVippsResyncAction() {
  vippsResyncBusy.value = true
  vippsResyncError.value = null
  try {
    vippsResyncResult.value = await runVippsResync()
  } catch (e: any) {
    vippsResyncError.value = e?.message ?? 'Failed'
  } finally {
    vippsResyncBusy.value = false
  }
}
const syncResult = ref<BankSyncResult | null>(null)
const syncError = ref<string | null>(null)

async function runSync() {
  syncBusy.value = true
  syncError.value = null
  try {
    syncResult.value = await runBankSync()
    queryClient.invalidateQueries({ queryKey: ['accounting'] })
  } catch (e: any) {
    syncError.value = e?.message ?? 'Sync failed'
  } finally {
    syncBusy.value = false
  }
}

// ── Bank imports list ──────────────────────────────────────
const { data: bankImportsList } = useBankImportsList()

// ── Per-bank data freshness (DIL-371) ──────────────────────
// Most-recent import timestamp per bank account / Vipps MSN, so the
// operator can see at a glance how current each bank's data is.
// created_at is ISO 8601, so lexical max == chronological max.
const bankLastImport = computed(() => {
  const m = new Map<string, string>()
  for (const b of bankImportsList.value ?? []) {
    const cur = m.get(b.account_code)
    if (!cur || b.created_at > cur) m.set(b.account_code, b.created_at)
  }
  return m
})
const vippsFreshness = computed(() => {
  const m = new Map<string, string>()
  for (const v of vippsImports.value ?? []) {
    const cur = m.get(v.msn)
    if (!cur || v.created_at > cur) m.set(v.msn, v.created_at)
  }
  return [...m.entries()].map(([msn, lastUpdated]) => ({ msn, lastUpdated }))
})

function selectBankImport(id: string) {
  currentBankImportId.value = id
}

// ── Reassign misuploaded import ────────────────────────────
const reassignImportId = ref<string | null>(null)
const reassignFrom = ref('')
const reassignTo = ref('')
const reassignError = ref<string | null>(null)
const reassignBusy = ref(false)

function openReassign(id: string, currentCode: string) {
  reassignImportId.value = id
  reassignFrom.value = currentCode
  reassignTo.value = currentCode
  reassignError.value = null
}

function cancelReassign() {
  reassignImportId.value = null
}

async function submitReassign() {
  if (!reassignImportId.value || !reassignTo.value || reassignTo.value === reassignFrom.value) return
  reassignError.value = null
  reassignBusy.value = true
  try {
    await reassignBankImport(reassignImportId.value, reassignTo.value)
    queryClient.invalidateQueries({ queryKey: ['accounting', 'bank-imports'] })
    queryClient.invalidateQueries({ queryKey: ['accounting', 'bank-import', reassignImportId.value] })
    reassignImportId.value = null
  } catch (e: any) {
    reassignError.value = e?.message ?? 'Reassign failed'
  } finally {
    reassignBusy.value = false
  }
}

// ── Accounts tab data ───────────────────────────────────────
const vippsMSNs = computed(() => {
  const seen = new Set<string>()
  for (const v of vippsImports.value ?? []) {
    if (v.msn) seen.add(v.msn)
  }
  return [...seen].sort()
})

type AccountFilter = { kind: 'bank' | 'vipps' | 'none'; value: string }
const accountFilter = ref<AccountFilter>({ kind: 'none', value: '' })
const filterYear = ref<number>(new Date().getFullYear())
const filterMonth = ref<number | null>(null) // 1–12, null = whole year
const yearAutoSet = ref(false)
const userTouchedYear = ref(false)

// Pre-select the club's default faktura bank account so the Accounts tab
// shows transactions without requiring a manual pick first.
const defaultBankGlCode = ref('')
const accountAutoSet = ref(false)
const userTouchedAccount = ref(false)

onMounted(async () => {
  try {
    const res = await fetch('/api/v1/admin/settings/bank-accounts', { credentials: 'include' })
    if (!res.ok) return
    const list: { gl_code?: string; is_default_for_invoices?: boolean }[] = await res.json()
    const def = (list ?? []).find((a) => a.is_default_for_invoices)
    if (def?.gl_code) defaultBankGlCode.value = def.gl_code
  } catch {
    // Non-fatal: the user can still pick an account manually.
  }
})

const yearOptions = computed(() => {
  const current = new Date().getFullYear()
  const years = new Set<number>(Array.from({ length: 6 }, (_, i) => current - i))
  for (const v of vippsImports.value ?? []) {
    const y = Number(v.created_at?.slice(0, 4))
    if (Number.isFinite(y)) years.add(y)
  }
  for (const b of bankImportsList.value ?? []) {
    const y = Number(b.created_at?.slice(0, 4))
    if (Number.isFinite(y)) years.add(y)
  }
  return [...years].sort((a, b) => b - a)
})

const periodRange = computed(() => {
  const y = filterYear.value
  const m = filterMonth.value
  if (m) {
    const from = `${y}-${String(m).padStart(2, '0')}-01`
    const lastDay = new Date(y, m, 0).getDate()
    const to = `${y}-${String(m).padStart(2, '0')}-${String(lastDay).padStart(2, '0')}`
    return { from, to }
  }
  return { from: `${y}-01-01`, to: `${y}-12-31` }
})

const filterBankAccount = computed(() =>
  accountFilter.value.kind === 'bank' ? accountFilter.value.value : undefined,
)
const filterVippsMSN = computed(() =>
  accountFilter.value.kind === 'vipps' ? accountFilter.value.value : undefined,
)
const filterFrom = computed(() => periodRange.value.from)
const filterTo = computed(() => periodRange.value.to)

const { data: accountsBankRows } = useBankRowsByAccount(filterBankAccount, filterFrom, filterTo)
const { data: accountsVippsRows } = useVippsRowsByMSN(filterVippsMSN, filterFrom, filterTo)

function selectBankAccount(code: string) {
  userTouchedAccount.value = true
  accountFilter.value = code ? { kind: 'bank', value: code } : { kind: 'none', value: '' }
}
function selectVippsMSN(msn: string) {
  userTouchedAccount.value = true
  accountFilter.value = msn ? { kind: 'vipps', value: msn } : { kind: 'none', value: '' }
}

// Default to the faktura bank account once both its gl_code and the
// account list are loaded, unless the user has already chosen one.
watch(
  [defaultBankGlCode, bankAccounts],
  () => {
    if (accountAutoSet.value || userTouchedAccount.value) return
    if (accountFilter.value.kind !== 'none') return
    const code = defaultBankGlCode.value
    if (!code || !bankAccounts.value.some((a) => a.code === code)) return
    accountFilter.value = { kind: 'bank', value: code }
    accountAutoSet.value = true
  },
  { immediate: true },
)

function onUserYearChange(y: number) {
  userTouchedYear.value = true
  filterYear.value = y
}

watch(
  [vippsImports, bankImportsList],
  () => {
    if (userTouchedYear.value || yearAutoSet.value) return
    const years = yearOptions.value
    if (years.length === 0) return
    filterYear.value = years[0]
    yearAutoSet.value = true
  },
  { immediate: true },
)

const yearSelectOptions = computed(() =>
  yearOptions.value.map((y) => ({ value: y, label: String(y) })),
)
const vippsMSNOptions = computed(() => [
  { value: '', label: '—' },
  ...vippsMSNs.value.map((msn) => ({ value: msn, label: msn })),
])
const monthSelectOptions = computed(() => [
  { value: 0, label: t('admin.bankImports.allMonths') },
  ...monthOptions(locale.value, 'long').map((o) => ({
    ...o,
    label: o.label.charAt(0).toUpperCase() + o.label.slice(1),
  })),
])
const bankFormatOptions = computed(() =>
  (formats.value ?? []).map((f) => ({ value: f, label: f })),
)

const filterMonthValue = computed<number>({
  get: () => filterMonth.value ?? 0,
  set: (v) => {
    filterMonth.value = v === 0 ? null : v
  },
})
</script>

<template>
  <div>
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.bankImports.title') }}</h1>
        <p class="mt-1 text-sm text-gray-500">{{ t('admin.bankImports.subtitle') }}</p>
      </div>
      <div class="flex items-center gap-2">
        <button
          type="button"
          :disabled="syncBusy"
          class="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
          data-testid="bank-sync-btn"
          @click="runSync"
        >
          {{ syncBusy ? t('common.loading') : t('admin.bankImports.runSync') }}
        </button>
        <button
          type="button"
          :disabled="vippsResyncBusy"
          class="rounded-md border border-blue-300 bg-white px-3 py-1.5 text-sm font-semibold text-blue-700 hover:bg-blue-50 disabled:opacity-50"
          data-testid="vipps-resync-btn"
          @click="runVippsResyncAction"
        >
          {{ vippsResyncBusy ? t('common.loading') : t('admin.bankImports.runVippsResync') }}
        </button>
      </div>
    </div>

    <div v-if="syncResult" class="mt-3 rounded-md bg-blue-50 px-3 py-2 text-xs text-blue-900" data-testid="bank-sync-result">
      {{ t('admin.bankImports.runSyncResult', {
        kid: syncResult.kid_matched,
        vipps: syncResult.vipps_reconciled,
        transfers: syncResult.transfers_linked,
      }) }}
      <span v-if="syncResult.closed_periods?.length" class="ml-2 text-yellow-800">
        ({{ t('admin.bankImports.runSyncClosed', { years: syncResult.closed_periods.join(', ') }) }})
      </span>
    </div>
    <div v-if="vippsResyncResult" class="mt-3 rounded-md bg-blue-50 px-3 py-2 text-xs text-blue-900" data-testid="vipps-resync-result">
      {{ t('admin.bankImports.vippsResyncResult', {
        resynced: vippsResyncResult.resynced,
        scanned: vippsResyncResult.scanned,
        skipped: vippsResyncResult.skipped,
        failed: vippsResyncResult.failed?.length ?? 0,
      }) }}
    </div>
    <div v-if="syncError" class="mt-3 rounded-md bg-red-50 px-3 py-2 text-xs text-red-700">{{ syncError }}</div>
    <div v-if="vippsResyncError" class="mt-3 rounded-md bg-red-50 px-3 py-2 text-xs text-red-700">{{ vippsResyncError }}</div>

    <div class="mt-6">
      <Tabs v-model="activeTab" :tabs="tabs" />
    </div>

    <!-- Accounts tab -->
    <div v-if="activeTab === 'reconcile'" class="mt-6">
      <BankReconcileTab />
    </div>

    <div v-if="activeTab === 'accounts'" class="mt-6 space-y-4">
      <section class="rounded-lg border border-gray-200 bg-white p-5">
        <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <div>
            <label class="block text-xs font-medium text-gray-700">{{ t('admin.bankImports.selectBankAccount') }}</label>
            <div data-testid="accounts-bank-select">
              <AccountSelect
                :model-value="filterBankAccount ?? ''"
                :options="bankAccounts"
                @update:model-value="(v) => selectBankAccount(v as string)"
              />
            </div>
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-700">{{ t('admin.bankImports.selectVippsMSN') }}</label>
            <div class="mt-1" data-testid="accounts-vipps-select">
              <Select
                :model-value="filterVippsMSN ?? ''"
                :options="vippsMSNOptions"
                :aria-label="t('admin.bankImports.selectVippsMSN')"
                @update:model-value="(v) => selectVippsMSN(v as string)"
              />
            </div>
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-700">{{ t('admin.bankImports.selectYear') }}</label>
            <div class="mt-1" data-testid="accounts-year-select">
              <Select
                :model-value="filterYear"
                :options="yearSelectOptions"
                width="content"
                :aria-label="t('admin.bankImports.selectYear')"
                @update:model-value="(v) => onUserYearChange(v as number)"
              />
            </div>
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-700">{{ t('admin.bankImports.selectMonth') }}</label>
            <div class="mt-1" data-testid="accounts-month-select">
              <Select
                v-model="filterMonthValue"
                :options="monthSelectOptions"
                width="content"
                :aria-label="t('admin.bankImports.selectMonth')"
              />
            </div>
          </div>
        </div>
      </section>

      <section class="rounded-lg border border-gray-200 bg-white p-5" data-testid="bank-freshness">
        <h3 class="text-sm font-semibold text-gray-900">{{ t('admin.bankImports.dataFreshness') }}</h3>
        <p class="mt-0.5 text-xs text-gray-500">{{ t('admin.bankImports.dataFreshnessHint') }}</p>
        <ul class="mt-3 divide-y divide-gray-100 text-sm">
          <li v-for="a in bankAccounts" :key="a.code" class="flex items-center justify-between py-1.5">
            <span class="text-gray-700">{{ a.code }} — {{ a.name }}</span>
            <span v-if="bankLastImport.get(a.code)" class="text-xs tabular-nums text-gray-600">
              {{ t('admin.bankImports.lastUpdated') }}: {{ formatDate(bankLastImport.get(a.code)) }}
            </span>
            <span v-else class="text-xs text-gray-400">{{ t('admin.bankImports.neverImported') }}</span>
          </li>
          <li v-for="v in vippsFreshness" :key="'vipps-' + v.msn" class="flex items-center justify-between py-1.5">
            <span class="text-gray-700">Vipps · msn {{ v.msn }}</span>
            <span class="text-xs tabular-nums text-gray-600">
              {{ t('admin.bankImports.lastUpdated') }}: {{ formatDate(v.lastUpdated) }}
            </span>
          </li>
        </ul>
      </section>

      <div v-if="accountFilter.kind === 'bank'">
        <BankRowsTable :rows="accountsBankRows ?? []" :show-reconcile="false" />
        <p v-if="!(accountsBankRows && accountsBankRows.length)" class="mt-3 text-xs text-gray-500">
          {{ t('admin.bankImports.noRowsInPeriod') }}
        </p>
      </div>
      <div v-else-if="accountFilter.kind === 'vipps'">
        <VippsRowsTable :rows="accountsVippsRows ?? []" />
        <p v-if="!(accountsVippsRows && accountsVippsRows.length)" class="mt-3 text-xs text-gray-500">
          {{ t('admin.bankImports.noRowsInPeriod') }}
        </p>
      </div>
      <p v-else class="text-xs text-gray-500">
        {{ t('admin.bankImports.noAccountSelected') }}
      </p>
    </div>

    <!-- Imports tab -->
    <div v-if="activeTab === 'imports'" class="mt-6 grid items-start gap-4 lg:grid-cols-3">
      <!-- Bank upload card -->
      <section class="flex flex-col rounded-lg border border-gray-200 bg-white p-5" data-testid="bank-upload-card">
        <h2 class="text-base font-semibold text-gray-900">{{ t('admin.bankImports.bankCardTitle') }}</h2>
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.bankImports.bankCardDesc') }}</p>

        <div class="mt-4 space-y-3">
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-700">{{ t('admin.bankImports.bankAccount') }}</label>
            <div data-testid="bank-account-select">
              <AccountSelect v-model="bankAccountCode" :options="bankAccounts" />
            </div>
          </div>

          <div>
            <label class="mb-1 block text-xs font-medium text-gray-700">{{ t('admin.bankImports.format') }}</label>
            <Select v-model="bankFormat" :options="bankFormatOptions" />
          </div>

          <FileInput
            accept=".csv,text/csv"
            compact
            class="block text-sm"
            data-testid="bank-file-input"
            @change="onBankFile"
          />
        </div>

        <p v-if="bankError" class="mt-3 rounded-md bg-red-50 px-2 py-1 text-xs text-red-700">{{ bankError }}</p>

        <div class="mt-4 flex justify-end">
          <button
            type="button"
            :disabled="!bankFile || !bankAccountCode || bankBusy"
            class="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
            data-testid="bank-upload-submit"
            @click="submitBank"
          >
            {{ bankBusy ? t('common.loading') : t('admin.bankImports.upload') }}
          </button>
        </div>

        <div v-if="bankResult" class="mt-3 rounded-md bg-gray-50 px-3 py-2 text-xs text-gray-700" data-testid="bank-result">
          <span class="font-medium">{{ bankResult.filename }}</span>
          — {{ t('admin.bankImports.imported') }}: {{ bankResult.imported }}
          · {{ t('admin.bankImports.skipped') }}: {{ bankResult.skipped_dup }}
          · {{ t('admin.bankImports.matched') }}: {{ bankResult.matched }}
          · {{ t('admin.bankImports.transfers') }}: {{ bankResult.transfers }}
          <p v-if="bankResult.auto_matched" class="mt-2 rounded bg-blue-50 px-2 py-1 text-blue-800">
            {{ t('admin.bankImports.autoMatchedNotice', { code: bankResult.bank_account_code }) }}
          </p>
          <p v-if="bankResult.closed_periods?.length" class="mt-2 rounded bg-yellow-50 px-2 py-1 text-yellow-800">
            {{ t('admin.bankImports.closedPeriodsNotice', { years: bankResult.closed_periods.join(', ') }) }}
          </p>
        </div>
      </section>

      <!-- Vipps upload card -->
      <section class="flex flex-col rounded-lg border border-gray-200 bg-white p-5" data-testid="vipps-upload-card">
        <h2 class="text-base font-semibold text-gray-900">{{ t('admin.bankImports.vippsCardTitle') }}</h2>
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.bankImports.vippsCardDesc') }}</p>

        <FileInput
          accept=".csv,text/csv"
          compact
          class="mt-4 block text-sm"
          data-testid="vipps-file-input"
          @change="onVippsFile"
        />

        <p v-if="vippsError" class="mt-3 rounded-md bg-red-50 px-2 py-1 text-xs text-red-700">{{ vippsError }}</p>

        <div class="mt-4 flex justify-end">
          <button
            type="button"
            :disabled="!vippsFile || vippsBusy"
            class="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
            data-testid="vipps-upload-submit"
            @click="submitVipps"
          >
            {{ vippsBusy ? t('common.loading') : t('admin.bankImports.upload') }}
          </button>
        </div>

        <div v-if="vippsResult" class="mt-3 rounded-md bg-gray-50 px-3 py-2 text-xs text-gray-700" data-testid="vipps-result">
          <span class="font-medium">{{ vippsResult.filename }}</span>
          — {{ t('admin.bankImports.imported') }}: {{ vippsResult.imported }}
          · {{ t('admin.bankImports.skipped') }}: {{ vippsResult.skipped_dup }}
        </div>
      </section>

      <!-- Uni24 import card -->
      <section class="flex flex-col rounded-lg border border-gray-200 bg-white p-5" data-testid="uni24-upload-card">
        <h2 class="text-base font-semibold text-gray-900">{{ t('admin.financials.importUni24Title') }}</h2>
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.financials.importUni24Desc') }}</p>

        <div class="mt-4 space-y-3">
          <div class="grid grid-cols-2 gap-3">
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-700">{{ t('admin.financials.importDateFrom') }}</label>
              <input
                v-model="uni24DateFrom"
                type="date"
                class="w-full rounded-md border border-gray-300 px-3 py-1.5 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-700">{{ t('admin.financials.importDateTo') }}</label>
              <input
                v-model="uni24DateTo"
                type="date"
                class="w-full rounded-md border border-gray-300 px-3 py-1.5 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          </div>

          <FileInput
            accept=".csv,text/csv"
            compact
            class="block text-sm"
            data-testid="uni24-file-input"
            @change="onUni24File"
          />
        </div>

        <p v-if="uni24Error" class="mt-3 rounded-md bg-red-50 px-2 py-1 text-xs text-red-700">{{ uni24Error }}</p>

        <div class="mt-4 flex justify-end">
          <button
            type="button"
            :disabled="!uni24File || uni24Busy"
            class="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
            data-testid="uni24-upload-submit"
            @click="submitUni24"
          >
            {{ uni24Busy ? t('common.loading') : t('admin.financials.importRun') }}
          </button>
        </div>

        <div v-if="uni24Result" class="mt-3 rounded-md bg-gray-50 px-3 py-2 text-xs text-gray-700" data-testid="uni24-result">
          {{ t('admin.financials.importDone', { imported: uni24Result.imported, skipped: uni24Result.skipped }) }}
          <details v-if="uni24Result.rows.length" class="mt-2">
            <summary class="cursor-pointer text-gray-500 hover:text-gray-700">
              {{ t('admin.financials.importColStatus') }} ({{ uni24Result.rows.length }})
            </summary>
            <div class="mt-2 overflow-x-auto">
              <table class="w-full text-xs">
                <thead class="text-left text-gray-500">
                  <tr>
                    <th class="py-1 pr-3">{{ t('admin.financials.importColId') }}</th>
                    <th class="py-1 pr-3">{{ t('admin.financials.importColName') }}</th>
                    <th class="py-1 pr-3">{{ t('admin.financials.importColStatus') }}</th>
                    <th class="py-1">{{ t('admin.financials.importColError') }}</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-gray-100">
                  <tr v-for="row in uni24Result.rows" :key="row.row" :class="row.status === 'error' ? 'bg-red-50' : ''">
                    <td class="py-1 pr-3 font-mono">{{ row.external_id }}</td>
                    <td class="py-1 pr-3">{{ row.name }}</td>
                    <td class="py-1 pr-3">{{ row.status }}</td>
                    <td class="py-1 text-red-600">{{ row.error }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </details>
        </div>
      </section>
    </div>

    <!-- Recent bank imports list -->
    <section v-if="activeTab === 'imports' && bankImportsList && bankImportsList.length" class="mt-8" data-testid="bank-imports-list">
      <h2 class="text-base font-semibold text-gray-900">{{ t('admin.bankImports.bankImportsListTitle') }}</h2>
      <ul class="mt-3 divide-y divide-gray-100 rounded-lg border border-gray-200 bg-white">
        <li
          v-for="b in bankImportsList"
          :key="b.id"
          class="flex cursor-pointer items-center justify-between px-3 py-2 text-sm hover:bg-gray-50"
          :class="{ 'bg-blue-50': b.id === currentBankImportId }"
          @click="selectBankImport(b.id)"
        >
          <span>
            {{ b.filename }}
            <span class="ml-2 text-xs text-gray-500">{{ b.account_code }}</span>
          </span>
          <span class="flex items-center gap-3 text-xs text-gray-500">
            <span class="tabular-nums">{{ formatDate(b.created_at) }}</span>
            {{ b.row_count }} {{ t('admin.bankImports.rows') }}
            <button
              type="button"
              class="font-semibold text-blue-700 hover:underline"
              @click.stop="openReassign(b.id, b.account_code)"
            >
              {{ t('admin.bankImports.reassign') }}
            </button>
          </span>
        </li>
      </ul>
    </section>

    <!-- Bank rows from selected/uploaded import -->
    <section v-if="activeTab === 'imports' && bankRows && bankRows.length" class="mt-4">
      <h2 class="text-base font-semibold text-gray-900">{{ t('admin.bankImports.rowsTitle') }}</h2>
      <div class="mt-3">
        <BankRowsTable :rows="bankRows" :show-reconcile="true" @reconcile="openReconcile" />
      </div>
    </section>

    <!-- Recent Vipps imports list -->
    <section v-if="activeTab === 'imports' && vippsImports && vippsImports.length" class="mt-8">
      <h2 class="text-base font-semibold text-gray-900">{{ t('admin.bankImports.vippsListTitle') }}</h2>
      <ul class="mt-3 divide-y divide-gray-100 rounded-lg border border-gray-200 bg-white">
        <li
          v-for="v in vippsImports"
          :key="v.id"
          class="flex cursor-pointer items-center justify-between px-3 py-2 text-sm hover:bg-gray-50"
          :class="{ 'bg-blue-50': v.id === currentVippsImportId }"
          @click="currentVippsImportId = v.id"
        >
          <span>{{ v.filename }} <span class="ml-2 text-xs text-gray-500">msn {{ v.msn }}</span></span>
          <span class="flex items-center gap-3 text-xs text-gray-500">
            <span class="tabular-nums">{{ formatDate(v.created_at) }}</span>
            {{ v.row_count }} {{ t('admin.bankImports.rows') }}
          </span>
        </li>
      </ul>
    </section>

    <!-- Vipps rows expanded -->
    <section v-if="activeTab === 'imports' && vippsRows && vippsRows.length" class="mt-4">
      <VippsRowsTable :rows="vippsRows" />
    </section>

    <!-- Reconcile preview modal -->
    <div
      v-if="reconcileTarget"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      data-testid="reconcile-modal"
    >
      <div class="max-h-[90vh] w-full max-w-2xl overflow-y-auto rounded-lg bg-white p-6 shadow-xl">
        <h3 class="text-lg font-semibold text-gray-900">{{ t('admin.bankImports.reconcileTitle') }}</h3>
        <p class="mt-1 text-xs text-gray-500">{{ reconcileTarget.description }}</p>

        <p v-if="reconcileError" class="mt-3 rounded-md bg-red-50 px-3 py-2 text-xs text-red-700">
          {{ reconcileError }}
        </p>

        <div v-if="reconcilePreview" class="mt-4 space-y-3">
          <div class="grid grid-cols-2 gap-2 text-xs text-gray-700">
            <div><span class="text-gray-500">{{ t('admin.bankImports.settlement') }}:</span> {{ reconcilePreview.settlement_number }}</div>
            <div><span class="text-gray-500">{{ t('admin.bankImports.msn') }}:</span> {{ reconcilePreview.msn }}</div>
            <div><span class="text-gray-500">{{ t('admin.bankImports.bankDeposit') }}:</span> {{ nok(reconcilePreview.bank_amount) }}</div>
            <div><span class="text-gray-500">{{ t('admin.bankImports.totalFees') }}:</span> {{ nok(reconcilePreview.total_fees) }}</div>
          </div>

          <div
            class="rounded-md px-3 py-2 text-xs"
            :class="reconcilePreview.balanced ? 'bg-green-50 text-green-700' : 'bg-red-50 text-red-700'"
            data-testid="reconcile-balance"
          >
            {{ reconcilePreview.balanced ? t('admin.bankImports.balanced') : reconcilePreview.reason }}
          </div>

          <div v-if="reconcilePreview.period_closed" class="rounded-md bg-red-50 px-3 py-2 text-xs text-red-800" data-testid="reconcile-period-closed">
            {{ t('admin.bankImports.periodClosedNotice', { year: reconcilePreview.period_year }) }}
          </div>

          <div v-if="reconcilePreview.unresolved_count > 0" class="rounded-md bg-yellow-50 px-3 py-2 text-xs text-yellow-800">
            {{ t('admin.bankImports.unresolvedNotice', { n: reconcilePreview.unresolved_count }) }}
          </div>

          <table class="w-full text-xs">
            <thead class="text-left text-gray-500">
              <tr>
                <th class="py-1">{{ t('admin.bankImports.colAccount') }}</th>
                <th class="py-1">{{ t('admin.bankImports.colDescription') }}</th>
                <th class="py-1 text-right">{{ t('admin.bankImports.colDebit') }}</th>
                <th class="py-1 text-right">{{ t('admin.bankImports.colCredit') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(l, i) in reconcilePreview.lines" :key="i" class="border-t border-gray-100">
                <td class="py-1 tabular-nums">{{ l.account_code }}</td>
                <td class="py-1">{{ l.description }}</td>
                <td class="py-1 text-right tabular-nums">{{ l.debit > 0 ? nok(l.debit) : '' }}</td>
                <td class="py-1 text-right tabular-nums">{{ l.credit > 0 ? nok(l.credit) : '' }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="mt-5 flex justify-end gap-2">
          <button
            type="button"
            class="rounded-md px-3 py-1.5 text-sm text-gray-600 hover:bg-gray-100"
            @click="reconcileTarget = null; reconcilePreview = null"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            :disabled="!reconcilePreview?.balanced || reconcilePreview?.period_closed || reconcileBusy"
            class="rounded-md bg-green-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-green-700 disabled:opacity-50"
            data-testid="reconcile-confirm"
            @click="confirmReconcile"
          >
            {{ reconcileBusy ? t('common.loading') : t('admin.bankImports.confirmBilag') }}
          </button>
        </div>
      </div>
    </div>

    <!-- Reassign import modal (DIL-343) -->
    <div
      v-if="reassignImportId"
      class="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4"
      role="dialog"
      aria-modal="true"
      @click.self="cancelReassign"
    >
      <div class="w-full max-w-md rounded-lg bg-white p-5 shadow-xl">
        <h2 class="text-base font-semibold text-gray-900">{{ t('admin.bankImports.reassignTitle') }}</h2>
        <p class="mt-2 text-sm text-gray-600">{{ t('admin.bankImports.reassignBody', { code: reassignFrom }) }}</p>

        <label class="mt-4 block text-sm font-medium text-gray-700">
          {{ t('admin.bankImports.reassignTo') }}
        </label>
        <Select v-model="reassignTo" :options="bankAccounts.map((a) => ({ value: a.code, label: `${a.code} — ${a.name}` }))" />

        <p v-if="reassignError" class="mt-3 rounded bg-red-50 px-3 py-2 text-xs text-red-700">{{ reassignError }}</p>

        <div class="mt-4 flex justify-end gap-2">
          <button
            type="button"
            class="rounded-md bg-white px-4 py-2 text-sm font-semibold text-gray-700 ring-1 ring-gray-300 hover:bg-gray-50"
            @click="cancelReassign"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            :disabled="reassignBusy || reassignTo === reassignFrom"
            class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
            @click="submitReassign"
          >
            {{ reassignBusy ? t('common.loading') : t('admin.bankImports.reassign') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
