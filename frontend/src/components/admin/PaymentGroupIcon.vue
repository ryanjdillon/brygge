<script setup lang="ts">
import { computed } from 'vue'
import { iconMeta } from '@/lib/paymentFilterIcons'

// The round tinted badge of a payment filter group, on payment rows and in the
// group settings. Decorative: the label next to it carries the meaning.
const props = withDefaults(
  defineProps<{
    icon: string
    size?: 'md' | 'lg'
  }>(),
  { size: 'md' },
)

const meta = computed(() => iconMeta(props.icon))
</script>

<template>
  <span
    class="flex shrink-0 items-center justify-center rounded-full ring-1 ring-inset"
    :class="[meta.classes, size === 'lg' ? 'h-11 w-11' : 'h-9 w-9']"
    aria-hidden="true"
    data-testid="filter-group-icon"
    :data-icon="icon"
  >
    <component :is="meta.icon" :class="size === 'lg' ? 'h-5 w-5' : 'h-4 w-4'" />
  </span>
</template>
