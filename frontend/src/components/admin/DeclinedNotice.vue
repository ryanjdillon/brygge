<script setup lang="ts">
import { useI18n } from 'vue-i18n'

// Shown in an upload or sync result when its automatic passes left payments a
// person had undone (ACC-13(b), BRY-565): such a payment is never booked
// automatically again, so it waits in Tildel for the treasurer. Renders
// nothing for a zero or absent count.
const props = defineProps<{ count?: number | null; testid?: string }>()
const { t } = useI18n()
</script>

<template>
  <p v-if="props.count" class="mt-1 text-yellow-800" :data-testid="props.testid ?? 'declined-notice'">
    {{ t('admin.bankImports.declinedNotice', { count: props.count }) }}
  </p>
</template>
