<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { ExternalLink } from 'lucide-vue-next'
import { formatNOK, formatDate } from '@/lib/format'
import { invoiceStatus, invoiceStatusClass, isNothingDue, type MemberInvoice } from '@/composables/useMyInvoices'

// Reusable list of the member's own invoices. Used both on the portal
// dashboard (recent/unpaid summaries) and the full Fakturas page.
defineProps<{
  invoices: MemberInvoice[]
  emptyText?: string
}>()

const { t } = useI18n()

function statusLabel(inv: MemberInvoice): string {
  return t(`portal.invoices.status.${invoiceStatus(inv)}`)
}

// The line under a faktura a card refund returned money for, or under the
// credit note that refund issued.
function refundNote(inv: MemberInvoice): string {
  if (inv.refund_document) return t('portal.invoices.refundCreditNote')
  if (inv.refund_state === 'full') return t('portal.invoices.refundedFull')
  if (inv.refund_state === 'partial') return t('portal.invoices.refundedPartly', { amount: formatNOK(inv.refunded_amount ?? 0) })
  return ''
}
</script>

<template>
  <div>
    <ul v-if="invoices.length" class="divide-y divide-gray-100">
      <li
        v-for="inv in invoices"
        :key="inv.id"
        class="py-3"
      >
        <!-- Stacked on a phone, where the title, amount, status and action do
             not fit side by side; one row from sm. -->
        <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between sm:gap-3">
          <div class="min-w-0">
            <p class="truncate text-sm font-medium text-gray-900">
              #{{ inv.invoice_number }}
              <span v-if="inv.price_item_name || inv.description" class="font-normal text-gray-500">
                — {{ inv.price_item_name || inv.description }}
              </span>
            </p>
            <p class="mt-0.5 text-xs text-gray-500">
              {{ t('portal.invoices.due') }}: {{ formatDate(inv.due_date) }}
              <span v-if="inv.kid_number && !isNothingDue(inv)"> · KID {{ inv.kid_number }}</span>
            </p>
          </div>
          <div class="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-2">
            <span class="text-sm font-semibold tabular-nums text-gray-900">
              {{ formatNOK(inv.total_amount) }}
            </span>
            <span
              :class="['inline-flex rounded-full px-2 py-0.5 text-xs font-medium', invoiceStatusClass(inv)]"
            >
              {{ statusLabel(inv) }}
            </span>
            <!-- Trailing per-row action. Defaults to the "open PDF" link; the
                 public payables page overrides it with a "Pay" button. -->
            <slot name="actions" :invoice="inv">
              <a
                v-if="inv.has_pdf"
                :href="`/api/v1/members/me/invoices/${inv.id}/pdf`"
                target="_blank"
                rel="noopener noreferrer"
                class="inline-flex min-h-11 items-center gap-1 rounded-md border border-gray-300 px-2 py-1 text-xs font-medium text-gray-700 hover:bg-gray-50"
                :title="t('portal.invoices.open')"
              >
                <ExternalLink class="h-3.5 w-3.5" />
                {{ t('portal.invoices.open') }}
              </a>
            </slot>
          </div>
        </div>
        <!-- A card refund: what went back to the card and the credit notes
             that say so. Nothing here is owed or to be paid. -->
        <p v-if="refundNote(inv)" class="mt-1.5 text-xs text-sky-800" data-testid="refund-note">
          {{ refundNote(inv) }}
          <template v-for="(n, i) in inv.refund_credit_notes ?? []" :key="n.id">
            <template v-if="i === 0"> · </template><template v-else>, </template>
            <a
              v-if="n.has_pdf"
              :href="`/api/v1/members/me/invoices/${n.id}/pdf`"
              target="_blank"
              rel="noopener noreferrer"
              class="-my-3.5 inline-block py-3.5 underline"
            >{{ t('portal.invoices.creditNoteNumber', { number: n.number }) }}</a>
            <span v-else>{{ t('portal.invoices.creditNoteNumber', { number: n.number }) }}</span>
          </template>
        </p>
      </li>
    </ul>
    <p v-else class="py-6 text-center text-sm text-gray-500">
      {{ emptyText ?? t('portal.invoices.none') }}
    </p>
  </div>
</template>
