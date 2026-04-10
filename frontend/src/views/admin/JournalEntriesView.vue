<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  Plus,
  ChevronDown,
  ChevronRight,
  Send,
  Ban,
  RefreshCw,
  Lock,
  Unlock,
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
  type JournalEntry,
} from '@/composables/useAccounting'

const { t } = useI18n()

const { data: periods } = useFiscalPeriods()

const selectedPeriodId = ref('')
const statusFilter = ref('all')
const expandedId = ref<string | null>(null)
const newYear = ref(new Date().getFullYear())
const syncing = ref(false)
const syncMessage = ref('')
const showCreatePeriod = ref(false)

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

const statusRef = computed(() => statusFilter.value)
const { data: entries, isLoading } = useJournalEntries(selectedPeriodId, statusRef)

const postMutation = usePostEntry()
const voidMutation = useVoidEntry()
const createPeriodMutation = useCreatePeriod()
const closePeriodMutation = useClosePeriod()
const reopenPeriodMutation = useReopenPeriod()
const syncPaymentsMutation = useSyncPayments()
const syncInvoicesMutation = useSyncInvoices()

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

function handleCreatePeriod() {
  createPeriodMutation.mutate({ year: newYear.value }, {
    onSuccess: (period) => {
      selectedPeriodId.value = period.id
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
        <input
          v-model.number="newYear"
          type="number"
          min="2000"
          max="2100"
          class="w-24 rounded-md border border-gray-300 px-3 py-2 text-sm"
        />
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
          <select v-model="selectedPeriodId" class="rounded-md border border-gray-300 px-3 py-2 text-sm">
            <option v-for="p in periods" :key="p.id" :value="p.id">{{ p.year }}</option>
          </select>
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
          <button
            class="inline-flex items-center gap-1.5 rounded-md border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
            :title="t('admin.accounting.journal.createPeriodTooltip')"
            @click="showCreatePeriod = !showCreatePeriod"
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
        </div>
      </div>

      <p
        v-if="syncMessage"
        class="mt-2 text-sm"
        :class="syncMessage.startsWith(t('common.error')) ? 'text-red-600' : 'text-green-600'"
      >
        {{ syncMessage }}
      </p>

      <!-- Inline create period form -->
      <div v-if="showCreatePeriod" class="mt-3 flex items-center gap-3 border-t border-gray-100 pt-3">
        <input
          v-model.number="newYear"
          type="number"
          min="2000"
          max="2100"
          class="w-24 rounded-md border border-gray-300 px-3 py-2 text-sm"
        />
        <button
          class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          :disabled="createPeriodMutation.isPending.value"
          @click="handleCreatePeriod"
        >
          <Plus class="h-4 w-4" />
          {{ t('admin.accounting.journal.createPeriod') }}
        </button>
      </div>
    </div>

    <!-- Action bar -->
    <div class="mt-4 flex flex-wrap items-center gap-4">
      <RouterLink
        to="/admin/accounting/journal/new"
        class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
      >
        <Plus class="h-4 w-4" />
        {{ t('admin.accounting.journal.newEntry') }}
      </RouterLink>

      <div class="flex items-center gap-2">
        <label class="text-sm font-medium text-gray-700">{{ t('admin.accounting.journal.status') }}:</label>
        <select v-model="statusFilter" class="rounded-md border border-gray-300 px-3 py-2 text-sm">
          <option value="all">{{ t('admin.accounting.journal.allStatuses') }}</option>
          <option value="draft">{{ t('admin.accounting.journal.draft') }}</option>
          <option value="posted">{{ t('admin.accounting.journal.posted') }}</option>
          <option value="voided">{{ t('admin.accounting.journal.voided') }}</option>
        </select>
      </div>
    </div>

    <!-- Table -->
    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th class="w-8 px-2 py-3"></th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.accounting.journal.entryNumber') }}</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.accounting.journal.date') }}</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.accounting.journal.description') }}</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.accounting.journal.status') }}</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.accounting.journal.source') }}</th>
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
                <button
                  v-if="entry.status === 'draft'"
                  class="inline-flex items-center gap-1 rounded-md bg-green-50 px-2.5 py-1.5 text-xs font-medium text-green-700 hover:bg-green-100"
                  :disabled="postMutation.isPending.value"
                  @click="handlePost(entry.id)"
                >
                  <Send class="h-3.5 w-3.5" />
                  {{ t('admin.accounting.journal.post') }}
                </button>
                <button
                  v-if="entry.status === 'posted'"
                  class="inline-flex items-center gap-1 rounded-md bg-red-50 px-2.5 py-1.5 text-xs font-medium text-red-700 hover:bg-red-100"
                  :disabled="voidMutation.isPending.value"
                  @click="handleVoid(entry.id)"
                >
                  <Ban class="h-3.5 w-3.5" />
                  {{ t('admin.accounting.journal.void') }}
                </button>
              </td>
            </tr>
            <tr v-if="expandedId === entry.id">
              <td colspan="7" class="bg-gray-50 px-8 py-4">
                <table v-if="entry.lines?.length" class="min-w-full text-sm">
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
                      <td class="py-2 pr-4 font-mono">{{ line.account_code }}</td>
                      <td class="py-2 pr-4">{{ line.account_name }}</td>
                      <td class="py-2 pr-4 text-right">{{ formatNOK(line.debit) }}</td>
                      <td class="py-2 pr-4 text-right">{{ formatNOK(line.credit) }}</td>
                      <td class="py-2 pr-4 text-right">{{ formatNOK(line.mva_amount) }}</td>
                      <td class="py-2">{{ line.description }}</td>
                    </tr>
                  </tbody>
                </table>
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
