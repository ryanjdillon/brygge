<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { formatDateTimeMedium } from '@/lib/format'

// Small muted "Sist oppdatert: <timestamp>" line shown on faktura/economy
// surfaces to convey how fresh the payment data is. Renders nothing until a
// timestamp is available.
defineProps<{ at: string | null | undefined }>()

const { t } = useI18n()
</script>

<template>
  <p v-if="at" class="text-xs text-gray-400">
    {{ t('common.lastUpdated') }}: {{ formatDateTimeMedium(at) }}
  </p>
</template>
