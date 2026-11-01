<script setup lang="ts">
import { useI18n } from 'vue-i18n'

// Shown in the result of an upload or sync whose Vipps booking did not run
// because another Vipps booking held the club's booking slot past a person's
// wait. The import or sync itself stands; only the booking waits for the next
// sync, so the notice is a note, not an error.
const { t } = useI18n()
</script>

<template>
  <p class="mt-2 text-yellow-800" data-testid="booking-deferred">
    {{ t('admin.bankImports.bookingDeferred') }}
  </p>
</template>
