<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { ApiError } from '@/composables/useApi'

const { t } = useI18n()

const props = defineProps<{ error: Error | null }>()

const codeMessages: Record<string, string> = {
  UNAUTHORIZED: 'error.unauthorized',
  FORBIDDEN: 'error.forbidden',
  NOT_FOUND: 'error.notFound',
  RATE_LIMITED: 'error.rateLimited',
  VALIDATION: 'error.validation',
}

function errorMessage(): string {
  if (!props.error) return ''
  if (props.error instanceof ApiError && props.error.code) {
    const key = codeMessages[props.error.code]
    if (key) return t(key)
  }
  return props.error.message
}
</script>

<template>
  <div v-if="error" role="alert" class="rounded-md bg-red-50 p-4 text-sm text-red-700">
    {{ errorMessage() }}
  </div>
</template>
