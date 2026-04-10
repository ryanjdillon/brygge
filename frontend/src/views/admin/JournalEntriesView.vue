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
} from 'lucide-vue-next'
import {
  useFiscalPeriods,
  useJournalEntries,
  usePostEntry,
  useVoidEntry,
  type JournalEntry,
} from '@/composables/useAccounting'

const { t } = useI18n()

const { data: periods } = useFiscalPeriods()

const selectedPeriodId = ref('')
const statusFilter = ref('all')
const expandedId = ref<string | null>(null)

watch(periods, (val) => {
  if (val && val.length > 0 && !selectedPeriodId.value) {
    const open = val.find(p => p.status === 'open')
    selectedPeriodId.value = open?.id ?? val[0].id
  }
}, { immediate: true })

const statusRef = computed(() => statusFilter.value)
const { data: entries, isLoading } = useJournalEntries(selectedPeriodId, statusRef)

const postMutation = usePostEntry()
const voidMutation = useVoidEntry()

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
  if (confirm('Er du sikker på at du vil annullere dette bilaget? En reverseringspostering vil bli opprettet.')) {
    voidMutation.mutate(entryId)
  }
}

function formatNOK(amount: number): string {
  if (!amount) return '-'
  return new Intl.NumberFormat('nb-NO', { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(amount)
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.accounting.journal.title') }}</h1>
      <RouterLink
        to="/admin/accounting/journal/new"
        class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
      >
        <Plus class="h-4 w-4" />
        {{ t('admin.accounting.journal.newEntry') }}
      </RouterLink>
    </div>

    <div class="mt-4 flex flex-wrap items-center gap-4">
      <div class="flex items-center gap-2">
        <label class="text-sm font-medium text-gray-700">{{ t('admin.accounting.journal.period') }}:</label>
        <select v-model="selectedPeriodId" class="rounded-md border border-gray-300 px-3 py-2 text-sm">
          <option v-for="p in periods" :key="p.id" :value="p.id">
            {{ p.year }} ({{ p.status === 'open' ? t('admin.accounting.periods.open') : t('admin.accounting.periods.closed') }})
          </option>
        </select>
      </div>
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
