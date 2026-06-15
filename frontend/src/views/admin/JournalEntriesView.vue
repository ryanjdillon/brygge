<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useConfirm } from '@/stores/confirm'
import {
  Plus,
  ChevronDown,
  ChevronUp,
  ChevronRight,
  Send,
  Ban,
  RefreshCw,
  Lock,
  Unlock,
  Search,
} from 'lucide-vue-next'
import {
  useFiscalPeriods,
  useJournalEntries,
  usePostEntry,
  useVoidEntry,
  useCreatePeriod,
  useClosePeriod,
  useReopenPeriod,
  useSyncPayments,
  useSyncInvoices,
  useRebuildInvoiceBilags,
  type JournalEntry,
} from '@/composables/useAccounting'
import Select from '@/components/ui/form/Select.vue'
import NumberInput from '@/components/ui/form/NumberInput.vue'

const { t } = useI18n()

const { data: periods } = useFiscalPeriods()

const selectedPeriodId = ref('')
const statusFilter = ref('all')
const sourceFilter = ref('all')
const searchQ = ref('')
const sortBy = ref('entry_number')
const sortDir = ref<'asc' | 'desc'>('desc')
const expandedId = ref<string | null>(null)
const syncing = ref(false)
const syncMessage = ref('')
const showCreateYear = ref(false)

function toggleSort(col: string) {
  if (sortBy.value === col) {
    sortDir.value = sortDir.value === 'desc' ? 'asc' : 'desc'
  } else {
    sortBy.value = col
    sortDir.value = 'desc'
  }
}

const nextYear = computed(() => {
  if (!periods.value?.length) return new Date().getFullYear()
  const maxYear = Math.max(...periods.value.map(p => p.year))
  return maxYear + 1
})
const newYear = ref<number | null>(0)
watch(nextYear, (val) => { newYear.value = val }, { immediate: true })

watch(periods, (val) => {
  if (val && val.length > 0 && !selectedPeriodId.value) {
    const open = val.find(p => p.status === 'open')
    selectedPeriodId.value = open?.id ?? val[0].id
  }
}, { immediate: true })

const selectedPeriod = computed(() =>
  periods.value?.find(p => p.id === selectedPeriodId.value),
)

const hasPeriods = computed(() => (periods.value?.length ?? 0) > 0)

const { data: entries, isLoading } = useJournalEntries(selectedPeriodId, {
  status: statusFilter,
  source: sourceFilter,
  q: searchQ,
  sortBy,
  sortDir,
})

const postMutation = usePostEntry()
const voidMutation = useVoidEntry()
const createPeriodMutation = useCreatePeriod()
const closePeriodMutation = useClosePeriod()
const reopenPeriodMutation = useReopenPeriod()
const syncPaymentsMutation = useSyncPayments()
const syncInvoicesMutation = useSyncInvoices()
const rebuildInvoicesMutation = useRebuildInvoiceBilags()
const askConfirm = useConfirm()

const statusLabels = computed<Record<string, string>>(() => ({
  draft: t('admin.accounting.journal.draft'),
  posted: t('admin.accounting.journal.posted'),
  voided: t('admin.accounting.journal.voided'),
}))

const statusColors: Record<string, string> = {
  draft: 'bg-yellow-100 text-yellow-800',
  posted: 'bg-green-100 text-green-800',
  voided: 'bg-red-100 text-red-800',
}

const sourceLabels = computed<Record<string, string>>(() => ({
  manual: t('admin.accounting.journal.sourceManual'),
  sync_payment: t('admin.accounting.journal.sourcePaymentSync'),
  sync_invoice: t('admin.accounting.journal.sourceInvoiceSync'),
  bank_import: t('admin.accounting.journal.sourceBankImport'),
}))

const accountTypeColors: Record<string, string> = {
  asset: 'bg-blue-100 text-blue-800',
  liability: 'bg-amber-100 text-amber-800',
  revenue: 'bg-green-100 text-green-800',
  expense: 'bg-red-100 text-red-800',
}

function toggleExpand(entry: JournalEntry) {
  expandedId.value = expandedId.value === entry.id ? null : entry.id
}

function handlePost(entryId: string) {
  postMutation.mutate(entryId)
}

