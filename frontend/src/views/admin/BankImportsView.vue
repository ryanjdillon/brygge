<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQueryClient } from '@tanstack/vue-query'
import {
  useBankFormats,
  useBankImport,
  useVippsImports,
  useVippsImport,
  uploadBankCSV,
  uploadVippsCSV,
  previewVippsReconcile,
  confirmVippsReconcile,
  type BankImportResult,
  type VippsImportResult,
  type VippsReconcilePreview,
} from '@/composables/useBankImports'
import { useFiscalPeriods, useAccountsList } from '@/composables/useAccounting'

const { t } = useI18n()
const queryClient = useQueryClient()

const { data: periods } = useFiscalPeriods()
const { data: formats } = useBankFormats()
const { data: vippsImports } = useVippsImports()
const { data: accounts } = useAccountsList()

const bankAccounts = computed(() =>
  (accounts.value ?? []).filter((a) => a.is_active && a.code.startsWith('19')),
)

const selectedPeriod = computed<string>(() => {
  const open = periods.value?.find((p) => p.status === 'open')
  return open?.id ?? periods.value?.[0]?.id ?? ''
})

// ── Bank upload ──────────────────────────────────────────────
const bankFile = ref<File | null>(null)
const bankFormat = ref<string>('sparebank-norge-v1')
const bankAccountCode = ref<string>('1920')
const bankError = ref<string | null>(null)
const bankBusy = ref(false)
const bankResult = ref<BankImportResult | null>(null)
const currentBankImportId = ref<string | undefined>(undefined)
const { data: bankRows } = useBankImport(currentBankImportId)

function onBankFile(e: Event) {
  bankFile.value = (e.target as HTMLInputElement).files?.[0] ?? null
}

