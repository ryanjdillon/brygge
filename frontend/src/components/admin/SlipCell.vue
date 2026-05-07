<script setup lang="ts">
// Renders a slip identity (`<section><number>`, e.g. "C1") in the same
// style used across admin tables. Section and number are concatenated
// without a separator since the section letter is short and adjacency
// reads as a single label.
//
// Defensive: if the stored number already starts with the section
// letter (legacy data), don't double it up.

const props = defineProps<{
  section?: string | null
  number?: string | null
}>()

function format(): string {
  const n = (props.number ?? '').trim()
  const s = (props.section ?? '').trim()
  if (!n && !s) return ''
  if (!s) return n
  if (!n) return s
  if (n.toUpperCase().startsWith(s.toUpperCase())) return n
  return s + n
}
</script>

<template>
  <span v-if="number || section" class="font-medium text-gray-900">
    {{ format() }}
  </span>
  <span v-else class="text-gray-400">—</span>
</template>
