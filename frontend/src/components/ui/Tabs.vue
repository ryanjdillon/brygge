<script setup lang="ts">
defineProps<{
  modelValue: string
  tabs: { value: string; label: string; badge?: number | null }[]
}>()
defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()
</script>

<template>
  <div class="border-b border-gray-200">
    <nav class="-mb-px flex gap-4" role="tablist">
      <button
        v-for="tab in tabs"
        :key="tab.value"
        type="button"
        role="tab"
        :aria-selected="tab.value === modelValue"
        class="inline-flex items-center gap-2 border-b-2 px-3 py-2 text-sm font-medium transition-colors"
        :class="
          tab.value === modelValue
            ? 'border-brand-600 text-brand-700'
            : 'border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-700'
        "
        @click="$emit('update:modelValue', tab.value)"
      >
        {{ tab.label }}
        <span
          v-if="tab.badge && tab.badge > 0"
          class="inline-flex min-w-[1.25rem] items-center justify-center rounded-full bg-red-600 px-1.5 py-0.5 text-[10px] font-semibold text-white"
        >
          {{ tab.badge }}
        </span>
      </button>
    </nav>
  </div>
</template>
