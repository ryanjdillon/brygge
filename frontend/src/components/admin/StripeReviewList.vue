<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { StripeReviewItem } from '@/composables/useBankImports'
import { formatNOK, formatDate } from '@/lib/format'

// Read-only list of the Stripe balance transactions waiting for a person,
// with why. Acting on them comes with the manual row actions.
const props = defineProps<{ items: StripeReviewItem[] }>()

const { t, te, locale } = useI18n()

function reasonLabel(reason: string) {
  const key = `admin.bankImports.stripeReviewReasons.${reason}`
  return te(key) ? t(key) : reason
}
</script>

<template>
  <section v-if="props.items.length" class="mt-3 rounded-md border border-yellow-200 bg-yellow-50 px-3 py-2 text-xs text-yellow-900" data-testid="stripe-review-list">
    <h2 class="font-semibold">{{ t('admin.bankImports.stripeReviewTitle', { count: props.items.length }) }}</h2>
    <table class="mt-1 w-full text-left">
      <thead>
        <tr>
          <th class="pr-3 font-medium">{{ t('admin.bankImports.stripeReviewCols.date') }}</th>
          <th class="pr-3 text-right font-medium">{{ t('admin.bankImports.stripeReviewCols.amount') }}</th>
          <th class="pr-3 font-medium">{{ t('admin.bankImports.stripeReviewCols.reason') }}</th>
          <th class="font-medium">{{ t('admin.bankImports.stripeReviewCols.id') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="item in props.items" :key="item.id">
          <td class="pr-3">{{ formatDate(item.date, locale) }}</td>
          <td class="pr-3 text-right tabular-nums">{{ formatNOK(item.amount_ore / 100) }}</td>
          <td class="pr-3">{{ reasonLabel(item.review_reason) }}</td>
          <td class="font-mono">{{ item.balance_transaction_id }}</td>
        </tr>
      </tbody>
    </table>
  </section>
</template>
