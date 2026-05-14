<script setup lang="ts">
defineProps<{
  modelValue: string
  tabs: { value: string; label: string }[]
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
        class="border-b-2 px-3 py-2 text-sm font-medium transition-colors"
        :class="
          tab.value === modelValue
            ? 'border-blue-600 text-blue-700'
            : 'border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-700'
        "
        @click="$emit('update:modelValue', tab.value)"
      >
        {{ tab.label }}
      </button>
    </nav>
  </div>
</template>
