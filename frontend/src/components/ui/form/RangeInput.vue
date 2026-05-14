<script setup lang="ts">
withDefaults(
  defineProps<{
    modelValue: number | null | undefined
    min?: number
    max?: number
    step?: number | string
    disabled?: boolean
    name?: string
    id?: string
    ariaLabel?: string
    showValue?: boolean
  }>(),
  { step: 1, showValue: true },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: number): void
}>()

function onInput(e: Event) {
  emit('update:modelValue', Number((e.target as HTMLInputElement).value))
}
</script>

<template>
  <div class="flex items-center gap-3">
    <input
      :id="id"
      :name="name"
      type="range"
      :value="modelValue ?? min ?? 0"
      :min="min"
      :max="max"
      :step="step"
      :disabled="disabled"
      :aria-label="ariaLabel"
      class="h-2 flex-1 cursor-pointer appearance-none rounded-lg bg-gray-200 accent-blue-600 disabled:cursor-not-allowed disabled:opacity-50"
      @input="onInput"
    />
    <span v-if="showValue" class="w-12 text-right text-sm tabular-nums text-gray-700">{{ modelValue ?? '—' }}</span>
  </div>
</template>
