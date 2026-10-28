<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { StripeBookResult } from '@/composables/useBankImports'

// Shows what a Stripe booking pass booked and what it left: charges held
// for review (listed in StripeReviewList), rows waiting on a closed fiscal
// period, a pass that stood aside for a running Stripe sync, and failures.
const props = defineProps<{ result: StripeBookResult }>()

const { t } = useI18n()

const reviewCount = computed(() =>
  Object.values(props.result.needs_review ?? {}).reduce((sum, n) => sum + n, 0),
)
const errorCount = computed(() => props.result.errors?.length ?? 0)
</script>

<template>
  <div class="mt-2" data-testid="stripe-book-summary">
    <p v-if="props.result.busy" class="text-yellow-800" data-testid="stripe-book-busy">
      {{ t('admin.bankImports.stripeBookBusy') }}
    </p>
    <p v-else>
      {{ t('admin.bankImports.stripeBookCounts', {
        charges: props.result.charges_booked,
        fees: props.result.fees_booked,
      }) }}
    </p>
    <p v-if="reviewCount" class="text-yellow-800" data-testid="stripe-book-review">
      {{ t('admin.bankImports.stripeBookReview', { count: reviewCount }) }}
    </p>
    <p v-if="props.result.deferred?.length" class="text-yellow-800" data-testid="stripe-book-deferred">
      {{ t('admin.bankImports.stripeBookDeferred', { count: props.result.deferred.length }) }}
    </p>
    <p v-if="errorCount" class="text-red-700" data-testid="stripe-book-errors">
      {{ t('admin.bankImports.stripeBookErrors', { count: errorCount }) }}
    </p>
    <p v-if="props.result.error" class="text-red-700" data-testid="stripe-book-error">
      {{ props.result.error }}
    </p>
  </div>
</template>
