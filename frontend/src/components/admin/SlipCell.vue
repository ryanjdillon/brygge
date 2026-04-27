<script setup lang="ts">
// Renders a slip identity (`<section> <number>`) in the same style used
// across admin tables. Single source of truth so the slip column on the
// members and slips pages stays visually identical.

defineProps<{
  section?: string | null
  number?: string | null
}>()
</script>

<template>
  <span v-if="number || section" class="font-medium text-gray-900">
    {{ section ? section + ' ' : '' }}{{ number }}
  </span>
  <span v-else class="text-gray-400">—</span>
</template>
