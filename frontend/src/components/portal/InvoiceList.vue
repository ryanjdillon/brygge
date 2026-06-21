<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { ExternalLink } from 'lucide-vue-next'
import { formatNOK, formatDate } from '@/lib/format'
import { isOverdue, type MemberInvoice } from '@/composables/useMyInvoices'

// Reusable list of the member's own invoices. Used both on the portal
// dashboard (recent/unpaid summaries) and the full Fakturas page.
defineProps<{
  invoices: MemberInvoice[]
  emptyText?: string
}>()

const { t } = useI18n()

function statusClass(inv: MemberInvoice): string {
  if (inv.paid) return 'bg-green-100 text-green-800'
  if (isOverdue(inv)) return 'bg-red-100 text-red-800'
  return 'bg-amber-100 text-amber-800'
}

function statusLabel(inv: MemberInvoice): string {
  if (inv.paid) return t('portal.invoices.status.paid')
  if (isOverdue(inv)) return t('portal.invoices.status.overdue')
  return t('portal.invoices.status.unpaid')
}
</script>

<template>
  <div>
    <ul v-if="invoices.length" class="divide-y divide-gray-100">
      <li
        v-for="inv in invoices"
        :key="inv.id"
        class="flex items-center justify-between gap-3 py-3"
      >
        <div class="min-w-0">
          <p class="truncate text-sm font-medium text-gray-900">
            #{{ inv.invoice_number }}
            <span v-if="inv.price_item_name || inv.description" class="font-normal text-gray-500">
              — {{ inv.price_item_name || inv.description }}
            </span>
          </p>
          <p class="mt-0.5 text-xs text-gray-500">
            {{ t('portal.invoices.due') }}: {{ formatDate(inv.due_date) }}
            <span v-if="inv.kid_number"> · KID {{ inv.kid_number }}</span>
          </p>
        </div>
        <div class="flex shrink-0 items-center gap-3">
          <span class="text-sm font-semibold tabular-nums text-gray-900">
            {{ formatNOK(inv.total_amount) }}
          </span>
          <span
            :class="['inline-flex rounded-full px-2 py-0.5 text-xs font-medium', statusClass(inv)]"
          >
            {{ statusLabel(inv) }}
          </span>
          <a
            :href="`/api/v1/members/me/invoices/${inv.id}/pdf`"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex items-center gap-1 rounded-md border border-gray-300 px-2 py-1 text-xs font-medium text-gray-700 hover:bg-gray-50"
            :title="t('portal.invoices.open')"
          >
            <ExternalLink class="h-3.5 w-3.5" />
            {{ t('portal.invoices.open') }}
          </a>
        </div>
      </li>
    </ul>
    <p v-else class="py-6 text-center text-sm text-gray-500">
      {{ emptyText ?? t('portal.invoices.none') }}
    </p>
  </div>
</template>
