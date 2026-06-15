<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { ArrowDownLeft, ArrowUpRight, Sparkles, Search, FileText, Ban, Undo2 } from 'lucide-vue-next'
import Modal from '@/components/ui/Modal.vue'
import Select from '@/components/ui/form/Select.vue'
import {
  useBankUnmatchedRows,
  useBankRowSuggestions,
  useBankUnmatchedCountsByYear,
  useReconcileMutations,
  fetchPotentialInvoices,
  DISMISS_REASONS,
  type BankRowSummary,
  type BankRowKind,
  type InvoiceSuggestion,
  type AccountSuggestion,
  type DismissReason,
} from '@/composables/useBankReconcile'
import { useAccountsList } from '@/composables/useAccounting'

const { t, locale } = useI18n()

const kind = ref<BankRowKind>('all')
const q = ref('')
const currentYear = new Date().getFullYear()

const { data: countsByYear } = useBankUnmatchedCountsByYear()

// 0 = "all years" sentinel for the Select component (which requires string|number values).
// Converted to null for API calls where 0 means no year filter.
const yearSelectValue = ref<number>(currentYear)
const year = computed(() => yearSelectValue.value === 0 ? null : yearSelectValue.value)

const yearOptions = computed(() => {
  const years = [0, ...Array.from({ length: 5 }, (_, i) => currentYear - i)]
  return years.map((y) => {
    const count = y === 0
      ? Object.values(countsByYear.value ?? {}).reduce((a, b) => a + b, 0)
      : (countsByYear.value?.[y] ?? 0)
    return {
      value: y,
      label: y === 0
        ? `${t('admin.bankReconcile.yearAll')}${count > 0 ? ` (${count})` : ''}`
        : `${y}${count > 0 ? ` (${count})` : ''}`,
    }
  })
})

const { data: rows, isLoading } = useBankUnmatchedRows(kind, q, year)

const duplicateRows = computed(() =>
  (rows.value ?? []).filter((r) => r.likely_duplicate_of_matched && !r.dismissed_at),
)
const mainRows = computed(() =>
  (rows.value ?? []).filter((r) => !r.likely_duplicate_of_matched || r.dismissed_at),
)

const focusedRowId = ref<string | null>(null)
const { data: suggestions } = useBankRowSuggestions(focusedRowId)

const { assignInvoice, assignAccount, dismiss, unassign } = useReconcileMutations()

function focusRow(id: string) {
  focusedRowId.value = focusedRowId.value === id ? null : id
}

function formatNOK(n: number): string {
  return new Intl.NumberFormat('nb-NO', { style: 'currency', currency: 'NOK', signDisplay: 'always' }).format(n)
}
function formatDate(s: string): string {
  return new Date(s).toLocaleDateString(locale.value || 'nb-NO')
}

const filterChips: { value: BankRowKind; labelKey: string }[] = [
  { value: 'all', labelKey: 'admin.bankReconcile.filterAll' },
  { value: 'incoming', labelKey: 'admin.bankReconcile.filterIncoming' },
  { value: 'outgoing', labelKey: 'admin.bankReconcile.filterOutgoing' },
  { value: 'dismissed', labelKey: 'admin.bankReconcile.filterDismissed' },
]

const sourceChips: { value: BankRowKind; labelKey: string }[] = [
  { value: 'bank', labelKey: 'admin.bankReconcile.filterBank' },
  { value: 'vipps', labelKey: 'admin.bankReconcile.filterVipps' },
  { value: 'duplicate', labelKey: 'admin.bankReconcile.filterDuplicate' },
  { value: 'double_payment', labelKey: 'admin.bankReconcile.filterDoublePayment' },
]

const confidenceClass: Record<string, string> = {
  sterk: 'border-emerald-300 bg-emerald-50',
  sannsynleg: 'border-amber-300 bg-amber-50',
  svak: 'border-gray-300 bg-gray-50',
  potensiell: 'border-blue-200 bg-blue-50',
}

const { data: accounts } = useAccountsList()
const expenseAccounts = computed(() => (accounts.value ?? []).filter((a) => a.account_type === 'expense' && a.is_active))
const revenueAccounts = computed(() => (accounts.value ?? []).filter((a) => a.account_type === 'revenue' && a.is_active))

