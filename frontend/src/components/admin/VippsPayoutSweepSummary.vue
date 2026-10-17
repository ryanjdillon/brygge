<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { VippsPayoutSweepResult } from '@/composables/useBankImports'

// Shows the Vipps payouts a sync cleared on its own (BRY-340) and why the
// rest stay open: charges still in Tildel, no matching bank deposit, or a
// refused posting.
const props = defineProps<{ result: VippsPayoutSweepResult }>()

const { t } = useI18n()

const open = computed(() => [...props.result.skipped, ...props.result.failed])
</script>

<template>
  <div class="mt-2" data-testid="vipps-payout-sweep-summary">
    <p>
      {{ t('admin.bankImports.payoutSweepCounts', {
        cleared: props.result.cleared.length,
        waiting: props.result.waiting.length,
        skipped: props.result.skipped.length,
        failed: props.result.failed.length,
      }) }}
    </p>
    <ul v-if="open.length" class="mt-1 space-y-0.5">
      <li v-for="p in open" :key="`${p.msn}/${p.settlement_number}`">
        Utb. {{ p.settlement_number }} (Vippsnr {{ p.msn }}): {{ p.reason }}
      </li>
    </ul>
  </div>
</template>
