<script setup lang="ts">
defineProps<{
  label?: string
  helperText?: string
  error?: string
  required?: boolean
  for?: string
}>()
</script>

<template>
  <div>
    <label v-if="label" :for="$props.for" class="mb-1 block text-xs font-medium text-gray-700">
      {{ label }}
      <span v-if="required" class="text-red-500">*</span>
    </label>
    <slot />
    <p v-if="error" class="mt-1 text-xs text-red-600">{{ error }}</p>
    <p v-else-if="helperText" class="mt-1 text-xs text-gray-500">{{ helperText }}</p>
  </div>
</template>
