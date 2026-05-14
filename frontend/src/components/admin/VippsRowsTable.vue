<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { VippsImportRow } from '@/composables/useBankImports'

defineProps<{
  rows: VippsImportRow[]
}>()

const { t } = useI18n()

function nok(n: number): string {
  return new Intl.NumberFormat('nb-NO', { style: 'currency', currency: 'NOK' }).format(n)
}
</script>

<template>
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
        <tr v-for="row in rows" :key="row.id" class="border-t border-gray-100">
          <td class="whitespace-nowrap px-3 py-2">{{ row.row_type }}</td>
          <td class="whitespace-nowrap px-3 py-2 tabular-nums text-gray-600">
            {{ row.tx_at ?? row.booking_date ?? '' }}
          </td>
          <td class="max-w-xs truncate px-3 py-2" :title="row.customer_name">{{ row.customer_name }}</td>
          <td class="whitespace-nowrap px-3 py-2 text-right tabular-nums">{{ nok(row.amount) }}</td>
          <td class="whitespace-nowrap px-3 py-2 text-right tabular-nums text-gray-500">{{ nok(row.fee) }}</td>
          <td
            class="max-w-xs truncate px-3 py-2 text-xs text-gray-500"
            :title="row.order_id || row.settlement_number"
          >
            {{ row.order_id || row.settlement_number }}
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
