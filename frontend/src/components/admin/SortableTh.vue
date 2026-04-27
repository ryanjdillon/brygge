<script setup lang="ts">
import { ArrowUp, ArrowDown } from 'lucide-vue-next'

// Reusable sortable table-header cell. The parent owns the sort state
// (server-side or client-side); this component just renders the label
// + active-direction arrow and emits click to toggle.
defineProps<{
  active: boolean
  dir: 'asc' | 'desc'
}>()

defineEmits<{
  (e: 'click'): void
}>()
</script>

<template>
  <th
    scope="col"
    class="cursor-pointer select-none px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 hover:text-gray-700"
    @click="$emit('click')"
  >
    <span class="inline-flex items-center gap-1">
      <slot />
      <ArrowUp v-if="active && dir === 'asc'" class="h-3 w-3" />
      <ArrowDown v-else-if="active && dir === 'desc'" class="h-3 w-3" />
    </span>
  </th>
</template>