// ── Potential invoices modal ─────────────────────────────────
const potentialOpen = ref(false)
const potentialRowId = ref<string | null>(null)
const potentialQuery = ref('')
const potentialItems = ref<InvoiceSuggestion[]>([])
const potentialLoading = ref(false)

async function openPotential(row: BankRowSummary) {
  potentialRowId.value = row.id
  potentialQuery.value = ''
  potentialOpen.value = true
  await loadPotential()
}
async function loadPotential() {
  if (!potentialRowId.value) return
  potentialLoading.value = true
  try {
    potentialItems.value = await fetchPotentialInvoices(potentialRowId.value, potentialQuery.value)
  } finally {
    potentialLoading.value = false
  }
}

// ── Account picker modal ─────────────────────────────────────
const accountPickerOpen = ref(false)
const accountPickerRowId = ref<string | null>(null)
const accountPickerKind = ref<'expense' | 'revenue'>('expense')
const accountPickerSuggestions = ref<AccountSuggestion[]>([])
const accountPickerSearch = ref('')

function openAccountPicker(row: BankRowSummary) {
  accountPickerRowId.value = row.id
  accountPickerKind.value = row.amount < 0 ? 'expense' : 'revenue'
  accountPickerSuggestions.value = suggestions.value?.accounts ?? []
  accountPickerSearch.value = ''
  accountPickerOpen.value = true
}
const filteredAccounts = computed(() => {
  const base = accountPickerKind.value === 'expense' ? expenseAccounts.value : revenueAccounts.value
  const qq = accountPickerSearch.value.trim().toLowerCase()
  if (!qq) return base
  return base.filter((a) => a.code.includes(qq) || a.name.toLowerCase().includes(qq))
})

// ── Dismiss reason modal ─────────────────────────────────────
const dismissOpen = ref(false)
const dismissRowId = ref<string | null>(null)
const dismissReason = ref<DismissReason | null>(null)

function openDismiss(row: BankRowSummary) {
  dismissRowId.value = row.id
  if (row.likely_duplicate_of_matched) dismissReason.value = 'duplicate'
  else if (row.possible_double_payment) dismissReason.value = 'double_payment'
  else dismissReason.value = null
  dismissOpen.value = true
}

async function submitDismiss() {
  if (!dismissRowId.value || !dismissReason.value) return
  await dismiss.mutateAsync({ rowId: dismissRowId.value, reason: dismissReason.value })
  dismissOpen.value = false
}

// ── Actions ──────────────────────────────────────────────────
async function doAssignInvoice(rowId: string, invoiceId: string) {
  await assignInvoice.mutateAsync({ rowId, invoiceId })
  potentialOpen.value = false
}
async function doAssignAccount(rowId: string, accountCode: string, k: 'expense' | 'revenue') {
  await assignAccount.mutateAsync({ rowId, accountCode, kind: k })
  accountPickerOpen.value = false
}
async function doUnassign(rowId: string) {
  if (!window.confirm(t('admin.bankReconcile.unassignConfirm'))) return
  await unassign.mutateAsync({ rowId })
}
</script>

