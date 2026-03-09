<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { CheckCircle2 } from 'lucide-vue-next'
import type { Booking } from '@/composables/useBookings'

defineProps<{
  booking: Booking
}>()

const { t } = useI18n()
</script>

<template>
  <div class="mx-auto max-w-md rounded-lg border border-green-200 bg-green-50 p-6 text-center">
    <CheckCircle2 class="mx-auto h-12 w-12 text-green-600" />
    <h2 class="mt-4 text-xl font-semibold text-gray-900">{{ t('booking.confirmed') }}</h2>
    <p class="mt-2 text-sm text-gray-600">{{ t('booking.confirmedDesc') }}</p>

    <dl class="mt-6 space-y-2 text-left text-sm">
      <div class="flex justify-between">
        <dt class="text-gray-500">{{ t('booking.reference') }}</dt>
        <dd class="font-mono text-gray-900">{{ booking.id.slice(0, 8).toUpperCase() }}</dd>
      </div>
      <div class="flex justify-between">
        <dt class="text-gray-500">{{ t('booking.dates') }}</dt>
        <dd class="text-gray-900">{{ booking.start_date.slice(0, 10) }} — {{ booking.end_date.slice(0, 10) }}</dd>
      </div>
      <div class="flex justify-between">
        <dt class="text-gray-500">{{ t('booking.status') }}</dt>
        <dd class="text-gray-900">{{ booking.status }}</dd>
      </div>
    </dl>

    <div v-if="booking.boat_length_m" class="mt-4 rounded bg-amber-50 p-3 text-left text-xs text-amber-800">
      {{ t('booking.dimensionDisclaimer') }}
    </div>
  </div>
</template>
