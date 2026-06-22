<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { BankImportRow } from '@/composables/useBankImports'
import { formatNOK as nok } from '@/lib/format'

defineProps<{
  rows: BankImportRow[]
  showReconcile?: boolean
}>()

const emit = defineEmits<{
  (e: 'reconcile', row: BankImportRow): void
}>()

const { t } = useI18n()

function vippsPattern(desc: string): boolean {
  return /Utb\.\s*\d+\s+Vippsnr\s+\d+/i.test(desc)
}
</script>

<template>
  <div class="overflow-x-auto rounded-lg border border-gray-200 bg-white">
    <table class="min-w-full divide-y divide-gray-200 text-sm">
      <thead class="bg-gray-50 text-left text-xs font-medium uppercase tracking-wide text-gray-500">
        <tr>
          <th class="px-3 py-2">{{ t('admin.bankImports.colDate') }}</th>
          <th class="px-3 py-2">{{ t('admin.bankImports.colDescription') }}</th>
          <th class="px-3 py-2 text-right">{{ t('admin.bankImports.colAmount') }}</th>
          <th class="px-3 py-2">{{ t('admin.bankImports.colCounterpart') }}</th>
          <th v-if="showReconcile" class="px-3 py-2 text-right" />
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="row in rows"
          :key="row.id"
          class="border-t border-gray-100"
          :data-testid="vippsPattern(row.description) ? 'bank-row-vipps' : 'bank-row'"
        >
          <td class="whitespace-nowrap px-3 py-2 text-gray-700 tabular-nums">{{ row.date }}</td>
          <td class="max-w-md truncate px-3 py-2" :title="row.description">{{ row.description }}</td>
          <td
            class="whitespace-nowrap px-3 py-2 text-right tabular-nums"
            :class="row.amount < 0 ? 'text-red-700' : 'text-green-700'"
          >
            {{ nok(row.amount) }}
          </td>
          <td class="max-w-xs truncate px-3 py-2 text-gray-600" :title="row.counterpart">{{ row.counterpart }}</td>
          <td v-if="showReconcile" class="px-3 py-2 text-right">
            <button
              v-if="vippsPattern(row.description) && !row.journal_entry_id"
              type="button"
              class="rounded-md bg-brand-600 px-2 py-1 text-xs font-semibold text-white hover:bg-brand-700"
              data-testid="reconcile-btn"
              @click="emit('reconcile', row)"
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
</template>