function handleVoid(entryId: string) {
  if (confirm(t('admin.accounting.journal.confirmVoid'))) {
    voidMutation.mutate(entryId)
  }
}

async function handleSync() {
  if (!selectedPeriodId.value) return
  syncing.value = true
  syncMessage.value = ''

  try {
    const payResult = await new Promise<{ synced: number }>((resolve, reject) => {
      syncPaymentsMutation.mutate(
        { period_id: selectedPeriodId.value },
        { onSuccess: resolve, onError: reject },
      )
    })

    const invResult = await new Promise<{ synced: number }>((resolve, reject) => {
      syncInvoicesMutation.mutate(
        { period_id: selectedPeriodId.value },
        { onSuccess: resolve, onError: reject },
      )
    })

    syncMessage.value = `${t('admin.accounting.journal.updateComplete')}: ${payResult.synced} ${t('admin.accounting.journal.paymentsSynced')}, ${invResult.synced} ${t('admin.accounting.journal.invoicesSynced')}`
  } catch (err) {
    syncMessage.value = `${t('common.error')}: ${(err as Error).message}`
  } finally {
    syncing.value = false
  }
}

const rebuilding = ref(false)
async function handleRebuildInvoiceBilags() {
  if (!selectedPeriodId.value) return
  const periodLabel = periods.value?.find((p) => p.id === selectedPeriodId.value)?.year ?? ''
  const ok = await askConfirm({
    title: t('admin.accounting.journal.rebuildInvoiceBilagsTitle'),
    body: t('admin.accounting.journal.rebuildInvoiceBilagsBody', { year: periodLabel }),
    confirmLabel: t('admin.accounting.journal.rebuildInvoiceBilagsConfirm'),
    cancelLabel: t('common.cancel'),
    tone: 'danger',
  })
  if (!ok) return

  rebuilding.value = true
  syncMessage.value = ''
  try {
    const result = await new Promise<{ deleted: number; resynced: number; skipped: number }>((resolve, reject) => {
      rebuildInvoicesMutation.mutate(
        { period_id: selectedPeriodId.value },
        { onSuccess: resolve, onError: reject },
      )
    })
    syncMessage.value = t('admin.accounting.journal.rebuildInvoiceBilagsDone', {
      deleted: result.deleted,
      resynced: result.resynced,
      skipped: result.skipped,
    })
  } catch (err) {
    syncMessage.value = `${t('common.error')}: ${(err as Error).message}`
  } finally {
    rebuilding.value = false
  }
}

function handleCreatePeriod() {
  if (newYear.value == null) return
  createPeriodMutation.mutate({ year: newYear.value }, {
    onSuccess: (period) => {
      selectedPeriodId.value = period.id
      showCreateYear.value = false
      newYear.value = nextYear.value
      handleSync()
    },
  })
}

function handleClosePeriod() {
  if (!selectedPeriodId.value) return
  closePeriodMutation.mutate(selectedPeriodId.value)
}

function handleReopenPeriod() {
  if (!selectedPeriodId.value) return
  reopenPeriodMutation.mutate(selectedPeriodId.value)
}


const periodOptions = computed(() =>
  (periods.value ?? []).map((p) => ({ value: p.id, label: String(p.year) })),
)

const statusFilterOptions = computed(() => [
  { value: 'all', label: t('admin.accounting.journal.allStatuses') },
  { value: 'draft', label: t('admin.accounting.journal.draft') },
  { value: 'posted', label: t('admin.accounting.journal.posted') },
  { value: 'voided', label: t('admin.accounting.journal.voided') },
])

const sourceFilterOptions = computed(() => [
  { value: 'all', label: t('admin.accounting.journal.allSources') },
  { value: 'manual', label: t('admin.accounting.journal.sourceManual') },
  { value: 'sync_payment', label: t('admin.accounting.journal.sourcePaymentSync') },
  { value: 'sync_invoice', label: t('admin.accounting.journal.sourceInvoiceSync') },
  { value: 'bank_import', label: t('admin.accounting.journal.sourceBankImport') },
])

