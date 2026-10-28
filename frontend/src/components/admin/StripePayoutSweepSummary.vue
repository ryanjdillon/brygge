<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { StripePayoutSweepItem, StripePayoutSweepResult } from '@/composables/useBankImports'

// Shows the Stripe payouts a sync or import cleared on its own and why the
// rest stay open: members still to book, a deposit not imported yet, an
// ambiguous or manual payout, a Stripe deposit no payout explains. The
// reasons and any sweep error come from the server.
const props = defineProps<{ result: StripePayoutSweepResult }>()

const { t } = useI18n()

const open = computed(() => [...props.result.waiting, ...props.result.skipped, ...props.result.failed])

function label(p: StripePayoutSweepItem): string {
  return p.payout_id ?? t('admin.bankImports.stripePayoutDeposit', { date: p.bank_date ?? '' })
}
</script>

<template>
  <div class="mt-2" data-testid="stripe-payout-sweep-summary">
    <p>
      {{ t('admin.bankImports.stripePayoutSweepCounts', {
        cleared: props.result.cleared.length,
        waiting: props.result.waiting.length,
        skipped: props.result.skipped.length,
        failed: props.result.failed.length,
      }) }}
    </p>
    <p v-if="props.result.error" class="text-red-700" data-testid="stripe-payout-sweep-error">
      {{ props.result.error }}
    </p>
    <ul v-if="open.length" class="mt-1 space-y-0.5" data-testid="stripe-payout-sweep-open">
      <li v-for="p in open" :key="`${p.payout_id ?? ''}/${p.bank_row_id ?? ''}`">
        {{ label(p) }}: {{ p.reason }}
      </li>
    </ul>
  </div>
</template>
