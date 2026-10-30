<script setup lang="ts">
import { useI18n } from 'vue-i18n'

// Shown in place of a faktura suggestion list whose request failed, so a
// failed load is never mistaken for "no matching faktura".
const emit = defineEmits<{ (e: 'retry'): void }>()
const { t } = useI18n()
</script>

<template>
  <div
    class="mt-3 flex flex-col gap-2 rounded-md bg-red-50 px-3 py-2 text-sm text-red-800 sm:flex-row sm:items-center sm:justify-between"
    role="alert"
    data-testid="suggestions-load-error"
  >
    <span>{{ t('admin.bankReconcile.suggestionsLoadFailed') }}</span>
    <button
      type="button"
      class="min-h-11 rounded-md border border-red-300 bg-white px-3 py-2 text-sm font-semibold text-red-800 hover:bg-red-100"
      data-testid="suggestions-load-retry"
      @click="emit('retry')"
    >
      {{ t('admin.bankReconcile.suggestionsRetry') }}
    </button>
  </div>
</template>
