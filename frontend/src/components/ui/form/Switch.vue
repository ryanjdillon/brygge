<script setup lang="ts">
defineProps<{
  modelValue: boolean
  disabled?: boolean
  id?: string
  ariaLabel?: string
}>()

defineEmits<{
  (e: 'update:modelValue', value: boolean): void
}>()
</script>

<template>
  <label
    class="inline-flex cursor-pointer items-center gap-3 text-sm text-gray-800"
    :class="{ 'cursor-not-allowed opacity-60': disabled }"
  >
    <button
      type="button"
      role="switch"
      :aria-checked="modelValue"
      :aria-label="ariaLabel"
      :id="id"
      :disabled="disabled"
      class="relative inline-flex h-5 w-9 shrink-0 rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-brand-500 focus:ring-offset-1"
      :class="modelValue ? 'bg-brand-600' : 'bg-gray-300'"
      @click="$emit('update:modelValue', !modelValue)"
    >
      <span
        class="inline-block h-4 w-4 translate-y-0.5 transform rounded-full bg-white shadow transition-transform"
        :class="modelValue ? 'translate-x-[1.125rem]' : 'translate-x-0.5'"
      />
    </button>
    <span v-if="$slots.default" class="select-none"><slot /></span>
  </label>
</template>
