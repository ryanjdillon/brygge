<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    modelValue: string | number | null | undefined
    type?: 'text' | 'email' | 'tel' | 'url' | 'search' | 'password'
    placeholder?: string
    disabled?: boolean
    readonly?: boolean
    required?: boolean
    maxlength?: number
    minlength?: number
    pattern?: string
    error?: string
    name?: string
    id?: string
    autocomplete?: string
    inputmode?: 'text' | 'numeric' | 'decimal' | 'email' | 'tel' | 'url' | 'search'
    textAlign?: 'left' | 'center' | 'right'
    inputClass?: string
    ariaLabel?: string
  }>(),
  { type: 'text', textAlign: 'left' },
)

defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'blur', evt: FocusEvent): void
  (e: 'focus', evt: FocusEvent): void
  (e: 'keydown', evt: KeyboardEvent): void
  (e: 'keyup', evt: KeyboardEvent): void
}>()

const alignClass = computed(() => ({
  left: 'text-left',
  center: 'text-center',
  right: 'text-right',
}[props.textAlign]))

const cls = computed(() => [
  'block w-full rounded-md border bg-white px-3 py-1.5 text-sm focus:outline-none focus:ring-1 disabled:cursor-not-allowed disabled:bg-gray-50',
  alignClass.value,
  props.error
    ? 'border-red-400 focus:border-red-500 focus:ring-red-500'
    : 'border-gray-300 hover:border-gray-400 focus:border-brand-500 focus:ring-brand-500',
  props.inputClass,
])
</script>

<template>
  <div>
    <div class="relative flex items-center">
      <span v-if="$slots.prefix" class="pointer-events-none absolute left-2 flex items-center text-gray-400">
        <slot name="prefix" />
      </span>
      <input
        :id="id"
        :name="name"
        :type="type"
        :value="modelValue ?? ''"
        :placeholder="placeholder"
        :disabled="disabled"
        :readonly="readonly"
        :required="required"
        :maxlength="maxlength"
        :minlength="minlength"
        :pattern="pattern"
        :autocomplete="autocomplete"
        :inputmode="inputmode"
        :aria-label="ariaLabel"
        :aria-invalid="!!error"
        :class="[cls, { 'pl-7': !!$slots.prefix, 'pr-7': !!$slots.suffix }]"
        @input="$emit('update:modelValue', ($event.target as HTMLInputElement).value)"
        @blur="$emit('blur', $event)"
        @focus="$emit('focus', $event)"
        @keydown="$emit('keydown', $event)"
        @keyup="$emit('keyup', $event)"
      />
      <span v-if="$slots.suffix" class="pointer-events-none absolute right-2 flex items-center text-gray-400">
        <slot name="suffix" />
      </span>
    </div>
    <p v-if="error" class="mt-1 text-xs text-red-600">{{ error }}</p>
  </div>
</template>
