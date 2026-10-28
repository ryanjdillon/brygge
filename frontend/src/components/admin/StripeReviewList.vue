<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import StripeTransactionCard from '@/components/admin/StripeTransactionCard.vue'
import {
  useStripeTransactions,
  STRIPE_TXN_PAGE_SIZE,
  type StripeTxnListStatus,
} from '@/composables/useStripeTransactions'

// The Stripe balance transactions waiting for a person, those set aside
// and those booked, one card each with the actions the server allows. The
// actions refresh this list themselves (useStripeTxnMutations).
const { t } = useI18n()

// ?stripe=<status> opens that tab and brings the list into view (the
// journal's refusal to void a Stripe entry links to the booked rows).
const route = useRoute()
const STATUSES: StripeTxnListStatus[] = ['needs_review', 'dismissed', 'booked']
const tab = ref<StripeTxnListStatus>('needs_review')
const section = ref<HTMLElement | null>(null)
watch(
  () => route.query.stripe,
  async (q) => {
    const requested = STATUSES.find((st) => st === q)
    if (!requested) return
    tab.value = requested
    await nextTick()
    section.value?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
  },
  { immediate: true },
)
const offset = ref(0)
watch(tab, () => (offset.value = 0))

const enabled = ref(true)
const { data: page, isLoading } = useStripeTransactions(tab, offset, enabled)
const items = computed(() => page.value?.items ?? [])
const pageNo = computed(() => Math.floor(offset.value / STRIPE_TXN_PAGE_SIZE) + 1)

// An action can empty the last page; step back rather than show nothing.
watch(items, (list) => {
  if (!list.length && offset.value > 0 && !isLoading.value) {
    offset.value = Math.max(0, offset.value - STRIPE_TXN_PAGE_SIZE)
  }
})

const TABS: { key: StripeTxnListStatus; label: string }[] = [
  { key: 'needs_review', label: 'admin.bankImports.stripeTxn.tabs.review' },
  { key: 'dismissed', label: 'admin.bankImports.stripeTxn.tabs.dismissed' },
  { key: 'booked', label: 'admin.bankImports.stripeTxn.tabs.booked' },
]
const EMPTY: Record<StripeTxnListStatus, string> = {
  needs_review: 'admin.bankImports.stripeTxn.empty.review',
  dismissed: 'admin.bankImports.stripeTxn.empty.dismissed',
  booked: 'admin.bankImports.stripeTxn.empty.booked',
}
</script>

<template>
  <section ref="section" class="mt-3 rounded-md border border-gray-200 bg-gray-50 p-3" data-testid="stripe-review-list">
    <h2 class="text-sm font-semibold text-gray-900">{{ t('admin.bankImports.stripeTxn.title') }}</h2>
    <div class="mt-2 flex gap-2" role="tablist">
      <button
        v-for="tb in TABS"
        :key="tb.key"
        type="button"
        role="tab"
        :aria-selected="tab === tb.key"
        class="min-h-11 flex-1 rounded-md border px-3 text-sm font-semibold sm:flex-none"
        :class="tab === tb.key ? 'border-brand-600 bg-brand-600 text-white' : 'border-gray-300 bg-white text-gray-700 hover:bg-gray-50'"
        :data-testid="`stripe-review-tab-${tb.key}`"
        @click="tab = tb.key"
      >
        {{ t(tb.label) }}
      </button>
    </div>

    <p v-if="isLoading" class="mt-3 text-sm text-gray-500">{{ t('common.loading') }}…</p>
    <p v-else-if="!items.length" class="mt-3 text-sm text-gray-500" data-testid="stripe-review-empty">
      {{ t(EMPTY[tab]) }}
    </p>
    <ul v-else class="mt-3 space-y-2">
      <li v-for="item in items" :key="item.id">
        <StripeTransactionCard :item="item" />
      </li>
    </ul>

    <div v-if="offset > 0 || page?.has_more" class="mt-3 flex items-center justify-between gap-2" data-testid="stripe-review-pager">
      <button
        type="button"
        class="min-h-11 rounded-md border border-gray-300 bg-white px-3 text-sm disabled:opacity-50"
        :disabled="offset === 0"
        data-testid="stripe-review-prev"
        @click="offset = Math.max(0, offset - STRIPE_TXN_PAGE_SIZE)"
      >
        {{ t('common.previous') }}
      </button>
      <span class="text-xs text-gray-600">{{ t('admin.bankImports.stripeTxn.page', { page: pageNo }) }}</span>
      <button
        type="button"
        class="min-h-11 rounded-md border border-gray-300 bg-white px-3 text-sm disabled:opacity-50"
        :disabled="!page?.has_more"
        data-testid="stripe-review-next"
        @click="offset += STRIPE_TXN_PAGE_SIZE"
      >
        {{ t('common.next') }}
      </button>
    </div>
  </section>
</template>
