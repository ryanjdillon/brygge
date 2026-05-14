<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    modelValue: number | null | undefined
    min?: number
    max?: number
    step?: number | string
    placeholder?: string
    disabled?: boolean
    readonly?: boolean
    required?: boolean
    error?: string
    name?: string
    id?: string
    ariaLabel?: string
  }>(),
  { step: 1 },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: number | null): void
  (e: 'blur', evt: FocusEvent): void
  (e: 'change', value: number | null): void
}>()

const cls = computed(() => [
  'block w-full rounded-md border bg-white px-3 py-1.5 text-sm focus:outline-none focus:ring-1 disabled:cursor-not-allowed disabled:bg-gray-50',
  props.error
    ? 'border-red-400 focus:border-red-500 focus:ring-red-500'
    : 'border-gray-300 hover:border-gray-400 focus:border-blue-500 focus:ring-blue-500',
])

function onInput(e: Event) {
  const raw = (e.target as HTMLInputElement).value
  if (raw === '') {
    emit('update:modelValue', null)
    return
  }
  const n = Number(raw)
  emit('update:modelValue', Number.isFinite(n) ? n : null)
}

function onChange(e: Event) {
  const raw = (e.target as HTMLInputElement).value
  if (raw === '') {
    emit('change', null)
    return
  }
  const n = Number(raw)
  emit('change', Number.isFinite(n) ? n : null)
}
</script>

<template>
  <div>
    <input
      :id="id"
      :name="name"
      type="number"
      :value="modelValue ?? ''"
      :min="min"
      :max="max"
      :step="step"
      :placeholder="placeholder"
      :disabled="disabled"
      :readonly="readonly"
      :required="required"
      :aria-label="ariaLabel"
      :aria-invalid="!!error"
      :class="cls"
      @input="onInput"
      @change="onChange"
      @blur="$emit('blur', $event)"
    />
    <p v-if="error" class="mt-1 text-xs text-red-600">{{ error }}</p>
  </div>
</template>