async function submitBank() {
  if (!bankFile.value || !selectedPeriod.value || !bankAccountCode.value) return
  bankError.value = null
  bankBusy.value = true
  try {
    const res = await uploadBankCSV(
      bankFile.value,
      bankFormat.value,
      selectedPeriod.value,
      bankAccountCode.value,
    )
    bankResult.value = res
    currentBankImportId.value = res.id
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

function onVippsFile(e: Event) {
  vippsFile.value = (e.target as HTMLInputElement).files?.[0] ?? null
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

// ── Reconciliation ──────────────────────────────────────────
const reconcilePreview = ref<VippsReconcilePreview | null>(null)
const reconcileError = ref<string | null>(null)
const reconcileBusy = ref(false)
const reconcileTarget = ref<{ id: string; description: string } | null>(null)

function vippsPattern(desc: string): boolean {
  return /Utb\.\s*\d+\s+Vippsnr\s+\d+/i.test(desc)
}

async function openReconcile(row: { id: string; description: string }) {
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
  if (!reconcilePreview.value || !reconcileTarget.value || !selectedPeriod.value) return
  reconcileBusy.value = true
  try {
    await confirmVippsReconcile(reconcileTarget.value.id, selectedPeriod.value, reconcilePreview.value.lines)
    reconcileTarget.value = null
    reconcilePreview.value = null
    queryClient.invalidateQueries({ queryKey: ['accounting', 'bank-import', currentBankImportId.value] })
  } catch (e: any) {
    reconcileError.value = e?.message ?? 'Confirm failed'
  } finally {
    reconcileBusy.value = false
  }
}

function nok(n: number): string {
  return new Intl.NumberFormat('nb-NO', { style: 'currency', currency: 'NOK' }).format(n)
}
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.bankImports.title') }}</h1>
    <p class="mt-1 text-sm text-gray-500">{{ t('admin.bankImports.subtitle') }}</p>

    <div class="mt-4 flex items-center gap-3 text-sm">
      <label class="font-medium text-gray-700">{{ t('admin.bankImports.period') }}</label>
      <select
        :value="selectedPeriod"
        class="rounded-md border border-gray-300 px-3 py-1.5"
        disabled
      >
        <option v-for="p in periods ?? []" :key="p.id" :value="p.id">
          {{ p.year }} ({{ p.status }})
        </option>
      </select>
    </div>

    <div class="mt-6 grid gap-4 lg:grid-cols-2">
      <!-- Bank upload card -->
      <section class="rounded-lg border border-gray-200 bg-white p-5" data-testid="bank-upload-card">
        <h2 class="text-base font-semibold text-gray-900">{{ t('admin.bankImports.bankCardTitle') }}</h2>
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.bankImports.bankCardDesc') }}</p>

        <div class="mt-3 space-y-2">
          <label class="block text-xs font-medium text-gray-700">{{ t('admin.bankImports.bankAccount') }}</label>
          <select
            v-model="bankAccountCode"
            class="block w-full rounded-md border border-gray-300 px-3 py-1.5 text-sm"
            data-testid="bank-account-select"
          >
            <option v-for="a in bankAccounts" :key="a.code" :value="a.code">
              {{ a.code }} — {{ a.name }}
            </option>
          </select>

          <label class="mt-2 block text-xs font-medium text-gray-700">{{ t('admin.bankImports.format') }}</label>
          <select v-model="bankFormat" class="block w-full rounded-md border border-gray-300 px-3 py-1.5 text-sm">
            <option v-for="f in formats ?? []" :key="f" :value="f">{{ f }}</option>
          </select>

          <input
            type="file"
            accept=".csv,text/csv"
            class="mt-2 block text-sm"
            data-testid="bank-file-input"
            @change="onBankFile"
          />
        </div>

        <p v-if="bankError" class="mt-2 rounded-md bg-red-50 px-2 py-1 text-xs text-red-700">{{ bankError }}</p>

        <div class="mt-3 flex justify-end">
          <button
            type="button"
            :disabled="!bankFile || !selectedPeriod || !bankAccountCode || bankBusy"
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
        </div>
      </section>

      <!-- Vipps upload card -->
      <section class="rounded-lg border border-gray-200 bg-white p-5" data-testid="vipps-upload-card">
        <h2 class="text-base font-semibold text-gray-900">{{ t('admin.bankImports.vippsCardTitle') }}</h2>
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.bankImports.vippsCardDesc') }}</p>

        <input
          type="file"
          accept=".csv,text/csv"
          class="mt-3 block text-sm"
          data-testid="vipps-file-input"
          @change="onVippsFile"
        />

        <p v-if="vippsError" class="mt-2 rounded-md bg-red-50 px-2 py-1 text-xs text-red-700">{{ vippsError }}</p>

        <div class="mt-3 flex justify-end">
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
    </div>

    <!-- Bank rows from current upload -->
    <section v-if="bankRows && bankRows.length" class="mt-8">
      <h2 class="text-base font-semibold text-gray-900">{{ t('admin.bankImports.rowsTitle') }}</h2>
      <div class="mt-3 overflow-x-auto rounded-lg border border-gray-200 bg-white">
        <table class="min-w-full divide-y divide-gray-200 text-sm">
          <thead class="bg-gray-50 text-left text-xs font-medium uppercase tracking-wide text-gray-500">
            <tr>
              <th class="px-3 py-2">{{ t('admin.bankImports.colDate') }}</th>
              <th class="px-3 py-2">{{ t('admin.bankImports.colDescription') }}</th>
              <th class="px-3 py-2 text-right">{{ t('admin.bankImports.colAmount') }}</th>
              <th class="px-3 py-2">{{ t('admin.bankImports.colCounterpart') }}</th>
              <th class="px-3 py-2 text-right" />
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="row in bankRows"
              :key="row.id"
              class="border-t border-gray-100"
              :data-testid="vippsPattern(row.description) ? 'bank-row-vipps' : 'bank-row'"
            >
              <td class="px-3 py-2 text-gray-700 tabular-nums">{{ row.date }}</td>
              <td class="px-3 py-2">{{ row.description }}</td>
              <td class="px-3 py-2 text-right tabular-nums" :class="row.amount < 0 ? 'text-red-700' : 'text-green-700'">
                {{ nok(row.amount) }}
              </td>
              <td class="px-3 py-2 text-gray-600">{{ row.counterpart }}</td>
              <td class="px-3 py-2 text-right">
                <button
                  v-if="vippsPattern(row.description) && !row.journal_entry_id"
                  type="button"
                  class="rounded-md bg-purple-600 px-2 py-1 text-xs font-semibold text-white hover:bg-purple-700"
                  data-testid="reconcile-btn"
                  @click="openReconcile(row)"
                >
                  {{ t('admin.bankImports.reconcile') }}
                </button>
                <span v-else-if="row.journal_entry_id" class="text-xs text-green-700">
                  {{ t('admin.bankImports.bilagCreated') }}
                </span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <!-- Recent Vipps imports list -->
    <section v-if="vippsImports && vippsImports.length" class="mt-8">
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
          <span class="text-xs text-gray-500">{{ v.row_count }} {{ t('admin.bankImports.rows') }}</span>
        </li>
      </ul>
    </section>

    <!-- Vipps rows expanded -->
    <section v-if="vippsRows && vippsRows.length" class="mt-4">
      <div class="overflow-x-auto rounded-lg border border-gray-200 bg-white">
        <table class="min-w-full divide-y divide-gray-200 text-xs">
          <thead class="bg-gray-50 text-left font-medium uppercase tracking-wide text-gray-500">
            <tr>
              <th class="px-3 py-2">{{ t('admin.bankImports.colType') }}</th>
              <th class="px-3 py-2">{{ t('admin.bankImports.colWhen') }}</th>
              <th class="px-3 py-2">{{ t('admin.bankImports.colCustomer') }}</th>
              <th class="px-3 py-2 text-right">{{ t('admin.bankImports.colAmount') }}</th>
              <th class="px-3 py-2 text-right">{{ t('admin.bankImports.colFee') }}</th>
              <th class="px-3 py-2">{{ t('admin.bankImports.colSettlement') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in vippsRows" :key="row.id" class="border-t border-gray-100">
              <td class="px-3 py-2">{{ row.row_type }}</td>
              <td class="px-3 py-2 tabular-nums text-gray-600">{{ row.tx_at ?? row.booking_date ?? '' }}</td>
              <td class="px-3 py-2">{{ row.customer_name }}</td>
              <td class="px-3 py-2 text-right tabular-nums">{{ nok(row.amount) }}</td>
              <td class="px-3 py-2 text-right tabular-nums text-gray-500">{{ nok(row.fee) }}</td>
              <td class="px-3 py-2 text-xs text-gray-500">{{ row.order_id || row.settlement_number }}</td>
            </tr>
          </tbody>
        </table>
      </div>
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
            :disabled="!reconcilePreview?.balanced || reconcileBusy"
            class="rounded-md bg-green-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-green-700 disabled:opacity-50"
            data-testid="reconcile-confirm"
            @click="confirmReconcile"
          >
            {{ reconcileBusy ? t('common.loading') : t('admin.bankImports.confirmBilag') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
