<script setup lang="ts">
import { ref, onErrorCaptured } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
const error = ref<Error | null>(null)

onErrorCaptured((err) => {
  error.value = err instanceof Error ? err : new Error(String(err))
  console.error('Uncaught component error:', err)
  return false
})

function retry() {
  error.value = null
}

defineExpose({ error, retry })
</script>

<template>
  <div v-if="error" role="alert" class="flex flex-col items-center justify-center p-8 text-center">
    <p class="text-lg font-medium text-gray-900">{{ t('error.title') }}</p>
    <p class="mt-2 text-sm text-gray-500">{{ t('error.description') }}</p>
    <button
      class="mt-4 rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700"
      @click="retry"
    >
      {{ t('error.retry') }}
    </button>
  </div>
  <slot v-else />
</template>
