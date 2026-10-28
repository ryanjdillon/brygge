<script setup lang="ts">
import { computed, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import { Apple, CreditCard, Smartphone, Wallet } from 'lucide-vue-next'

export type PaymentMethod = 'vipps' | 'card' | 'apple_pay' | 'google_pay'

const props = defineProps<{
  method: PaymentMethod | string
  cardBrand?: string | null
}>()

const { t } = useI18n()

// Card brands are trademarks, written the same in every language, so they
// are named here rather than translated. Stripe's codes not listed fall back
// to the generic card label.
const BRAND_NAMES: Record<string, string> = {
  visa: 'Visa',
  mastercard: 'Mastercard',
  amex: 'American Express',
  diners: 'Diners Club',
  discover: 'Discover',
  jcb: 'JCB',
  unionpay: 'UnionPay',
}

// Hues stay clear of the payment-kind badges in RecentPaymentsView (blue,
// teal, slate, amber, gray), so method and kind never read as one another;
// a plain card is the outlined chip.
const META: Record<PaymentMethod, { icon: Component; classes: string }> = {
  vipps: { icon: Smartphone, classes: 'bg-orange-50 text-orange-800 ring-orange-200' },
  card: { icon: CreditCard, classes: 'bg-white text-stone-700 ring-stone-300' },
  apple_pay: { icon: Apple, classes: 'bg-indigo-50 text-indigo-700 ring-indigo-200' },
  google_pay: { icon: Wallet, classes: 'bg-violet-50 text-violet-700 ring-violet-200' },
}

const known = computed<PaymentMethod>(() => (props.method in META ? (props.method as PaymentMethod) : 'card'))
const brand = computed(() => (props.cardBrand ? BRAND_NAMES[props.cardBrand.toLowerCase()] : undefined))
const meta = computed(() => META[known.value])

// A plain card is named by its brand when known; a wallet keeps its own name
// and shows the brand beside it where there is room. The text is the chip's
// accessible name: the brand stays readable to screen readers on a phone,
// where it is visually hidden.
const label = computed(() => {
  if (known.value === 'card') return brand.value ?? t('recentPayments.method.card')
  return t(`recentPayments.method.${known.value}`)
})
const walletBrand = computed(() => (known.value === 'apple_pay' || known.value === 'google_pay' ? brand.value : undefined))
const fullLabel = computed(() => (walletBrand.value ? `${label.value} · ${walletBrand.value}` : label.value))
</script>

<template>
  <span
    class="inline-flex min-w-0 max-w-full items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset"
    :class="meta.classes"
    :title="fullLabel"
    data-testid="payment-method-chip"
    :data-method="known"
  >
    <component :is="meta.icon" class="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
    <span class="min-w-0 truncate">{{ label }}<span v-if="walletBrand" class="sr-only sm:not-sr-only"> · {{ walletBrand }}</span></span>
  </span>
</template>
