<script setup lang="ts">
// Faktura number rendered as a status-coloured chip (BRY-192), matching
// the solid light-fill chip style used on the Chart of Accounts table.
// Colour mirrors the Sent-tab paid-status filters: paid = green,
// waiting = yellow, past due = red.
export type PaidStatus = 'paid' | 'waiting' | 'past_due'

defineProps<{
  number: number | string
  status: PaidStatus
  /** Localised status label for the hover tooltip (colour alone isn't accessible). */
  title?: string
}>()

const colorByStatus: Record<PaidStatus, string> = {
  paid: 'bg-green-100 text-green-800',
  waiting: 'bg-yellow-100 text-yellow-800',
  past_due: 'bg-red-100 text-red-800',
}
</script>

<template>
  <span
    :class="[
      'inline-flex items-center rounded px-1.5 py-0.5 font-mono text-xs font-semibold',
      colorByStatus[status],
    ]"
    :title="title"
  >
    {{ number }}
  </span>
</template>
