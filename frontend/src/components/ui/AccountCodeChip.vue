<script setup lang="ts">
import { computed } from 'vue'
import { Lock } from 'lucide-vue-next'

type AccountType = 'asset' | 'liability' | 'revenue' | 'expense'

const props = defineProps<{
  code: string
  accountType: AccountType
  isSystem?: boolean
  size?: 'sm' | 'md'
}>()

// Colors mirror the chip styling on the Chart of Accounts page so users
// see the same visual identity for an account wherever it appears.
const typeColors: Record<AccountType, string> = {
  asset: 'bg-blue-100 text-blue-800',
  liability: 'bg-amber-100 text-amber-800',
  revenue: 'bg-green-100 text-green-800',
  expense: 'bg-red-100 text-red-800',
}

const sizing = computed(() =>
  props.size === 'sm' ? 'px-1.5 py-0.5 text-xs' : 'px-2 py-0.5 text-sm',
)
</script>

<template>
  <span
    :class="[
      'inline-flex items-center gap-1 rounded font-mono font-semibold',
      typeColors[accountType],
      sizing,
    ]"
  >
    {{ code }}
    <Lock v-if="isSystem" class="h-3 w-3 opacity-50" />
  </span>
</template>
