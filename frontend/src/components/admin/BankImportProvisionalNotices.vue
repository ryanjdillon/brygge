<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatDate } from '@/lib/format'
import type { BankImportResult } from '@/composables/useBankImports'

// Notices about in-flight rows after a bank statement upload (BRY-378). A
// statement exported on the day it ends can carry transfers still under way
// as bare "Overførsel" rows; the settled rows only come with a later export
// of that day, which refreshes or supersedes them.
const props = defineProps<{ result: BankImportResult }>()

const { t, locale } = useI18n()

function localIsoToday(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

const settled = computed(() => (props.result.refreshed ?? 0) + (props.result.superseded ?? 0))
const exportDay = computed(() => {
  const last = props.result.last_row_date
  if (!last) return false
  return last >= localIsoToday() || (props.result.provisional_rows ?? 0) > 0
})
</script>

<template>
  <p v-if="settled" class="mt-2 rounded bg-brand-50 px-2 py-1 text-brand-800" data-testid="bank-provisional-settled">
    {{ t('admin.bankImports.provisionalSettledNotice', { count: settled }) }}
  </p>
  <p v-if="exportDay" class="mt-2 rounded bg-yellow-50 px-2 py-1 text-yellow-800" data-testid="bank-export-day-notice">
    {{ t('admin.bankImports.exportDayNotice', { date: formatDate(props.result.last_row_date, locale) }) }}
  </p>
  <div v-if="props.result.reference_mismatches?.length" class="mt-2 rounded bg-yellow-50 px-2 py-1 text-yellow-800" data-testid="bank-reference-mismatch">
    <p>{{ t('admin.bankImports.referenceMismatchNotice', { count: props.result.reference_mismatches.length }) }}</p>
    <ul class="list-disc pl-5">
      <li v-for="m in props.result.reference_mismatches" :key="m.row_id">
        Ref {{ m.reference }}: {{ formatDate(m.date, locale) }} · {{ m.amount }} kr → {{ formatDate(m.new_date, locale) }} · {{ m.new_amount }} kr
      </li>
    </ul>
  </div>
  <p v-if="props.result.provisional_ambiguous?.length" class="mt-2 rounded bg-yellow-50 px-2 py-1 text-yellow-800" data-testid="bank-provisional-ambiguous">
    {{ t('admin.bankImports.provisionalAmbiguousNotice', { count: props.result.provisional_ambiguous.length }) }}
  </p>
</template>