<template>
  <div>
    <!-- Filter chips + search -->
    <div class="flex flex-wrap items-center gap-2">
      <button
        v-for="chip in filterChips"
        :key="chip.value"
        type="button"
        class="rounded-full px-3 py-1 text-xs font-semibold"
        :class="kind === chip.value ? 'bg-blue-600 text-white' : 'bg-gray-100 text-gray-700 hover:bg-gray-200'"
        @click="kind = chip.value"
      >
        {{ t(chip.labelKey) }}
      </button>
      <span class="h-4 w-px bg-gray-300" aria-hidden="true" />
      <button
        v-for="chip in sourceChips"
        :key="chip.value"
        type="button"
        class="rounded-full px-3 py-1 text-xs font-semibold"
        :class="kind === chip.value ? 'bg-indigo-600 text-white' : 'bg-gray-100 text-gray-700 hover:bg-gray-200'"
        @click="kind = chip.value"
      >
        {{ t(chip.labelKey) }}
      </button>
      <div class="ml-auto flex items-center gap-2">
        <label class="text-xs text-gray-600">{{ t('admin.bankReconcile.year') }}</label>
        <Select v-model="yearSelectValue" :options="yearOptions" width="content" />
        <Search class="h-4 w-4 text-gray-400" />
        <input
          v-model="q"
          type="search"
          :placeholder="t('admin.bankReconcile.searchPlaceholder')"
          class="rounded-md border border-gray-300 px-3 py-1 text-sm"
        />
      </div>
    </div>

    <p v-if="isLoading" class="mt-6 text-sm text-gray-500">{{ t('common.loading') }}…</p>
    <p v-else-if="!rows?.length" class="mt-6 rounded-md border border-dashed border-gray-300 px-4 py-6 text-center text-sm text-gray-500">
      {{ t('admin.bankReconcile.empty') }}
    </p>

    <template v-else>
      <!-- Confirmed Arkivref duplicates — grouped at top for quick dismissal -->
      <div v-if="duplicateRows.length" class="mt-4 rounded-md border border-amber-200 bg-amber-50 p-3">
        <p class="mb-1 text-xs font-semibold text-amber-900">
          ⚠ {{ t('admin.bankReconcile.confirmedDuplicatesHeader', { count: duplicateRows.length }) }}
        </p>
        <p class="mb-3 text-xs text-amber-700">{{ t('admin.bankReconcile.confirmedDuplicatesHint') }}</p>
        <ul class="space-y-2">
          <li
            v-for="row in duplicateRows"
            :key="row.id"
            class="flex items-center justify-between gap-4 rounded-md border border-amber-200 bg-white px-4 py-3"
          >
            <div class="flex items-start gap-3">
              <ArrowDownLeft class="mt-0.5 h-5 w-5 shrink-0 text-emerald-600" />
              <div>
                <p class="text-sm">
                  <span class="font-semibold tabular-nums text-emerald-700">{{ formatNOK(row.amount) }}</span>
                  <span class="ml-2 text-gray-500">{{ formatDate(row.row_date) }}</span>
                  <span class="ml-2 rounded bg-gray-100 px-1.5 py-0.5 font-mono text-[10px] text-gray-600">{{ row.bank_account_code }}</span>
                </p>
                <p v-if="row.counterpart" class="mt-0.5 text-sm font-medium text-gray-900">{{ row.counterpart }}</p>
                <p class="mt-0.5 truncate text-xs text-gray-500" :title="row.description">{{ row.description || '—' }}</p>
              </div>
            </div>
            <button
              type="button"
              class="inline-flex shrink-0 items-center gap-1 rounded-md border border-amber-300 bg-white px-3 py-1 text-xs font-semibold text-amber-700 hover:bg-amber-50"
              @click="openDismiss(row)"
            >
              <Ban class="h-3 w-3" />
              {{ t('admin.bankReconcile.dismiss') }}
            </button>
          </li>
        </ul>
      </div>

      <!-- Main unmatched rows -->
      <ul v-if="mainRows.length" class="mt-4 space-y-3">
        <li
          v-for="row in mainRows"
          :key="row.id"
          class="rounded-md border border-gray-200 bg-white p-4"
        >
          <!-- Bank row info -->
          <div class="flex items-start justify-between gap-4">
            <div class="flex items-start gap-3">
              <component
                :is="row.amount >= 0 ? ArrowDownLeft : ArrowUpRight"
                :class="row.amount >= 0 ? 'text-emerald-600' : 'text-gray-500'"
                class="mt-0.5 h-5 w-5 shrink-0"
              />
              <div>
                <p class="text-sm">
                  <span class="font-semibold tabular-nums" :class="row.amount >= 0 ? 'text-emerald-700' : 'text-gray-700'">
                    {{ formatNOK(row.amount) }}
                  </span>
                  <span class="ml-2 text-gray-500">{{ formatDate(row.row_date) }}</span>
                  <span class="ml-2 rounded bg-gray-100 px-1.5 py-0.5 font-mono text-[10px] text-gray-600">{{ row.bank_account_code }}</span>
                </p>
                <p v-if="row.counterpart" class="mt-0.5 text-sm font-medium text-gray-900">{{ row.counterpart }}</p>
                <p class="mt-0.5 truncate text-xs text-gray-500" :title="row.description">{{ row.description || '—' }}</p>
                <p v-if="row.dismissed_at" class="mt-1 text-xs text-amber-700">
                  {{ t('admin.bankReconcile.dismissedAs', { reason: t('admin.bankReconcile.reasons.' + (row.dismissed_reason ?? '')) }) }}
                </p>
                <p v-if="row.possible_double_payment && !row.dismissed_at" class="mt-1 inline-flex items-center gap-1 rounded-full bg-red-100 px-2 py-0.5 text-[10px] font-semibold text-red-800">
                  ⚠ {{ t('admin.bankReconcile.possibleDoublePayment') }}
                </p>
              </div>
            </div>
            <button
              type="button"
              class="text-xs text-blue-700 hover:underline"
              @click="focusRow(row.id)"
            >
              {{ focusedRowId === row.id ? t('admin.bankReconcile.hideSuggestions') : t('admin.bankReconcile.showSuggestions') }}
            </button>
          </div>

          <!-- Suggestions, only when focused -->
          <div v-if="focusedRowId === row.id && suggestions" class="mt-3 space-y-2">
            <div
              v-for="sug in suggestions.invoices.slice(0, 3)"
              :key="sug.invoice_id"
              class="flex items-center justify-between gap-3 rounded-md border px-3 py-2 text-sm"
              :class="confidenceClass[sug.confidence_label]"
            >
              <div class="min-w-0 flex-1">
                <p class="flex items-center gap-2">
                  <Sparkles class="h-3.5 w-3.5" />
                  <span class="font-semibold">#{{ sug.invoice_number }}</span>
                  <span class="text-gray-700">{{ sug.member_name }}</span>
                  <span class="text-xs text-gray-500">· {{ sug.price_item_name || '—' }}</span>
                  <span class="text-xs font-semibold text-gray-800">{{ formatNOK(sug.total_amount) }}</span>
                </p>
                <p class="text-xs text-gray-500" :title="sug.why_tooltip">
                  {{ t('admin.bankReconcile.confidence.' + sug.confidence_label) }} · {{ sug.why_tooltip }}
                  <span v-if="sug.kid_number" class="ml-1 font-mono">KID: {{ sug.kid_number }}</span>
                </p>
              </div>
              <button
                type="button"
                class="rounded-md bg-blue-600 px-3 py-1 text-xs font-semibold text-white hover:bg-blue-700"
                @click="doAssignInvoice(row.id, sug.invoice_id)"
              >
                {{ t('admin.bankReconcile.assign') }}
              </button>
            </div>

            <!-- Footer actions -->
            <div class="flex flex-wrap gap-2">
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1 text-xs font-semibold text-gray-700 hover:bg-gray-50"
                @click="openPotential(row)"
              >
                <Search class="h-3 w-3" />
                {{ t('admin.bankReconcile.searchInvoice') }}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1 text-xs font-semibold text-gray-700 hover:bg-gray-50"
                @click="openAccountPicker(row)"
              >
                <FileText class="h-3 w-3" />
                {{ t('admin.bankReconcile.assignAccount') }}
              </button>
              <button
                v-if="!row.dismissed_at"
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-amber-300 bg-white px-3 py-1 text-xs font-semibold text-amber-700 hover:bg-amber-50"
                @click="openDismiss(row)"
              >
                <Ban class="h-3 w-3" />
                {{ t('admin.bankReconcile.dismiss') }}
              </button>
              <button
                v-if="row.dismissed_at"
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1 text-xs font-semibold text-gray-700 hover:bg-gray-50"
                @click="doUnassign(row.id)"
              >
                <Undo2 class="h-3 w-3" />
                {{ t('admin.bankReconcile.unassign') }}
              </button>
            </div>
          </div>
        </li>
      </ul>
    </template>

    <!-- Potential invoices modal -->
    <Modal v-model:open="potentialOpen" size="2xl" :title="t('admin.bankReconcile.potentialTitle')">
      <div class="rounded-md bg-blue-50 px-3 py-2 text-xs text-blue-900">
        {{ t('admin.bankReconcile.potentialBanner') }}
      </div>
      <input
        v-model="potentialQuery"
        type="search"
        :placeholder="t('admin.bankReconcile.searchInvoicePlaceholder')"
        class="mt-3 w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
        @input="loadPotential"
      />
      <p v-if="potentialLoading" class="mt-3 text-xs text-gray-500">{{ t('common.loading') }}…</p>
      <ul v-else-if="potentialItems.length" class="mt-3 max-h-96 divide-y divide-gray-100 overflow-y-auto">
        <li v-for="inv in potentialItems" :key="inv.invoice_id" class="flex items-center justify-between gap-3 py-2 text-sm">
          <div>
            <p class="font-semibold">#{{ inv.invoice_number }} <span class="font-normal text-gray-700">— {{ inv.member_name }}</span> <span class="text-sm font-semibold text-gray-800">{{ formatNOK(inv.total_amount) }}</span></p>
            <p class="text-xs text-gray-500">{{ inv.price_item_name || '—' }} · {{ inv.member_email || '—' }} · {{ formatDate(inv.issue_date) }}<span v-if="inv.kid_number" class="ml-1 font-mono">· KID: {{ inv.kid_number }}</span></p>
          </div>
          <button
            type="button"
            class="rounded-md bg-blue-600 px-3 py-1 text-xs font-semibold text-white hover:bg-blue-700"
            @click="potentialRowId && doAssignInvoice(potentialRowId, inv.invoice_id)"
          >
            {{ t('admin.bankReconcile.assign') }}
          </button>
        </li>
      </ul>
      <p v-else class="mt-3 text-xs text-gray-500">{{ t('admin.bankReconcile.noPotential') }}</p>
    </Modal>

    <!-- Account picker modal -->
    <Modal v-model:open="accountPickerOpen" size="lg" :title="t('admin.bankReconcile.accountPickerTitle')">
      <input
        v-model="accountPickerSearch"
        type="search"
        :placeholder="t('admin.bankReconcile.searchAccountPlaceholder')"
        class="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
      />
      <div v-if="accountPickerSuggestions.length" class="mt-3">
        <p class="text-xs font-semibold uppercase text-gray-500">{{ t('admin.bankReconcile.accountSuggestions') }}</p>
        <ul class="mt-1 space-y-1">
          <li v-for="sug in accountPickerSuggestions" :key="sug.code" class="flex items-center justify-between gap-3 rounded-md border px-3 py-2 text-sm" :class="confidenceClass[sug.confidence_label]">
            <span class="font-mono">{{ sug.code }} — {{ sug.name }}</span>
            <button type="button" class="rounded-md bg-blue-600 px-3 py-1 text-xs font-semibold text-white hover:bg-blue-700" @click="accountPickerRowId && doAssignAccount(accountPickerRowId, sug.code, accountPickerKind)">
              {{ t('admin.bankReconcile.assign') }}
            </button>
          </li>
        </ul>
      </div>
      <ul class="mt-3 max-h-72 divide-y divide-gray-100 overflow-y-auto">
        <li v-for="a in filteredAccounts" :key="a.code" class="flex items-center justify-between gap-3 py-2 text-sm">
          <span class="font-mono">{{ a.code }} — {{ a.name }}</span>
          <button type="button" class="rounded-md bg-blue-600 px-3 py-1 text-xs font-semibold text-white hover:bg-blue-700" @click="accountPickerRowId && doAssignAccount(accountPickerRowId, a.code, accountPickerKind)">
            {{ t('admin.bankReconcile.assign') }}
          </button>
        </li>
      </ul>
    </Modal>

    <!-- Dismiss reason modal -->
    <Modal v-model:open="dismissOpen" size="md" :title="t('admin.bankReconcile.dismissTitle')">
      <p class="text-sm text-gray-700">{{ t('admin.bankReconcile.dismissHint') }}</p>
      <fieldset class="mt-4 space-y-2">
        <label v-for="r in DISMISS_REASONS" :key="r" class="flex cursor-pointer items-start gap-2 rounded-md border border-gray-200 p-3 hover:bg-gray-50">
          <input v-model="dismissReason" type="radio" :value="r" class="mt-0.5" />
          <div>
            <p class="text-sm font-medium">{{ t('admin.bankReconcile.reasons.' + r) }}</p>
            <p class="text-xs text-gray-500">{{ t('admin.bankReconcile.reasonHints.' + r) }}</p>
          </div>
        </label>
      </fieldset>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button type="button" class="rounded-md bg-white px-3 py-2 text-sm font-semibold text-gray-700 ring-1 ring-gray-300 hover:bg-gray-50" @click="dismissOpen = false">
            {{ t('common.cancel') }}
          </button>
          <button type="button" :disabled="!dismissReason" class="rounded-md bg-amber-600 px-3 py-2 text-sm font-semibold text-white hover:bg-amber-700 disabled:opacity-50" @click="submitDismiss">
            {{ t('admin.bankReconcile.dismiss') }}
          </button>
        </div>
      </template>
    </Modal>
  </div>
</template>
