<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  modelValue: string | null | undefined
  min?: string
  max?: string
  disabled?: boolean
  readonly?: boolean
  required?: boolean
  error?: string
  name?: string
  id?: string
  ariaLabel?: string
}>()

defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'blur', evt: FocusEvent): void
}>()

const cls = computed(() => [
  'block w-full rounded-md border bg-white px-3 py-1.5 text-sm focus:outline-none focus:ring-1 disabled:cursor-not-allowed disabled:bg-gray-50',
  props.error
    ? 'border-red-400 focus:border-red-500 focus:ring-red-500'
    : 'border-gray-300 hover:border-gray-400 focus:border-blue-500 focus:ring-blue-500',
])
</script>

<template>
  <div>
    <input
      :id="id"
      :name="name"
      type="date"
      :value="modelValue ?? ''"
      :min="min"
      :max="max"
      :disabled="disabled"
      :readonly="readonly"
      :required="required"
      :aria-label="ariaLabel"
      :aria-invalid="!!error"
      :class="cls"
      @input="$emit('update:modelValue', ($event.target as HTMLInputElement).value)"
      @blur="$emit('blur', $event)"
    />
    <p v-if="error" class="mt-1 text-xs text-red-600">{{ error }}</p>
  </div>
</template>
