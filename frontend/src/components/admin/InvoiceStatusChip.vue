<script setup lang="ts">
import { useI18n } from 'vue-i18n'

// Four-state chip for "did this member get billed (and pay) for X this
// fiscal period?" Maps backend state values:
//   ''      → no chip (returns null via v-if in the parent)
//   draft   → gray dashed outline, light fill
//   sent    → red dashed outline, light fill (outstanding)
//   paid    → green solid outline, light fill (received)
export type InvoiceState = '' | 'draft' | 'sent' | 'paid'

const props = defineProps<{
  state: InvoiceState
  /** Tooltip prefix, e.g. "Membership" — combined with the state label */
  label?: string
}>()

const { t } = useI18n()

const classByState: Record<Exclude<InvoiceState, ''>, string> = {
  draft: 'border-dashed border-gray-400 bg-gray-50 text-gray-700',
  sent: 'border-dashed border-red-400 bg-red-50 text-red-700',
  paid: 'border-solid border-emerald-500 bg-emerald-50 text-emerald-700',
}

function stateLabel(s: Exclude<InvoiceState, ''>): string {
  return t(`admin.invoiceStatus.${s}`)
}
</script>

<template>
  <span
    v-if="state"
    :class="[
      'inline-flex items-center rounded-full border px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide',
      classByState[state],
    ]"
    :title="props.label ? `${props.label}: ${stateLabel(state)}` : stateLabel(state)"
  >
    {{ stateLabel(state) }}
  </span>
  <span v-else class="text-xs text-gray-300">—</span>
</template>