function formatNOK(amount: number): string {
  if (!amount) return '-'
  return new Intl.NumberFormat('nb-NO', { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(amount)
}
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.accounting.journal.title') }}</h1>

    <!-- Top bar: period management -->
    <div class="mt-4 rounded-lg border border-gray-200 bg-white p-4">
      <div v-if="!hasPeriods" class="flex flex-wrap items-center gap-3">
        <p class="text-sm text-gray-500">{{ t('admin.accounting.journal.noPeriods') }}</p>
        <div class="w-24">
          <NumberInput v-model="newYear" :min="2000" :max="2100" />
        </div>
        <button
          class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          :disabled="createPeriodMutation.isPending.value"
          @click="handleCreatePeriod"
        >
          <Plus class="h-4 w-4" />
          {{ t('admin.accounting.journal.createPeriod') }}
        </button>
      </div>

      <div v-else class="flex flex-wrap items-center gap-3">
        <div class="flex items-center gap-2">
          <label class="text-sm font-medium text-gray-700">{{ t('admin.accounting.journal.period') }}:</label>
          <Select v-model="selectedPeriodId" :options="periodOptions" width="content" />
        </div>

        <span
          v-if="selectedPeriod"
          :class="[
            'inline-flex rounded-full px-2.5 py-0.5 text-xs font-medium',
            selectedPeriod.status === 'open' ? 'bg-green-100 text-green-800' : 'bg-gray-100 text-gray-800',
          ]"
        >
          {{ selectedPeriod.status === 'open' ? t('admin.accounting.periods.open') : t('admin.accounting.periods.closed') }}
        </span>

        <button
          v-if="selectedPeriod?.status === 'open'"
          class="inline-flex items-center gap-1.5 rounded-md border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
          :disabled="closePeriodMutation.isPending.value"
          :title="t('admin.accounting.journal.closePeriodTooltip')"
          @click="handleClosePeriod"
        >
          <Lock class="h-4 w-4" />
          {{ t('admin.accounting.journal.closePeriod') }}
        </button>
        <button
          v-if="selectedPeriod?.status === 'closed'"
          class="inline-flex items-center gap-1.5 rounded-md border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
          :disabled="reopenPeriodMutation.isPending.value"
          :title="t('admin.accounting.journal.reopenPeriodTooltip')"
          @click="handleReopenPeriod"
        >
          <Unlock class="h-4 w-4" />
          {{ t('admin.accounting.journal.reopenPeriod') }}
        </button>

        <div class="ml-auto flex items-center gap-2">
          <div v-if="showCreateYear" class="w-20">
            <NumberInput v-model="newYear" :min="2000" :max="2100" />
          </div>
          <button
            :class="[
              'inline-flex items-center gap-1.5 rounded-md px-3 py-2 text-sm font-medium disabled:opacity-50',
              showCreateYear
                ? 'bg-blue-600 text-white hover:bg-blue-700'
                : 'border border-gray-300 text-gray-700 hover:bg-gray-50',
            ]"
            :title="t('admin.accounting.journal.createPeriodTooltip')"
            :disabled="createPeriodMutation.isPending.value"
            @click="showCreateYear ? handleCreatePeriod() : (showCreateYear = true)"
          >
            <Plus class="h-4 w-4" />
            {{ t('admin.accounting.journal.createPeriod') }}
          </button>
          <button
            class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
            :disabled="syncing || !selectedPeriodId"
            :title="t('admin.accounting.journal.updatePeriodTooltip')"
            @click="handleSync"
          >
            <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': syncing }" />
            {{ syncing ? t('admin.accounting.journal.updating') : t('admin.accounting.journal.updatePeriod') }}
          </button>
          <button
            class="inline-flex items-center gap-1.5 rounded-md border border-red-300 bg-white px-4 py-2 text-sm font-medium text-red-700 hover:bg-red-50 disabled:opacity-50"
            :disabled="rebuilding || !selectedPeriodId"
            :title="t('admin.accounting.journal.rebuildInvoiceBilagsTooltip')"
            data-testid="rebuild-invoice-bilags"
            @click="handleRebuildInvoiceBilags"
          >
            <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': rebuilding }" />
            {{ rebuilding ? t('admin.accounting.journal.updating') : t('admin.accounting.journal.rebuildInvoiceBilags') }}
          </button>
        </div>
      </div>

      <p
        v-if="syncMessage"
        class="mt-2 text-sm"
        :class="syncMessage.startsWith(t('common.error')) ? 'text-red-600' : 'text-green-600'"
      >
        {{ syncMessage }}
      </p>

    </div>

    <!-- Action bar -->
    <div class="mt-4 flex flex-wrap items-center gap-3">
      <RouterLink
        to="/admin/accounting/journal/new"
        class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
      >
        <Plus class="h-4 w-4" />
        {{ t('admin.accounting.journal.newEntry') }}
      </RouterLink>

      <div class="relative">
        <Search class="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
        <input
          v-model="searchQ"
          type="search"
          class="rounded-md border border-gray-300 py-2 pl-8 pr-3 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          :placeholder="t('common.search')"
        />
      </div>

      <div class="flex items-center gap-2">
        <label class="text-sm font-medium text-gray-700">{{ t('admin.accounting.journal.status') }}:</label>
        <Select v-model="statusFilter" :options="statusFilterOptions" width="content" />
      </div>

      <div class="flex items-center gap-2">
        <label class="text-sm font-medium text-gray-700">{{ t('admin.accounting.journal.source') }}:</label>
        <Select v-model="sourceFilter" :options="sourceFilterOptions" width="content" />
      </div>
    </div>

    <!-- Table -->
    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th class="w-8 px-2 py-3"></th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              <button class="inline-flex items-center gap-1 hover:text-gray-800" :title="t('admin.accounting.journal.tooltipEntryNumber')" @click="toggleSort('entry_number')">
                {{ t('admin.accounting.journal.entryNumber') }}
                <ChevronUp v-if="sortBy === 'entry_number' && sortDir === 'asc'" class="h-3 w-3" />
                <ChevronDown v-else-if="sortBy === 'entry_number'" class="h-3 w-3" />
              </button>
            </th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              <button class="inline-flex items-center gap-1 hover:text-gray-800" :title="t('admin.accounting.journal.tooltipDate')" @click="toggleSort('entry_date')">
                {{ t('admin.accounting.journal.date') }}
                <ChevronUp v-if="sortBy === 'entry_date' && sortDir === 'asc'" class="h-3 w-3" />
                <ChevronDown v-else-if="sortBy === 'entry_date'" class="h-3 w-3" />
              </button>
            </th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              <button class="inline-flex items-center gap-1 hover:text-gray-800" :title="t('admin.accounting.journal.tooltipDescription')" @click="toggleSort('description')">
                {{ t('admin.accounting.journal.description') }}
                <ChevronUp v-if="sortBy === 'description' && sortDir === 'asc'" class="h-3 w-3" />
                <ChevronDown v-else-if="sortBy === 'description'" class="h-3 w-3" />
              </button>
            </th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              <span class="inline-flex items-center gap-1" :title="t('admin.accounting.journal.tooltipAccounts')">{{ t('admin.accounting.accounts.code') }}</span>
            </th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              <button class="inline-flex items-center gap-1 hover:text-gray-800" :title="t('admin.accounting.journal.tooltipStatus')" @click="toggleSort('status')">
                {{ t('admin.accounting.journal.status') }}
                <ChevronUp v-if="sortBy === 'status' && sortDir === 'asc'" class="h-3 w-3" />
                <ChevronDown v-else-if="sortBy === 'status'" class="h-3 w-3" />
              </button>
            </th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              <button class="inline-flex items-center gap-1 hover:text-gray-800" :title="t('admin.accounting.journal.tooltipSource')" @click="toggleSort('source')">
                {{ t('admin.accounting.journal.source') }}
                <ChevronUp v-if="sortBy === 'source' && sortDir === 'asc'" class="h-3 w-3" />
                <ChevronDown v-else-if="sortBy === 'source'" class="h-3 w-3" />
              </button>
            </th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.accounting.journal.actions') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <template v-for="entry in entries" :key="entry.id">
            <tr class="cursor-pointer hover:bg-gray-50" @click="toggleExpand(entry)">
              <td class="px-2 py-3 text-gray-400">
                <component :is="expandedId === entry.id ? ChevronDown : ChevronRight" class="h-4 w-4" />
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-sm font-mono text-gray-900">{{ entry.entry_number }}</td>
              <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ entry.entry_date }}</td>
              <td class="px-4 py-3 text-sm text-gray-900">{{ entry.description }}</td>
              <td class="px-4 py-3">
                <div v-if="entry.lines?.length" class="flex flex-wrap gap-1">
                  <span
                    v-for="line in entry.lines"
                    :key="line.id"
                    :class="['inline-flex rounded px-1.5 py-0.5 font-mono text-xs font-semibold', accountTypeColors[line.account_type] ?? 'bg-gray-100 text-gray-800']"
                  >
                    {{ line.account_code }}
                  </span>
                </div>
                <span v-else class="text-sm text-gray-400">-</span>
              </td>
              <td class="whitespace-nowrap px-4 py-3">
                <span
                  :class="[
                    'inline-flex rounded-full px-2.5 py-0.5 text-xs font-medium',
                    statusColors[entry.status] ?? 'bg-gray-100 text-gray-800',
                  ]"
                >
                  {{ statusLabels[entry.status] ?? entry.status }}
                </span>
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">
                {{ sourceLabels[entry.source] ?? entry.source }}
              </td>
              <td class="whitespace-nowrap px-4 py-3" @click.stop>
                <div class="flex items-center gap-2">
                  <button
                    v-if="entry.status === 'draft'"
                    class="text-green-600 hover:text-green-800 disabled:opacity-50"
                    :disabled="postMutation.isPending.value"
                    :title="t('admin.accounting.journal.post')"
                    @click="handlePost(entry.id)"
                  >
                    <Send class="h-4 w-4" />
                  </button>
                  <button
                    v-if="entry.status === 'posted'"
                    class="text-red-600 hover:text-red-800 disabled:opacity-50"
                    :disabled="voidMutation.isPending.value"
                    :title="t('admin.accounting.journal.void')"
                    @click="handleVoid(entry.id)"
                  >
                    <Ban class="h-4 w-4" />
                  </button>
                </div>
              </td>
            </tr>
            <tr v-if="expandedId === entry.id">
              <td colspan="8" class="bg-gray-50 px-8 py-4">
                <div v-if="entry.lines?.length">
                  <table class="min-w-full text-sm">
                    <thead>
                      <tr class="text-xs font-medium uppercase text-gray-500">
                        <th class="pb-2 pr-4 text-left">{{ t('admin.accounting.accounts.code') }}</th>
                        <th class="pb-2 pr-4 text-left">{{ t('admin.accounting.accounts.name') }}</th>
                        <th class="pb-2 pr-4 text-right">{{ t('admin.accounting.journalForm.debit') }}</th>
                        <th class="pb-2 pr-4 text-right">{{ t('admin.accounting.journalForm.credit') }}</th>
                        <th class="pb-2 pr-4 text-right">{{ t('admin.accounting.journalForm.mva') }}</th>
                        <th class="pb-2 text-left">{{ t('admin.accounting.journal.description') }}</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="line in entry.lines" :key="line.id" class="border-t border-gray-200">
                        <td class="py-2 pr-4">
                          <span :class="['inline-flex rounded px-1.5 py-0.5 font-mono text-sm font-semibold', accountTypeColors[line.account_type] ?? 'bg-gray-100 text-gray-800']">
                            {{ line.account_code }}
                          </span>
                        </td>
                        <td class="py-2 pr-4">{{ line.account_name }}</td>
                        <td class="py-2 pr-4 text-right">{{ formatNOK(line.debit) }}</td>
                        <td class="py-2 pr-4 text-right">{{ formatNOK(line.credit) }}</td>
                        <td class="py-2 pr-4 text-right">{{ formatNOK(line.mva_amount) }}</td>
                        <td class="py-2">{{ line.description }}</td>
                      </tr>
                    </tbody>
                  </table>
                </div>
                <p v-else class="text-sm text-gray-500">{{ t('admin.accounting.journal.noEntries') }}</p>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
      <p v-if="!entries?.length" class="mt-4 text-center text-sm text-gray-500">
        {{ t('admin.accounting.journal.noEntries') }}
      </p>
    </div>
  </div>
</template>
