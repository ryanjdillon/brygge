<script setup lang="ts">
import { computed, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import { RotateCcw, Undo2, TriangleAlert, ShieldX, ShieldCheck } from 'lucide-vue-next'
import { formatNOK, formatDate } from '@/lib/format'
import type { RefundChipKind } from '@/lib/refundState'

// The chip for a card payment's refund or dispute, used by the payment
// overview and the faktura list so the two read alike. Colour never carries
// the meaning alone: each kind has its own icon and label. With an href
// (a Stripe Dashboard page, for a treasurer) the chip is a link whose tap
// area is 44px tall while the pill stays small.
const props = defineProps<{
  kind: RefundChipKind
  /** Kr refunded; named in the label of a partial refund and the tooltip of a full one. */
  amount?: number | null
  /** When a dispute needs the club's answer by. */
  dueBy?: string | null
  href?: string
}>()

const { t } = useI18n()

// Hues stay clear of the payment-method chips (orange, stone, indigo,
// violet) and the kind badges (blue, teal, slate, amber, gray).
const META: Record<RefundChipKind, { icon: Component; classes: string }> = {
  refunded: { icon: RotateCcw, classes: 'bg-sky-50 text-sky-800 ring-sky-200' },
  partial: { icon: Undo2, classes: 'bg-yellow-50 text-yellow-800 ring-yellow-300' },
  disputed: { icon: TriangleAlert, classes: 'bg-red-50 text-red-700 ring-red-200' },
  dispute_lost: { icon: ShieldX, classes: 'bg-red-600 text-white ring-red-700' },
  dispute_won: { icon: ShieldCheck, classes: 'bg-emerald-50 text-emerald-700 ring-emerald-200' },
}
const meta = computed(() => META[props.kind])

const label = computed(() => {
  switch (props.kind) {
    case 'refunded':
      return t('admin.refund.chip.refunded')
    case 'partial':
      return props.amount ? t('admin.refund.chip.partialAmount', { amount: formatNOK(props.amount) }) : t('admin.refund.chip.partial')
    case 'disputed':
      return t('admin.refund.chip.disputed')
    case 'dispute_lost':
      return t('admin.refund.chip.disputeLost')
    default:
      return t('admin.refund.chip.disputeWon')
  }
})

const title = computed(() => {
  if ((props.kind === 'refunded' || props.kind === 'partial') && props.amount) {
    return t('admin.refund.chip.refundedTitle', { amount: formatNOK(props.amount) })
  }
  if (props.kind === 'disputed' && props.dueBy) {
    return t('admin.refund.chip.disputedDue', { date: formatDate(props.dueBy) })
  }
  return label.value
})
</script>

<template>
  <component
    :is="href ? 'a' : 'span'"
    :href="href"
    :target="href ? '_blank' : undefined"
    :rel="href ? 'noopener noreferrer' : undefined"
    :title="href ? `${title} – ${t('admin.refund.chip.openStripe')}` : title"
    :class="href ? 'inline-flex min-w-0 max-w-full items-center py-3 -my-3' : 'inline-flex min-w-0 max-w-full items-center'"
    data-testid="refund-state-chip"
    :data-kind="kind"
  >
    <span
      class="inline-flex min-w-0 max-w-full items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset"
      :class="[meta.classes, href ? 'hover:brightness-95' : '']"
    >
      <component :is="meta.icon" class="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
      <span class="min-w-0 truncate">{{ label }}</span>
    </span>
  </component>
</template>
