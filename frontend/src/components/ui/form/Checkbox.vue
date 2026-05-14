<script setup lang="ts">
import { ref, watch } from 'vue'

const props = defineProps<{
  modelValue: boolean | null | undefined
  disabled?: boolean
  indeterminate?: boolean
  id?: string
  ariaLabel?: string
}>()

defineEmits<{
  (e: 'update:modelValue', value: boolean): void
}>()

const inputRef = ref<HTMLInputElement | null>(null)

watch(
  () => props.indeterminate,
  (v) => {
    if (inputRef.value) inputRef.value.indeterminate = !!v
  },
  { immediate: true, flush: 'post' },
)
</script>

<template>
  <label class="inline-flex cursor-pointer items-center gap-2 text-sm text-gray-800" :class="{ 'cursor-not-allowed opacity-60': disabled }">
    <input
      :id="id"
      ref="inputRef"
      type="checkbox"
      class="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-1 focus:ring-blue-500"
      :checked="!!modelValue"
      :disabled="disabled"
      :aria-label="ariaLabel"
      @change="$emit('update:modelValue', ($event.target as HTMLInputElement).checked)"
    />
    <span v-if="$slots.default"><slot /></span>
  </label>
</template>
