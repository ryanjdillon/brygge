<script setup lang="ts">
defineProps<{
  modelValue: string | number | null
  value: string | number
  name?: string
  disabled?: boolean
  id?: string
}>()

defineEmits<{
  (e: 'update:modelValue', value: string | number): void
}>()
</script>

<template>
  <label class="inline-flex cursor-pointer items-center gap-2 text-sm text-gray-800" :class="{ 'cursor-not-allowed opacity-60': disabled }">
    <input
      :id="id"
      type="radio"
      :name="name"
      :value="value"
      :checked="modelValue === value"
      :disabled="disabled"
      class="h-4 w-4 border-gray-300 text-brand-600 focus:ring-1 focus:ring-brand-500"
      @change="$emit('update:modelValue', value)"
    />
    <span v-if="$slots.default"><slot /></span>
  </label>
</template>
